package media

import (
	"context"
	"errors"
	domain "qingye/backend/internal/domain/media"
	"testing"
	"time"
)

type signingFiles struct {
	Files
	signed int
}

func (f *signingFiles) SignPublished(context.Context, domain.Upload, string, time.Duration) (domain.SignedImage, bool, error) {
	f.signed++
	return domain.SignedImage{URL: "https://example.invalid"}, true, nil
}

func TestMediaDeliveryAuthorizationDoesNotReadAndPrivateMediaNeverSigns(t *testing.T) {
	s, r, f := fixture(t)
	r.upload.State = "ready"
	r.upload.Public = true
	u, err := s.Authorize(context.Background(), "id", nil)
	if err != nil || u.ID != "id" || f.opened != 0 {
		t.Fatal("authorization fetched bytes", err)
	}
	signer := &signingFiles{Files: f}
	s.files = signer
	for _, purpose := range []string{"product", "chat", "avatar", "contact"} {
		u.Purpose = purpose
		u.PublishedProduct = purpose != "product"
		if _, found, err := s.SignPublished(context.Background(), u, "original", time.Minute); err != nil || found {
			t.Fatal("private/nonproduct signed", purpose, err)
		}
	}
	u.Purpose = "product"
	u.PublishedProduct = true
	if _, found, err := s.SignPublished(context.Background(), u, "original", time.Minute); err != nil || !found || signer.signed != 1 {
		t.Fatal("published image not signed", err)
	}
	r.upload.Public = false
	r.upload.Owner = "another"
	_, err = s.Authorize(context.Background(), "id", func(context.Context) (domain.Actor, error) { return actor(), nil })
	code(t, err, "NOT_FOUND")
	if f.opened != 0 {
		t.Fatal("private authorization read image")
	}
}

func TestMediaReadBudgetRunsAfterPermissionAndBeforeIO(t *testing.T) {
	s, r, f := fixture(t)
	r.upload.State = "ready"
	r.upload.Owner = "another"
	called := 0
	denied := errors.New("budget denied")
	before := func(domain.Upload) error { called++; return denied }
	_, err := s.OpenChecked(context.Background(), "id", false, func(context.Context) (domain.Actor, error) { return actor(), nil }, before)
	code(t, err, "NOT_FOUND")
	if called != 0 || f.opened != 0 {
		t.Fatal("unauthorized image consumed budget or opened")
	}
	r.upload.Public = true
	_, err = s.OpenChecked(context.Background(), "id", false, nil, before)
	if !errors.Is(err, denied) || called != 1 || f.opened != 0 {
		t.Fatal("budget bypassed", err)
	}
}
