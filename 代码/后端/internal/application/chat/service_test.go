package chat

import (
	"context"
	"errors"
	domain "qingye/backend/internal/domain/chat"
	"qingye/backend/internal/domain/fault"
	"strings"
	"testing"
	"time"
)

type limiterStub struct {
	calls int
	err   error
}

func (l *limiterStub) Allow(context.Context, string, int, time.Duration) error {
	l.calls++
	return l.err
}

type repositoryStub struct {
	Repository
	sent          domain.SendInput
	sends         int
	err           error
	dispatchError error
}

func (r *repositoryStub) Send(_ context.Context, _ domain.Actor, _ string, in domain.SendInput) (map[string]any, error) {
	r.sends++
	r.sent = in
	return map[string]any{"data": map[string]any{"id": "42"}}, r.err
}
func (r *repositoryStub) Start(context.Context, domain.Actor, domain.StartInput, string) (string, error) {
	return "conversation", r.err
}
func (r *repositoryStub) Participants(context.Context, string) ([]string, error) {
	return nil, errors.New("notification lookup offline")
}
func (r *repositoryStub) Dispatch(context.Context) ([]string, error) { return nil, r.dispatchError }
func memberActor() domain.Actor {
	return domain.Actor{ID: "member", Role: "member", Surface: "public", SessionHash: "authenticated-session"}
}
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	v, ok := fault.As(err)
	if !ok || v.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}
func TestRejectsCrossSurfaceIdentityBeforeDependencies(t *testing.T) {
	s := New(nil, nil, nil, "public", nil)
	for _, actor := range []domain.Actor{{}, {ID: "admin", Role: "admin", Surface: "public", SessionHash: "session"}, {ID: "member", Role: "member", Surface: "admin", SessionHash: "session"}, {ID: "member", Role: "member", Surface: "public"}} {
		_, err := s.Send(context.Background(), actor, "conversation", domain.SendInput{Body: "hello", ClientID: "client"})
		wantCode(t, err, "UNAUTHENTICATED")
	}
}
func TestRejectsInvalidInputBeforePersistence(t *testing.T) {
	limit := &limiterStub{}
	s := New(nil, limit, nil, "public", nil)
	actor := memberActor()
	for _, in := range []domain.SendInput{{Body: "hello", ClientID: "bad\x00id"}, {Body: "bad\x00body", ClientID: "id"}, {Body: strings.Repeat("字", 2001), ClientID: "id"}, {Body: " ", ClientID: "id"}, {Body: string([]byte{255}), ClientID: "id"}} {
		_, err := s.Send(context.Background(), actor, "conversation", in)
		wantCode(t, err, "INVALID_MESSAGE")
	}
	_, err := s.Detail(context.Background(), actor, "bad\x00conversation")
	wantCode(t, err, "INVALID_CONVERSATION")
	_, err = s.List(context.Background(), actor, 100001)
	wantCode(t, err, "INVALID_CURSOR")
	_, err = s.Messages(context.Background(), actor, "conversation", "10", "5", true)
	wantCode(t, err, "INVALID_CURSOR")
	if limit.calls != 0 {
		t.Fatal("invalid input reached limiter")
	}
}
func TestSendNormalizesOnceAndFailsClosedOnLimiter(t *testing.T) {
	repo := &repositoryStub{}
	limit := &limiterStub{}
	s := New(repo, limit, nil, "public", nil)
	_, err := s.Send(context.Background(), memberActor(), "conversation", domain.SendInput{Body: "  消息  ", ClientID: "retry-stable"})
	if err != nil || repo.sends != 1 || repo.sent.Body != "消息" || repo.sent.ClientID != "retry-stable" {
		t.Fatalf("normalization: %+v %v", repo, err)
	}
	limit.err = fault.New(fault.Unavailable, "RATE_LIMIT_UNAVAILABLE", "暂不可用")
	_, err = s.Send(context.Background(), memberActor(), "conversation", domain.SendInput{Body: "hello", ClientID: "another"})
	wantCode(t, err, "RATE_LIMIT_UNAVAILABLE")
	if repo.sends != 1 {
		t.Fatal("failed limiter allowed persistence")
	}
}
func TestRepositoryFailureDoesNotLeakPrivateDiagnostic(t *testing.T) {
	repo := &repositoryStub{err: errors.New("private SQL diagnostic")}
	s := New(repo, &limiterStub{}, nil, "public", nil)
	_, err := s.Send(context.Background(), memberActor(), "conversation", domain.SendInput{Body: "hello", ClientID: "id"})
	wantCode(t, err, "DATABASE_UNAVAILABLE")
	if strings.Contains(err.Error(), "private") {
		t.Fatal("diagnostic leaked into public failure")
	}
	if !errors.Is(err, repo.err) {
		t.Fatal("diagnostic cause lost")
	}
}
func TestCommittedStartSurvivesDispatchFailure(t *testing.T) {
	repo := &repositoryStub{dispatchError: errors.New("dispatch offline")}
	s := New(repo, &limiterStub{}, nil, "public", func() string { return "generated" })
	out, err := s.Start(context.Background(), memberActor(), domain.StartInput{ProductID: "product"})
	if err != nil || out["data"].(map[string]any)["id"] != "conversation" {
		t.Fatalf("committed start: %v %v", out, err)
	}
}
