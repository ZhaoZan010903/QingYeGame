package mediaobject

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	mediaapp "qingye/backend/internal/application/media"
	"qingye/backend/internal/application/medialifecycle"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/media"
	"qingye/backend/internal/infrastructure/mediafiles"
)

type ObjectRepository interface {
	ReadObject(context.Context, string, string) (medialifecycle.Object, error)
	StageCandidate(context.Context, string, string, string, string, string, string, int64) error
}

var privateReads = newReadSlots(4, 64, 5*time.Second)

// Files keeps chat/avatar/contact local and optionally reads verified historical
// product replicas. It retains the upload-capacity and resumable interfaces.
type Files struct {
	ProtectedReads       bool
	Local                *mediafiles.Store
	Staging, Published   medialifecycle.ObjectStore
	Repository           ObjectRepository
	Legacy               *LegacyFiles
	Environment, TempDir string
	NewID                func() string
}

func (s *Files) CheckCapacity(ctx context.Context, size int64) error {
	return s.Local.CheckCapacity(ctx, size)
}

func (s *Files) CheckProductCapacity(ctx context.Context, size int64) error {
	if err := s.Local.CheckCapacity(ctx, size); err != nil {
		return err
	}
	if staging, ok := s.Staging.(interface {
		CheckCapacity(context.Context, int64) error
	}); ok {
		return staging.CheckCapacity(ctx, size)
	}
	return nil
}

func (s *Files) Receive(ctx context.Context, key string, size int64, r io.Reader) (media.Staged, error) {
	return s.Local.Receive(ctx, key, size, r)
}
func (s *Files) ReceiveChunk(ctx context.Context, key string, offset, size int64, r io.Reader) (mediaapp.Chunk, error) {
	return s.Local.ReceiveChunk(ctx, key, offset, size, r)
}
func (s *Files) Sanitize(ctx context.Context, u media.Upload) (media.Processed, error) {
	if u.Purpose != "product" || !u.Managed {
		return s.Local.Sanitize(ctx, u)
	}
	generation := s.NewID()
	if err := os.MkdirAll(s.TempDir, 0700); err != nil {
		return media.Processed{}, ErrStorage
	}
	processDir, err := os.MkdirTemp(s.TempDir, "process-")
	if err != nil {
		return media.Processed{}, ErrStorage
	}
	defer os.RemoveAll(processDir) // Created by MkdirTemp under the private application spool.
	localUpload := u
	localUpload.ID = u.ID + "-" + generation
	p, err := s.Local.SanitizeTo(ctx, localUpload, processDir)
	if err != nil {
		return p, err
	}
	originalPath, err := mediafiles.ReadPath(processDir, p.Key)
	if err != nil {
		return media.Processed{}, ErrStorage
	}
	prefix := "r20/" + s.Environment + "/objects/" + u.ID + "/" + generation + "/"
	f, err := os.Open(originalPath)
	if err != nil {
		return media.Processed{}, ErrStorage
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return media.Processed{}, ErrStorage
	}
	h := sha256.New()
	_, err = io.Copy(h, contextReader{ctx, f})
	sha := hex.EncodeToString(h.Sum(nil))
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	key := prefix + "original"
	if err == nil {
		err = s.Repository.StageCandidate(ctx, u.ID, u.ProcessingToken, "original", key, p.MIME, sha, info.Size())
	}
	if err == nil {
		err = s.Staging.Put(ctx, key, f, info.Size(), p.MIME, sha)
	}
	if err == nil {
		err = s.Staging.Verify(ctx, key, info.Size(), sha)
	}
	if err != nil {
		return media.Processed{}, fault.New(fault.Unavailable, "IMAGE_STAGING_FAILED", "图片暂存失败，请重试")
	}
	return media.Processed{Key: key, MIME: p.MIME, Size: p.Size, Managed: []media.ManagedObject{{Variant: "original", Key: key, MIME: p.MIME, SHA256: sha, Size: info.Size()}}}, nil
}
func (s *Files) RemoveRaw(key string) {
	if strings.HasPrefix(key, "r20/") {
		return
	} // Persistent candidate cleanup owns uncommitted remote objects.
	s.Local.RemoveRaw(key)
}
func (s *Files) Open(ctx context.Context, key, id string, thumbnail bool) (*media.File, error) {
	if !strings.HasPrefix(key, "r20/") {
		if s.Legacy != nil {
			file, found, err := s.Legacy.Open(ctx, key, id)
			if found || err != nil {
				return file, err
			}
		}
		return s.Local.Open(ctx, key, id, thumbnail)
	}
	// R20 stores one display image. The compatibility preview route reads the same object.
	o, err := s.Repository.ReadObject(ctx, id, "original")
	if err != nil {
		return nil, fault.New(fault.NotFound, "MEDIA_FILE_MISSING", "图片暂不可用")
	}
	if o.Store == "oss" && !s.ProtectedReads {
		target, e := s.Published.SignedURL(ctx, o.Key, time.Minute)
		if e != nil {
			return nil, fault.New(fault.Unavailable, "MEDIA_UNAVAILABLE", "图片暂不可用")
		}
		return &media.File{MIME: o.MIME, RedirectURL: target}, nil
	}
	if o.Store != "staging" && o.Store != "oss" {
		return nil, fault.New(fault.NotFound, "MEDIA_FILE_MISSING", "图片暂不可用")
	}
	store := s.Staging
	if o.Store == "oss" {
		store = s.Published
	}
	file, err := privateRead(ctx, s.TempDir, o.Size, o.SHA256, o.MIME, func() (io.ReadCloser, error) { return store.Open(ctx, o.Key) })
	if err == nil && o.Store == "staging" {
		if local, ok := store.(interface{ ServerLocalSource() bool }); ok {
			file.ServerLocal = local.ServerLocalSource()
		}
	}
	return file, err
}

// The shared bound covers both R20 and historical reads. The slot remains held
// until the HTTP reader closes, so downloaded temporary files are bounded too.
func privateRead(ctx context.Context, temp string, size int64, sha, mime string, open func() (io.ReadCloser, error)) (*media.File, error) {
	if !validDigest(size, sha) {
		return nil, ErrIntegrity
	}
	release, err := privateReads.acquire(ctx)
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			release()
		}
	}()
	r, err := open()
	if err != nil {
		return nil, fault.New(fault.Unavailable, "MEDIA_UNAVAILABLE", "图片暂不可用")
	}
	defer r.Close()
	if err = os.MkdirAll(temp, 0700); err != nil {
		return nil, ErrStorage
	}
	f, err := os.CreateTemp(temp, "private-preview-*")
	if err != nil {
		return nil, ErrStorage
	}
	closeFile := func() { f.Close(); os.Remove(f.Name()) }
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(contextReader{ctx, r}, size+1))
	if err != nil || n != size || hex.EncodeToString(h.Sum(nil)) != sha {
		closeFile()
		return nil, ErrIntegrity
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		closeFile()
		return nil, ErrStorage
	}
	transferred = true
	return &media.File{Reader: &temporaryReadFile{File: f, release: release}, MIME: mime, Modified: time.Now()}, nil
}

type temporaryReadFile struct {
	*os.File
	release func()
	once    sync.Once
	err     error
}

func (f *temporaryReadFile) Close() error {
	f.once.Do(func() { name := f.Name(); f.err = f.File.Close(); _ = os.Remove(name); f.release() })
	return f.err
}
