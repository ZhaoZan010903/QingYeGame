// Package mediaobject confines object operations to new, immutable R20 media keys.
package mediaobject

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"qingye/backend/internal/infrastructure/mediafiles"
	"regexp"
	"strings"
	"time"
)

var ErrStorage = errors.New("media object storage unavailable")
var ErrIntegrity = errors.New("media object integrity check failed")
var segment = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)

const MaxObjectBytes int64 = 64 << 20

type backend interface {
	put(context.Context, string, io.ReadSeeker, int64, string, string) error
	open(context.Context, string) (io.ReadCloser, error)
	remove(context.Context, string) error
	sign(context.Context, string, time.Duration) (string, error)
}
type Store struct {
	backend           backend
	environment, temp string
	minFree           int64
	capacityPath      string
	serverLocal       bool
}

// Only a configured loopback staging backend can assert server-local storage.
// An OSS object read into the private spool must never gain this classification.
func (s *Store) ServerLocalSource() bool { return s.serverLocal }

func ValidateKey(environment, key string) error {
	p := strings.Split(key, "/")
	if len(p) != 6 || p[0] != "r20" || p[1] != environment || p[2] != "objects" || (p[5] != "original" && p[5] != "preview") {
		return errors.New("invalid managed object key")
	}
	for _, s := range p {
		if !segment.MatchString(s) {
			return errors.New("invalid managed object key")
		}
	}
	return nil
}
func (s *Store) check(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ValidateKey(s.environment, key)
}
func safe(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrStorage
}
func validDigest(size int64, sha string) bool {
	b, e := hex.DecodeString(sha)
	return size > 0 && size <= MaxObjectBytes && e == nil && len(b) == 32 && sha == strings.ToLower(sha)
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}

func (s *Store) CheckCapacity(ctx context.Context, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if size <= 0 || size > MaxObjectBytes {
		return ErrIntegrity
	}
	if err := mediafiles.CheckAvailable(s.temp, s.minFree, size*2); err != nil {
		return err
	}
	if s.capacityPath != "" {
		return mediafiles.CheckAvailable(s.capacityPath, s.minFree, size*2)
	}
	return nil
}

func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, mime, sha string) error {
	if err := s.check(ctx, key); err != nil {
		return err
	}
	if r == nil || !validDigest(size, sha) || (mime != "image/png" && mime != "image/jpeg" && mime != "image/webp") {
		return ErrIntegrity
	}
	if err := s.CheckCapacity(ctx, size); err != nil {
		return err
	}
	if err := os.MkdirAll(s.temp, 0700); err != nil {
		return ErrStorage
	}
	f, err := os.CreateTemp(s.temp, "object-*")
	if err != nil {
		return ErrStorage
	}
	defer func() { f.Close(); os.Remove(f.Name()) }()
	// A bounded spool makes retries seekable and validates bytes before contacting storage.
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(contextReader{ctx, r}, size+1))
	if err != nil {
		return safe(ctx, err)
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != sha {
		return ErrIntegrity
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return ErrStorage
	}
	if err = s.backend.put(ctx, key, f, size, mime, sha); err != nil {
		// A retry may find its immutable object already stored after a lost response.
		if verifyErr := s.Verify(ctx, key, size, sha); verifyErr == nil {
			return nil
		}
		return safe(ctx, err)
	}
	return nil
}
func (s *Store) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := s.check(ctx, key); err != nil {
		return nil, err
	}
	r, err := s.backend.open(ctx, key)
	if err != nil {
		return nil, safe(ctx, err)
	}
	return &safeReader{ctx: ctx, r: r}, nil
}

type safeReader struct {
	ctx context.Context
	r   io.ReadCloser
}

func (r *safeReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	if err == io.EOF {
		return n, io.EOF
	}
	return n, safe(r.ctx, err)
}
func (r *safeReader) Close() error { return safe(r.ctx, r.r.Close()) }
func (s *Store) Verify(ctx context.Context, key string, size int64, sha string) error {
	if !validDigest(size, sha) {
		return ErrIntegrity
	}
	r, err := s.Open(ctx, key)
	if err != nil {
		return err
	}
	defer r.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(r, size+1))
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != sha {
		return ErrIntegrity
	}
	return nil
}
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.check(ctx, key); err != nil {
		return err
	}
	// No list/prefix deletion is exposed; SeaweedFS treats directory deletes specially.
	return safe(ctx, s.backend.remove(ctx, key))
}
func (s *Store) SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if err := s.check(ctx, key); err != nil {
		return "", err
	}
	if ttl <= 0 || ttl > time.Minute {
		return "", errors.New("invalid signed URL lifetime")
	}
	u, err := s.backend.sign(ctx, key, ttl)
	if err != nil {
		return "", safe(ctx, err)
	}
	return u, nil
}
