package security

import (
	"context"
	"errors"
	domain "qingye/backend/internal/domain/security"
	"testing"
)

type controlStore struct {
	ReadStore
	calls int
	err   error
}

func (s *controlStore) Control(context.Context, string, string, int) error { s.calls++; return s.err }

type controlAudit struct {
	startErr, endErr error
	outcome          string
}

func (a *controlAudit) Start(context.Context, string, string, string, string, int, string) error {
	return a.startErr
}
func (a *controlAudit) Complete(_ context.Context, _ string, outcome string) error {
	a.outcome = outcome
	return a.endErr
}
func TestReadControlAuditFailureAndAmbiguousRedisResponse(t *testing.T) {
	for _, name := range []string{"applied", "conflict", "unknown", "audit-start-failed", "audit-finish-failed"} {
		t.Run(name, func(t *testing.T) {
			store := &controlStore{}
			audit := &controlAudit{}
			s := ReadService{Store: store, Audit: audit, NewID: func() string { return "operation" }}
			switch name {
			case "conflict":
				store.err = errors.New("version conflict")
			case "unknown":
				store.err = domain.ErrUnavailable
			case "audit-start-failed":
				audit.startErr = errors.New("offline")
			case "audit-finish-failed":
				audit.endErr = errors.New("offline")
			}
			e := s.Control(context.Background(), "actor", "subject", "1", 600, "reason")
			if (e == nil) != (name == "applied") {
				t.Fatal("incorrect outcome", e)
			}
			if name == "audit-start-failed" {
				if store.calls != 0 {
					t.Fatal("changed Redis without durable intent")
				}
				return
			}
			expected := "applied"
			if name == "conflict" {
				expected = "not-applied"
			}
			if name == "unknown" {
				expected = "unknown"
			}
			if audit.outcome != expected {
				t.Fatalf("misreported ambiguous outcome: %s", audit.outcome)
			}
		})
	}
}
