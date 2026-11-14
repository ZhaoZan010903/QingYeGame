package registration

import (
	"context"
	"errors"
	domain "qingye/backend/internal/domain/registration"
	"testing"
)

type accessStore struct {
	Repository
	created   []domain.Challenge
	challenge domain.Challenge
	stages    []string
}

func (s *accessStore) Create(_ context.Context, c domain.Challenge) error {
	s.created = append(s.created, c)
	return nil
}
func (s *accessStore) ClaimSend(context.Context, string, string, string, string, string) (domain.Challenge, error) {
	return s.challenge, nil
}
func (s *accessStore) Mark(_ context.Context, _ domain.Challenge, stage string, _ domain.Receipt) error {
	s.stages = append(s.stages, stage)
	return nil
}

type accessLimiter struct {
	Limiter
	challengeCalls, sendCalls int
	challengeErr, sendErr     error
}

func (l *accessLimiter) Challenge(context.Context, domain.Subject) error {
	l.challengeCalls++
	return l.challengeErr
}
func (l *accessLimiter) Send(context.Context, domain.Subject) error {
	l.sendCalls++
	return l.sendErr
}

func TestSMSChallengeAccessDefaultsToAllowlist(t *testing.T) {
	for _, purpose := range []string{"register", "login"} {
		for _, tt := range []struct {
			name, phone string
			public      bool
			phones      []string
			allowed     bool
		}{
			{"empty-default-closed", "13800000000", false, nil, false},
			{"allowlisted", "13800000000", false, []string{"13800000000"}, true},
			{"not-allowlisted", "13800000001", false, []string{"13800000000"}, false},
			{"explicit-public", "13800000001", true, nil, true},
			{"public-still-validates-phone", "123", true, nil, false},
		} {
			t.Run(purpose+"/"+tt.name, func(t *testing.T) {
				store, limit := &accessStore{}, &accessLimiter{}
				s := Service{Store: store, Limits: limit, NewID: func() string { return "challenge" }, PublicAccess: tt.public, AllowedPhones: tt.phones}
				who := domain.Subject{Binding: "browser", Hostname: "qingye.test"}
				c, err := s.CreatePurpose(context.Background(), who, tt.phone, purpose)
				if (err == nil) != tt.allowed {
					t.Fatalf("allowed=%v, error=%v", tt.allowed, err)
				}
				if !tt.allowed {
					if limit.challengeCalls != 0 || len(store.created) != 0 {
						t.Fatal("rejected phone reached challenge storage or budget")
					}
					return
				}
				if limit.challengeCalls != 1 || len(store.created) != 1 || c.Phone != tt.phone || c.Purpose != purpose || c.Binding != who.Binding || c.Hostname != who.Hostname {
					t.Fatal("accepted phone lost budget or challenge binding")
				}
			})
		}
	}
}

func TestSMSPublicChallengePreservesBudgetAndPurpose(t *testing.T) {
	store, limit := &accessStore{}, &accessLimiter{challengeErr: domain.ErrLimited}
	s := Service{Store: store, Limits: limit, PublicAccess: true}
	if _, err := s.CreatePurpose(context.Background(), domain.Subject{}, "13800000002", "register"); !errors.Is(err, domain.ErrLimited) || len(store.created) != 0 {
		t.Fatal("public mode bypassed challenge budget", err)
	}
	if _, err := s.CreatePurpose(context.Background(), domain.Subject{}, "13800000002", "reset"); !errors.Is(err, domain.ErrChallenge) || limit.challengeCalls != 1 {
		t.Fatal("public mode accepted unsupported purpose", err)
	}
}

type accessHuman struct {
	t    *testing.T
	pass bool
}

func (h accessHuman) Verify(_ context.Context, token, action, host, nonce, ip string) (bool, error) {
	if token != "human-proof" || action != domain.Action || host != "qingye.test" || nonce != "00000000-0000-0000-0000-000000000001" || ip != "192.0.2.2" {
		h.t.Fatal("public SMS lost human verification binding")
	}
	return h.pass, nil
}

type accessProvider struct {
	Provider
	calls int
	err   error
}

func (p *accessProvider) Send(context.Context, string, string) (domain.Receipt, error) {
	p.calls++
	return domain.Receipt{}, p.err
}

func TestSMSPublicSendPreservesHumanAndPaidSendBudget(t *testing.T) {
	for _, purpose := range []string{"register", "login"} {
		for _, tt := range []struct {
			name                  string
			human                 bool
			limitErr, providerErr error
			calls, budgetCalls    int
			stage                 string
		}{
			{"human-rejected", false, nil, nil, 0, 0, "failed"},
			{"budget-rejected", true, domain.ErrLimited, nil, 0, 1, "failed"},
			{"provider-uncertain-no-retry", true, nil, errors.New("unknown outcome"), 1, 1, "unknown"},
			{"success", true, nil, nil, 1, 1, "sent"},
		} {
			t.Run(purpose+"/"+tt.name, func(t *testing.T) {
				id := "00000000-0000-0000-0000-000000000001"
				store := &accessStore{challenge: domain.Challenge{ID: id, Hostname: "qingye.test", Stage: "claimed", Purpose: purpose}}
				limit := &accessLimiter{sendErr: tt.limitErr}
				provider, other := &accessProvider{err: tt.providerErr}, &accessProvider{}
				s := Service{Store: store, Limits: limit, Human: accessHuman{t: t, pass: tt.human}, NewID: func() string { return "attempt" }, PublicAccess: true, Provider: provider, LoginProvider: other}
				if purpose == "login" {
					s.Provider, s.LoginProvider = other, provider
				}
				err := s.SendPurpose(context.Background(), domain.Subject{IP: "192.0.2.2"}, "13800000002", id, "human-proof", purpose)
				if (err == nil) != (tt.stage == "sent") || provider.calls != tt.calls || other.calls != 0 || limit.sendCalls != tt.budgetCalls {
					t.Fatalf("public send controls changed: calls=%d other=%d budget=%d error=%v", provider.calls, other.calls, limit.sendCalls, err)
				}
				if len(store.stages) == 0 || store.stages[len(store.stages)-1] != tt.stage {
					t.Fatalf("lost persistent send outcome: %v", store.stages)
				}
				if provider.calls > 0 && store.stages[0] != "sending" {
					t.Fatal("paid send lacks persisted intent")
				}
			})
		}
	}
}
