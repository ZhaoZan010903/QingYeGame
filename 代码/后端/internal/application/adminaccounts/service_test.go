package adminaccounts

import (
	"context"
	identityapp "qingye/backend/internal/application/identity"
	domain "qingye/backend/internal/domain/adminaccounts"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
	"testing"
)

type fakeRepository struct {
	Repository
	calls int
}

func (r *fakeRepository) Create(context.Context, management.Actor, domain.CreateInput, string) (map[string]any, error) {
	r.calls++
	return nil, nil
}

type fakeHasher struct {
	calls int
	err   error
}

func (h *fakeHasher) Hash(context.Context, string) (string, error) {
	h.calls++
	return "test-hash", h.err
}
func TestAdministratorAuthorizationAndValidationPrecedeHash(t *testing.T) {
	r := &fakeRepository{}
	h := &fakeHasher{}
	s := New(r, h)
	in := domain.CreateInput{Login: "admin-one", Nickname: "管理员", Password: "password-1234", RequestID: "request-1234"}
	for _, actor := range []management.Actor{{}, {ID: "member", Role: "member"}} {
		if _, err := s.Create(context.Background(), actor, in); err == nil {
			t.Fatal("non-admin accepted")
		}
	}
	in.Password = "short"
	if _, err := s.Create(context.Background(), management.Actor{ID: "admin", Role: "admin"}, in); err == nil {
		t.Fatal("short password accepted")
	}
	if h.calls != 0 || r.calls != 0 {
		t.Fatal("rejected input reached password or storage work")
	}
}
func TestAdministratorHashCapacityIsBounded(t *testing.T) {
	r := &fakeRepository{}
	h := &fakeHasher{err: identityapp.ErrVerificationBusy}
	s := New(r, h)
	_, err := s.Create(context.Background(), management.Actor{ID: "admin", Role: "admin"}, domain.CreateInput{Login: "admin-one", Nickname: "管理员", Password: "password-1234", RequestID: "request-1234"})
	f, ok := fault.As(err)
	if !ok || f.Code != "PASSWORD_BUSY" || r.calls != 0 {
		t.Fatal("busy password work reached store", err)
	}
}
