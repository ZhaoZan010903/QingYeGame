package media

import (
	"context"
	"fmt"
	"io"
	domain "qingye/backend/internal/domain/media"
	"strings"
	"testing"
)

type resumeRepo struct {
	*repoStub
	renewed, canceled, claimed, released int
}

func (r *resumeRepo) WithUpload(ctx context.Context, _, _, owner string, fn func(domain.Upload, domain.Mutation) error) error {
	if owner != "" && owner != r.upload.Owner {
		return fmt.Errorf("owner mismatch")
	}
	r.locked = true
	defer func() { r.locked = false }()
	if err := fn(r.upload, r); err != nil {
		return err
	}
	r.committed = true
	return r.commitErr
}
func (r *resumeRepo) Progress(_ context.Context, offset int64, done bool) error {
	r.upload.Offset = offset
	if done {
		r.upload.State = "uploaded"
	}
	return nil
}
func (r *resumeRepo) Renew(_ context.Context, hash string) error {
	r.renewed++
	r.upload.TokenHash = hash
	return nil
}
func (r *resumeRepo) Cancel(context.Context) error {
	r.canceled++
	r.upload.State = "failed"
	r.upload.TokenHash = ""
	return nil
}
func (r *resumeRepo) Claim(_ context.Context, token string) error {
	r.claimed++
	r.upload.ProcessingToken = token
	r.upload.ProcessingValid = true
	return nil
}
func (r *resumeRepo) Release(context.Context) error {
	r.released++
	r.upload.ProcessingValid = false
	return nil
}

type resumeFiles struct {
	*fileStub
	afterStage, afterProcess func()
	installs                 int
}

func (f *resumeFiles) ReceiveChunk(context.Context, string, int64, int64, io.Reader) (Chunk, error) {
	if f.repo.locked {
		f.t.Fatal("network transfer held DB lock")
	}
	if f.afterStage != nil {
		f.afterStage()
	}
	return resumeChunk{f}, nil
}
func (f *resumeFiles) Sanitize(context.Context, domain.Upload) (domain.Processed, error) {
	if f.repo.locked {
		f.t.Fatal("managed object processing held DB lock")
	}
	if f.afterProcess != nil {
		f.afterProcess()
	}
	return domain.Processed{Key: "generation/original", MIME: "image/png", Size: 10}, nil
}

type resumeChunk struct{ f *resumeFiles }

func (c resumeChunk) Install(int64) error {
	if !c.f.repo.locked {
		c.f.t.Fatal("chunk installed without DB lock")
	}
	c.f.installs++
	return nil
}
func (c resumeChunk) Discard() { c.f.discarded++ }
func resumeFixture(t *testing.T) (*Service, *resumeRepo, *resumeFiles) {
	s, r, f := fixture(t)
	r.upload.Resumable = true
	r.upload.Purpose = "product"
	rr := &resumeRepo{repoStub: r}
	ff := &resumeFiles{fileStub: f}
	s.repo = rr
	s.files = ff
	return s, rr, ff
}
func TestContentRangeRejectsAmbiguousOverflowAndUnboundedChunks(t *testing.T) {
	for _, header := range []string{"", "bytes 0-0/*", "bytes -1-0/1", "bytes 0-1/1", "bytes 1-2/3", "bytes 0-2/5", "bytes 0-1048576/1048577", "bytes 0-0/9223372036854775808", "bytes 00-0/0", "bytes 0-9/10 extra"} {
		if _, _, _, err := ParseRange(header); err == nil {
			t.Fatalf("accepted %q", header)
		}
	}
	for _, header := range []string{"bytes 0-9/10", "bytes 0-1048575/1048580", "bytes 1048576-1048579/1048580"} {
		if _, _, _, err := ParseRange(header); err != nil {
			t.Fatalf("rejected %q: %v", header, err)
		}
	}
}
func TestChunkRechecksTicketAndStateAfterNetwork(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		mutate     func(*resumeRepo)
	}{
		{"ok", "", func(*resumeRepo) {}},
		{"revoked", "UPLOAD_TOKEN_INVALID", func(r *resumeRepo) { r.upload.TokenHash = "" }},
		{"canceled", "UPLOAD_CLOSED", func(r *resumeRepo) { r.upload.State = "failed" }},
		{"key-changed", "UPLOAD_CLOSED", func(r *resumeRepo) { r.upload.Key = "uploads/different.raw" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, r, f := resumeFixture(t)
			f.afterStage = func() { tc.mutate(r) }
			err := s.ReceiveRange(context.Background(), "id", strings.Repeat("a", 64), "bytes 0-9/10", 10, strings.NewReader("0123456789"))
			if tc.want != "" {
				code(t, err, tc.want)
				if f.installs != 0 {
					t.Fatal("invalid ticket installed")
				}
			} else if err != nil || r.upload.Offset != 10 || r.upload.State != "uploaded" {
				t.Fatalf("chunk not committed: %v %+v", err, r.upload)
			}
			if f.discarded != 1 {
				t.Fatal("temporary chunk leaked")
			}
		})
	}
}
func TestResumeStatusAndCancellationAreOwnerSurfaceScoped(t *testing.T) {
	s, r, _ := resumeFixture(t)
	r.upload.Valid = false
	if _, err := s.Status(context.Background(), actor(), "id"); err != nil || r.renewed != 1 {
		t.Fatalf("resume expired ticket %v", err)
	}
	other := actor()
	other.Surface = "admin"
	_, err := s.Status(context.Background(), other, "id")
	code(t, err, "UNAUTHENTICATED")
	if err = s.Cancel(context.Background(), actor(), "id"); err != nil {
		t.Fatal(err)
	}
	if err = s.Cancel(context.Background(), actor(), "id"); err != nil {
		t.Fatal("cancel not idempotent", err)
	}
	_, err = s.Status(context.Background(), actor(), "id")
	code(t, err, "UPLOAD_CLOSED")
}
func TestManagedProcessingUsesLeaseAndFinalStateCheck(t *testing.T) {
	s, r, f := resumeFixture(t)
	r.upload.State = "uploaded"
	f.afterProcess = func() { r.upload.State = "failed"; r.upload.ProcessingToken = "" }
	_, err := s.Complete(context.Background(), actor(), "id")
	code(t, err, "UPLOAD_CLOSED")
	if r.ready != 0 || r.claimed != 1 || f.removed != 0 {
		t.Fatal("canceled completion promoted or deleted candidates")
	}
	s, r, _ = resumeFixture(t)
	r.upload.State = "uploaded"
	r.upload.ProcessingValid = true
	_, err = s.Complete(context.Background(), actor(), "id")
	code(t, err, "IMAGE_PROCESSOR_BUSY")
	if r.claimed != 0 {
		t.Fatal("live processing lease replaced")
	}
}
