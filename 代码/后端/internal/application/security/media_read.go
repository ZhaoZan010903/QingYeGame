package security

import (
	"context"
	"errors"
	domain "qingye/backend/internal/domain/security"
)

type ReadStore interface {
	Begin(context.Context, domain.ReadSubject, string, int64) (domain.ReadDecision, error)
	Expand(context.Context, domain.ReadSubject, int64) (domain.ReadDecision, error)
	Finish(context.Context, domain.ReadSubject, string, int64) error
	Status(context.Context, domain.ReadSubject) (domain.ReadDecision, error)
	Records(context.Context) ([]domain.ReadRecord, map[string]string, error)
	Control(context.Context, string, string, int) error
}
type ReadAudit interface {
	Start(context.Context, string, string, string, string, int, string) error
	Complete(context.Context, string, string) error
}
type ReadService struct {
	Store ReadStore
	Audit ReadAudit
	NewID func() string
}

func (s *ReadService) Control(ctx context.Context, actor, id, version string, seconds int, reason string) error {
	auditID := s.NewID()
	if err := s.Audit.Start(ctx, auditID, actor, id, version, seconds, reason); err != nil {
		return domain.ErrUnavailable
	}
	err := s.Store.Control(ctx, id, version, seconds)
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	outcome := "applied"
	if err != nil {
		outcome = "not-applied"
	}
	if errors.Is(err, domain.ErrUnavailable) {
		outcome = "unknown"
	}
	if e := s.Audit.Complete(finishCtx, auditID, outcome); e != nil {
		return errors.New("audit outcome pending; refresh before retry")
	}
	return err
}
