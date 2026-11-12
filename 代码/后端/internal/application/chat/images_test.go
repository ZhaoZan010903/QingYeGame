package chat

import (
	"context"
	domain "qingye/backend/internal/domain/chat"
	"testing"
)

func TestSendImageValidationBeforePersistence(t *testing.T) {
	repo := &repositoryStub{}
	limit := &limiterStub{}
	s := New(repo, limit, nil, "public", nil)
	for _, images := range [][]string{{""}, {"one", "one"}, {"bad\x00id"}, {"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"}} {
		if _, err := s.Send(context.Background(), memberActor(), "conversation", domain.SendInput{ClientID: "intent", ImageIDs: images}); err == nil {
			t.Fatal("invalid images accepted")
		}
	}
	if repo.sends != 0 || limit.calls != 0 {
		t.Fatal("invalid payload reached persistence/limiter")
	}
	_, err := s.Send(context.Background(), memberActor(), "conversation", domain.SendInput{Body: "  ", ClientID: "image-only", ImageIDs: []string{"one"}})
	if err != nil || repo.sends != 1 || repo.sent.Body != "" {
		t.Fatal("image-only failed", err)
	}
}
