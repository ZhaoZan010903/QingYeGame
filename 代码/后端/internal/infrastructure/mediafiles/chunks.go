package mediafiles

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	application "qingye/backend/internal/application/media"
	"qingye/backend/internal/domain/fault"
	"regexp"
)

var rawUploadKey = regexp.MustCompile(`^uploads/[a-zA-Z0-9_-]{1,100}\.raw$`)

type incomingChunk struct {
	root, key, temp string
	offset, size    int64
	minFreeBytes    int64
}

func (s *Store) ReceiveChunk(ctx context.Context, key string, offset, size int64, reader io.Reader) (application.Chunk, error) {
	if !rawUploadKey.MatchString(key) || offset < 0 || size < 1 || size > application.ChunkSize {
		return nil, fault.New(fault.Invalid, "INVALID_CONTENT_RANGE", "上传分块范围无效")
	}
	// The staged chunk and its appended destination briefly coexist.
	if err := CheckAvailable(filepath.Join(s.Root, "uploads"), s.MinFreeBytes, size*2); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err = root.MkdirAll("uploads", 0700); err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	temp := "uploads/.chunk-" + hex.EncodeToString(nonce[:])
	f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	n, copyErr := io.Copy(f, io.LimitReader(contextReader{ctx, reader}, size+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil || n != size {
		_ = root.Remove(temp)
		return nil, fault.New(fault.Invalid, "UPLOAD_SIZE_MISMATCH", "分块上传不完整或大小不一致")
	}
	return &incomingChunk{root: s.Root, key: key, temp: temp, offset: offset, size: size, minFreeBytes: s.MinFreeBytes}, nil
}
func (c *incomingChunk) Discard() {
	root, err := os.OpenRoot(c.root)
	if err == nil {
		defer root.Close()
		_ = root.Remove(c.temp)
	}
}
func (c *incomingChunk) Install(committed int64) error {
	if committed < c.offset {
		return fault.New(fault.Conflict, "UPLOAD_OFFSET_MISMATCH", "上传位置已改变")
	}
	// Already committed retries only compare bytes and must remain available at low disk space.
	if c.offset == committed {
		if err := CheckAvailable(filepath.Join(c.root, "uploads"), c.minFreeBytes, c.size); err != nil {
			return err
		}
	}
	root, err := os.OpenRoot(c.root)
	if err != nil {
		return err
	}
	defer root.Close()
	src, err := root.Open(c.temp)
	if err != nil {
		return err
	}
	defer src.Close()
	flags := os.O_RDWR | os.O_CREATE
	if c.offset < committed {
		flags = os.O_RDONLY
	}
	dst, err := root.OpenFile(c.key, flags, 0600)
	if err != nil {
		return err
	}
	defer dst.Close()
	info, err := dst.Stat()
	if err != nil {
		return err
	}
	if info.Size() < committed {
		return fault.New(fault.Unavailable, "UPLOAD_STORAGE_MISMATCH", "上传分块不完整，请重新上传")
	}
	if c.offset < committed {
		if c.offset+c.size > committed {
			return fault.New(fault.Conflict, "UPLOAD_OFFSET_MISMATCH", "上传位置已改变")
		}
		// Retrying a committed chunk must match its bytes; never silently accept a conflicting replay.
		have := make([]byte, c.size)
		want := make([]byte, c.size)
		if _, err = dst.ReadAt(have, c.offset); err != nil {
			return err
		}
		if _, err = io.ReadFull(src, want); err != nil {
			return err
		}
		if !bytes.Equal(have, want) {
			return fault.New(fault.Conflict, "UPLOAD_CHUNK_CONFLICT", "重复分块内容不同，请重新上传")
		}
		return nil
	}
	// PostgreSQL owns the committed boundary. Discard any tail left by a rolled-back commit.
	if err = dst.Truncate(committed); err != nil {
		return err
	}
	if _, err = dst.Seek(committed, io.SeekStart); err != nil {
		return err
	}
	n, err := io.Copy(dst, src)
	if err != nil {
		return err
	}
	if n != c.size {
		return errors.New("staged chunk size changed")
	}
	return dst.Sync()
}
