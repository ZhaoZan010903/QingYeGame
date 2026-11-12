package media

import (
	"context"
	"testing"
	"time"

	"qingye/backend/internal/domain/fault"
	domain "qingye/backend/internal/domain/media"
)

func TestR20SecurityReadyCompleteBypassesBusyDecoderWithoutBypassingOwner(t *testing.T) {
	s, repo, files := resumeFixture(t)
	repo.upload.State = "ready"
	s.processors <- struct{}{}
	defer func() { <-s.processors }()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result, err := s.Complete(ctx, actor(), "id")
	if err != nil || result["data"].(map[string]any)["status"] != "ready" || repo.claimed != 0 || files.removed != 0 || len(s.processors) != 1 {
		t.Fatal("ready replay used decoder/storage or did not return ready", err)
	}
	other := actor()
	other.ID = "different-owner"
	_, err = s.Complete(ctx, other, "id")
	code(t, err, "NOT_FOUND")
}

type capacityFailureFiles struct {
	*fileStub
	failure error
}

func (f capacityFailureFiles) CheckCapacity(context.Context, int64) error { return f.failure }

func TestR20SecurityCapacityRefusalPreventsTicketReservation(t *testing.T) {
	s, old, files := fixture(t)
	repo := &securityCreateRepo{repoStub: old}
	s.repo = repo
	s.files = capacityFailureFiles{files, fault.New(fault.Unavailable, "STORAGE_FULL", "图片暂存空间不足")}
	_, err := s.Create(context.Background(), actor(), domain.CreateInput{Filename: "small.png", MIME: "image/png", Size: 10, Purpose: "product", Resumable: true})
	code(t, err, "STORAGE_FULL")
	if repo.created != 0 {
		t.Fatal("capacity refusal wrote a ticket")
	}
}
