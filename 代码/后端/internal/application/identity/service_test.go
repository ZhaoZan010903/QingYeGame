package identity

import (
	"context"
	"errors"
	"qingye/backend/internal/domain/fault"
	domain "qingye/backend/internal/domain/identity"
	"strings"
	"testing"
	"time"
)

type fakeStore struct {
	u              domain.User
	err            error
	reads, created int
	audience       string
	createErr      error
	verifiedHash   string
}

func (s *fakeStore) Credentials(context.Context, string) (domain.User, string, error) {
	s.reads++
	return s.u, "", s.err
}
func (s *fakeStore) Session(_ context.Context, _, surface, _ string) (*domain.User, error) {
	s.audience = surface
	return &s.u, s.err
}
func (s *fakeStore) CreateSession(_ context.Context, _ string, _ domain.User, verifiedHash, surface string, _ time.Time) error {
	s.created++
	s.audience = surface
	s.verifiedHash = verifiedHash
	return s.createErr
}
func (s *fakeStore) DeleteSession(_ context.Context, _, surface string) error {
	s.audience = surface
	return nil
}
func (s *fakeStore) UpdateNickname(context.Context, string, string) error { return nil }

type fakeLimiter struct {
	err   error
	calls int
}

func (l *fakeLimiter) Allow(context.Context, string, int, time.Duration) error {
	l.calls++
	return l.err
}

type fakeVerifier struct {
	err   error
	hash  string
	calls int
}

func (v *fakeVerifier) Compare(_ context.Context, hash, _ string) error {
	v.calls++
	v.hash = hash
	return v.err
}
func fixture() (*Service, *fakeStore, *fakeLimiter, *fakeVerifier) {
	r := &fakeStore{u: domain.User{ID: "member", Role: "member", Enabled: true}}
	l := &fakeLimiter{}
	v := &fakeVerifier{}
	return New(r, l, v, "public", func() string { return "private-token" }, func(value string) string { return "hash:" + value }), r, l, v
}
func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	e, ok := fault.As(err)
	if !ok || e.Code != code {
		t.Fatalf("wanted %s got %v", code, err)
	}
}
func TestLoginCannotCrossAudience(t *testing.T) {
	s, r, _, _ := fixture()
	r.u.Role = "admin"
	_, err := s.Login(context.Background(), LoginInput{Phone: "admin", Password: "valid"})
	assertCode(t, err, "INVALID_CREDENTIALS")
	if r.created != 0 {
		t.Fatal("cross-audience session issued")
	}
}
func TestUnknownUserUsesDummyHash(t *testing.T) {
	s, r, _, v := fixture()
	r.err = ErrNotFound
	_, err := s.Login(context.Background(), LoginInput{Phone: "unknown", Password: "valid"})
	assertCode(t, err, "INVALID_CREDENTIALS")
	if v.calls != 1 || v.hash != DummyPasswordHash || r.created != 0 {
		t.Fatal("unknown-account verification path changed")
	}
}
func TestRateLimiterFailsClosedBeforePasswordWork(t *testing.T) {
	s, r, l, v := fixture()
	l.err = fault.New(fault.Unavailable, "RATE_LIMIT_UNAVAILABLE", "unavailable")
	_, err := s.Login(context.Background(), LoginInput{Phone: "member", Password: "valid"})
	assertCode(t, err, "RATE_LIMIT_UNAVAILABLE")
	if v.calls != 0 || r.reads != 0 || r.created != 0 {
		t.Fatal("limiter failure allowed login work")
	}
}
func TestPasswordSaturationCannotCreateSession(t *testing.T) {
	s, r, _, v := fixture()
	v.err = ErrVerificationBusy
	_, err := s.Login(context.Background(), LoginInput{Phone: "member", Password: "valid"})
	assertCode(t, err, "LOGIN_BUSY")
	if r.created != 0 {
		t.Fatal("busy verifier created session")
	}
}
func TestSessionErrorsDoNotLeakInfrastructure(t *testing.T) {
	s, r, _, _ := fixture()
	r.err = errors.New("sensitive internal database details")
	_, err := s.Login(context.Background(), LoginInput{Phone: "member", Password: "valid"})
	assertCode(t, err, "DATABASE_UNAVAILABLE")
	if strings.Contains(err.Error(), "sensitive") {
		t.Fatal("private diagnostic leaked")
	}
}
func TestNicknameContractPreserved(t *testing.T) {
	s, r, _, _ := fixture()
	if _, err := s.UpdateNickname(context.Background(), r.u, strings.Repeat("叶", 30)); err != nil {
		t.Fatal(err)
	}
	_, err := s.UpdateNickname(context.Background(), r.u, strings.Repeat("叶", 31))
	assertCode(t, err, "INVALID_NICKNAME")
}

func TestInvalidTextNeverReachesPostgres(t *testing.T) {
	for _, value := range []string{"name\x00", string([]byte{0xff})} {
		s, r, _, v := fixture()
		_, err := s.Login(context.Background(), LoginInput{Phone: value, Password: "valid"})
		assertCode(t, err, "INVALID_CREDENTIALS")
		if r.reads != 0 || v.calls != 0 {
			t.Fatal("invalid login identifier reached database")
		}
		_, err = s.UpdateNickname(context.Background(), r.u, value)
		assertCode(t, err, "INVALID_NICKNAME")
	}
}

func TestCredentialsChangedDuringVerificationCannotIssueSession(t *testing.T) {
	s, r, _, _ := fixture()
	r.createErr = ErrCredentialsChanged
	_, err := s.Login(context.Background(), LoginInput{Phone: "member", Password: "valid"})
	assertCode(t, err, "INVALID_CREDENTIALS")
	if r.verifiedHash != DummyPasswordHash {
		t.Fatal("verified credential snapshot not passed to session creation")
	}
}
