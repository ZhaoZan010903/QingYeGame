//go:build oss_read

package mediafilesoss

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	application "qingye/backend/internal/application/media"
	domain "qingye/backend/internal/domain/media"
)

type localStub struct {
	application.Files
	calls int
	thumb bool
}

func (l *localStub) Open(_ context.Context, _, _ string, thumb bool) (*domain.File, error) {
	l.calls++
	l.thumb = thumb
	return &domain.File{Reader: &testReader{Reader: bytes.NewReader([]byte("local"))}}, nil
}

type testReader struct {
	*bytes.Reader
	closed int
}

func (r *testReader) Close() error { r.closed++; return nil }

type remoteStub struct {
	calls  int
	err    error
	reader *testReader
	ctx    context.Context
}

func (r *remoteStub) Open(ctx context.Context, _ Entry) (*domain.File, error) {
	r.calls++
	r.ctx = ctx
	if r.err != nil {
		return nil, r.err
	}
	r.reader = &testReader{Reader: bytes.NewReader([]byte("remote"))}
	return &domain.File{Reader: r.reader}, nil
}
func testStore(t *testing.T) (*Store, *localStub, *remoteStub) {
	t.Helper()
	local := &localStub{}
	remote := &remoteStub{}
	store, err := newStore(local, remote, &Manifest{entries: map[string]Entry{"id": {MediaID: "id", StorageKey: "legacy/key"}}})
	if err != nil {
		t.Fatal(err)
	}
	return store, local, remote
}
func TestNoOSSForUnmappedOrThumbnail(t *testing.T) {
	for _, tc := range []struct {
		name, key, id string
		thumb         bool
	}{
		{"unmapped", "images/new.jpg", "new", false},
		{"wrong-key", "images/wrong.jpg", "id", false},
		{"thumbnail", "legacy/key", "id", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, l, r := testStore(t)
			f, err := s.Open(context.Background(), tc.key, tc.id, tc.thumb)
			if err != nil {
				t.Fatal(err)
			}
			f.Reader.Close()
			if r.calls != 0 || l.calls != 1 || l.thumb != tc.thumb {
				t.Fatal("must use only the same local media")
			}
		})
	}
}
func TestOSSReaderLeaseSeekAndClose(t *testing.T) {
	s, l, r := testStore(t)
	f, err := s.Open(context.Background(), "legacy/key", "id", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.slots) != 1 || l.calls != 0 {
		t.Fatal("reader lease missing")
	}
	f.Reader.Seek(-3, io.SeekEnd)
	data, _ := io.ReadAll(f.Reader)
	if string(data) != "ote" {
		t.Fatal("range/seek lost")
	}
	f.Reader.Close()
	f.Reader.Close()
	if r.reader.closed != 1 || len(s.slots) != 0 || r.ctx.Err() != context.Canceled {
		t.Fatal("close must release once and cancel")
	}
}
func TestOSSOpenFailureFallsBackBeforeResponse(t *testing.T) {
	s, l, r := testStore(t)
	r.err = errors.New("remote failed")
	f, err := s.Open(context.Background(), "legacy/key", "id", false)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Reader.Close()
	data, _ := io.ReadAll(f.Reader)
	if string(data) != "local" || l.calls != 1 || len(s.slots) != 0 {
		t.Fatal("local fallback/lease incorrect")
	}
}
func TestCanceledRequestDoesNotOpenEitherStore(t *testing.T) {
	s, l, r := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.Open(ctx, "legacy/key", "id", false)
	if !errors.Is(err, context.Canceled) || l.calls != 0 || r.calls != 0 {
		t.Fatal("cancellation must prevent reads")
	}
}
func TestOSSReadCancellationPropagates(t *testing.T) {
	s, _, _ := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	f, err := s.Open(ctx, "legacy/key", "id", false)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Reader.Close()
	cancel()
	if _, err = f.Reader.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal("read ignored cancellation")
	}
	if _, err = f.Reader.Seek(0, io.SeekStart); !errors.Is(err, context.Canceled) {
		t.Fatal("seek ignored cancellation")
	}
}
func TestOSSOpenLimitFallsBackWithoutExtraConnection(t *testing.T) {
	s, l, r := testStore(t)
	var opened []io.Closer
	defer func() {
		for _, f := range opened {
			f.Close()
		}
	}()
	for i := 0; i < 9; i++ {
		f, err := s.Open(context.Background(), "legacy/key", "id", false)
		if err != nil {
			t.Fatal(err)
		}
		opened = append(opened, f.Reader)
	}
	if r.calls != 8 || l.calls != 1 || len(s.slots) != 8 {
		t.Fatal("remote concurrent reads must be bounded")
	}
}

type secretErrorReader struct{}

func (secretErrorReader) Read([]byte) (int, error) {
	return 0, errors.New("private signed URL and headers")
}
func (secretErrorReader) Seek(int64, int) (int64, error) {
	return 0, errors.New("private signed URL and headers")
}
func (secretErrorReader) Close() error { return errors.New("private signed URL and headers") }
func TestOSSStreamErrorsDoNotExposeRequestDetails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &leasedReader{ReadSeekCloser: secretErrorReader{}, ctx: ctx, cancel: cancel, release: func() {}}
	if err := r.safeError(errors.Join(errors.New("private request details"), io.EOF)); err != io.EOF {
		t.Fatal("wrapped EOF leaked request details")
	}
	if _, err := r.Read(make([]byte, 1)); err != errObject {
		t.Fatal("read error leaked")
	}
	if _, err := r.Seek(0, io.SeekStart); err != errObject {
		t.Fatal("seek error leaked")
	}
	if err := r.Close(); err != errObject {
		t.Fatal("close error leaked")
	}
}
