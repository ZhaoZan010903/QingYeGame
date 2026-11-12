package mediaobject

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"
	"time"
)

type memoryBackend struct {
	data  []byte
	calls int
	err   error
}

func (b *memoryBackend) put(_ context.Context, _ string, r io.ReadSeeker, _ int64, _, _ string) error {
	b.calls++
	if b.err != nil {
		return b.err
	}
	b.data, _ = io.ReadAll(r)
	return nil
}
func (b *memoryBackend) open(context.Context, string) (io.ReadCloser, error) {
	b.calls++
	if b.err != nil {
		return nil, b.err
	}
	return io.NopCloser(bytes.NewReader(b.data)), nil
}
func (b *memoryBackend) remove(context.Context, string) error { b.calls++; return b.err }
func (b *memoryBackend) sign(context.Context, string, time.Duration) (string, error) {
	b.calls++
	return "https://example.invalid/image", b.err
}
func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

const testKey = "r20/acceptance/objects/media0001/generation0001/original"

func TestManagedKeyBoundary(t *testing.T) {
	b := &memoryBackend{}
	s := &Store{backend: b, environment: "acceptance", temp: t.TempDir()}
	for _, key := range []string{"", "r20/acceptance/objects/", "legacy/image.jpg", "r20/test/objects/media0001/generation0001/original", "r20/acceptance/objects/media0001/../original", testKey + "/", testKey + "?extract=true", testKey + "%2f"} {
		if err := s.Delete(context.Background(), key); err == nil {
			t.Fatalf("allowed %q", key)
		}
	}
	if b.calls != 0 {
		t.Fatal("invalid keys reached remote storage")
	}
	if err := s.Delete(context.Background(), testKey); err != nil {
		t.Fatal(err)
	}
}
func TestPutChecksBytesBeforeRemoteWrite(t *testing.T) {
	data := bytes.Repeat([]byte("fixture"), 256)
	b := &memoryBackend{}
	s := &Store{backend: b, environment: "acceptance", temp: t.TempDir()}
	for _, tc := range []struct {
		size int64
		sha  string
	}{{int64(len(data) - 1), digest(data)}, {int64(len(data) + 1), digest(data)}, {int64(len(data)), digest([]byte("wrong"))}} {
		if err := s.Put(context.Background(), testKey, bytes.NewReader(data), tc.size, "image/png", tc.sha); err == nil {
			t.Fatal("bad payload accepted")
		}
	}
	if b.calls != 0 {
		t.Fatal("unverified input reached remote storage")
	}
	if err := s.Put(context.Background(), testKey, bytes.NewReader(data), int64(len(data)), "image/png", digest(data)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, b.data) {
		t.Fatal("payload changed")
	}
}
func TestVerifyReadsContentNotMetadata(t *testing.T) {
	data := bytes.Repeat([]byte("fixture"), 256)
	b := &memoryBackend{data: data}
	s := &Store{backend: b, environment: "acceptance", temp: t.TempDir()}
	if err := s.Verify(context.Background(), testKey, int64(len(data)), digest(data)); err != nil {
		t.Fatal(err)
	}
	b.data = append([]byte(nil), data...)
	b.data[0] ^= 1
	if err := s.Verify(context.Background(), testKey, int64(len(data)), digest(data)); err == nil {
		t.Fatal("corrupted destination accepted")
	}
}
func TestStorageErrorAndSignedURLBounds(t *testing.T) {
	b := &memoryBackend{err: errors.New("secret signed url must not leak")}
	s := &Store{backend: b, environment: "acceptance", temp: t.TempDir()}
	if err := s.Delete(context.Background(), testKey); !errors.Is(err, ErrStorage) {
		t.Fatal("unsafe error", err)
	}
	for _, ttl := range []time.Duration{0, -time.Second, 61 * time.Second} {
		if _, err := s.SignedURL(context.Background(), testKey, ttl); err == nil {
			t.Fatal("unbounded ttl accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Delete(ctx, testKey); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
