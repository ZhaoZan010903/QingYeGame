package media

import (
	"context"
	"errors"
	"io"
	"qingye/backend/internal/domain/fault"
	domain "qingye/backend/internal/domain/media"
	"strings"
	"testing"
	"time"
)

type repoStub struct {
	Repository
	upload                  domain.Upload
	locked                  bool
	committed               bool
	commitErr               error
	failed, ready, uploaded int
}

func (r *repoStub) Ticket(context.Context, string, string) (domain.Upload, error) {
	return r.upload, nil
}
func (r *repoStub) Lookup(context.Context, string) (domain.Upload, error)      { return r.upload, nil }
func (r *repoStub) OwnedProduct(context.Context, string, string) (bool, error) { return false, nil }
func (r *repoStub) WithUpload(ctx context.Context, _ string, _ string, _ string, fn func(domain.Upload, domain.Mutation) error) error {
	r.locked = true
	defer func() { r.locked = false }()
	if err := fn(r.upload, r); err != nil {
		return err
	}
	if r.commitErr != nil {
		return r.commitErr
	}
	r.committed = true
	return nil
}
func (r *repoStub) Uploaded(context.Context) error                { r.uploaded++; return nil }
func (r *repoStub) Failed(context.Context) error                  { r.failed++; return nil }
func (r *repoStub) Ready(context.Context, domain.Processed) error { r.ready++; return nil }

type fileStub struct {
	Files
	repo                                  *repoStub
	t                                     *testing.T
	installed, discarded, removed, opened int
	afterTransfer                         func()
	processErr                            error
}

func (f *fileStub) Receive(context.Context, string, int64, io.Reader) (domain.Staged, error) {
	if f.repo.locked {
		f.t.Fatal("slow upload holds database transaction")
	}
	if f.afterTransfer != nil {
		f.afterTransfer()
	}
	return f, nil
}
func (f *fileStub) Install() error {
	if !f.repo.locked {
		f.t.Fatal("file installed without ticket lock")
	}
	f.installed++
	return nil
}
func (f *fileStub) Discard() { f.discarded++ }
func (f *fileStub) Sanitize(context.Context, domain.Upload) (domain.Processed, error) {
	if !f.repo.locked {
		f.t.Fatal("image sanitized without media lock")
	}
	return domain.Processed{Key: "images/id.png", MIME: "image/png", Size: 100}, f.processErr
}
func (f *fileStub) RemoveRaw(string) {
	if !f.repo.committed {
		f.t.Fatal("original removed before commit")
	}
	f.removed++
}
func (f *fileStub) Open(context.Context, string, string, bool) (*domain.File, error) {
	f.opened++
	return &domain.File{}, nil
}

type allowAll struct{}

func (allowAll) Allow(context.Context, string, int, time.Duration) error { return nil }
func fixture(t *testing.T) (*Service, *repoStub, *fileStub) {
	r := &repoStub{upload: domain.Upload{ID: "id", Owner: "member", Key: "uploads/id.raw", State: "pending", TokenHash: strings.Repeat("a", 64), Size: 10, Valid: true}}
	f := &fileStub{repo: r, t: t}
	s := New(r, f, allowAll{}, "public", 100, make(chan struct{}, 1), func() string { return "id" }, func() string { return strings.Repeat("a", 64) }, func(v string) string { return v })
	return s, r, f
}
func code(t *testing.T, err error, want string) {
	t.Helper()
	f, ok := fault.As(err)
	if !ok || f.Code != want {
		t.Fatalf("error %v, want %s", err, want)
	}
}
func actor() domain.Actor { return domain.Actor{ID: "member", Role: "member", Surface: "public"} }
func TestUploadStreamsBeforeTransactionAndRechecksTicket(t *testing.T) {
	for _, test := range []struct {
		name      string
		mutate    func(*repoStub)
		want      string
		installed int
	}{{"success", func(*repoStub) {}, "", 1}, {"expired", func(r *repoStub) { r.upload.Valid = false }, "UPLOAD_TOKEN_INVALID", 0}, {"changed", func(r *repoStub) { r.upload.Key = "uploads/changed.raw" }, "UPLOAD_CLOSED", 0}, {"replay", func(r *repoStub) { r.upload.State = "uploaded" }, "", 0}} {
		t.Run(test.name, func(t *testing.T) {
			s, r, f := fixture(t)
			f.afterTransfer = func() { test.mutate(r) }
			err := s.Receive(context.Background(), "id", strings.Repeat("a", 64), 10, strings.NewReader("0123456789"))
			if test.want != "" {
				code(t, err, test.want)
			} else if err != nil {
				t.Fatal(err)
			}
			if f.installed != test.installed || f.discarded != 1 {
				t.Fatalf("install=%d discard=%d", f.installed, f.discarded)
			}
		})
	}
}
func TestCompleteRetainsOriginalAfterCommitFailure(t *testing.T) {
	s, r, f := fixture(t)
	r.upload.State = "uploaded"
	r.commitErr = errors.New("commit unavailable")
	_, err := s.Complete(context.Background(), actor(), "id")
	if !errors.Is(err, r.commitErr) || f.removed != 0 || r.ready != 1 {
		t.Fatalf("commit failure: %v %+v", err, f)
	}
	if len(s.processors) != 0 {
		t.Fatal("decoder slot leaked")
	}
}
func TestInvalidImageCommitsFailureBeforeCleanup(t *testing.T) {
	s, r, f := fixture(t)
	r.upload.State = "uploaded"
	f.processErr = fault.New(fault.Invalid, "INVALID_IMAGE", "图片无法解码")
	_, err := s.Complete(context.Background(), actor(), "id")
	code(t, err, "INVALID_IMAGE")
	if r.failed != 1 || r.ready != 0 || f.removed != 1 {
		t.Fatalf("invalid image state: %+v %+v", r, f)
	}
}
func TestPrivateThumbnailRequiresAuthorizationBeforeFileAccess(t *testing.T) {
	s, r, f := fixture(t)
	r.upload.State = "ready"
	r.upload.Owner = "another"
	_, err := s.Open(context.Background(), "id", true, func(context.Context) (domain.Actor, error) { return actor(), nil })
	code(t, err, "NOT_FOUND")
	if f.opened != 0 {
		t.Fatal("private thumbnail opened")
	}
	r.upload.Public = true
	_, err = s.Open(context.Background(), "id", true, func(context.Context) (domain.Actor, error) {
		t.Fatal("public image triggered session lookup")
		return domain.Actor{}, nil
	})
	if err != nil || f.opened != 1 {
		t.Fatalf("public image: %v", err)
	}
}
func TestMediaRejectsCrossSurfaceActorAndInvalidIdentifiers(t *testing.T) {
	s, _, _ := fixture(t)
	_, err := s.Complete(context.Background(), domain.Actor{ID: "member", Role: "admin", Surface: "public"}, "id")
	code(t, err, "UNAUTHENTICATED")
	for _, id := range []string{"bad\x00id", string([]byte{255}), strings.Repeat("a", 101)} {
		_, err := s.Open(context.Background(), id, false, nil)
		code(t, err, "INVALID_MEDIA")
	}
}
