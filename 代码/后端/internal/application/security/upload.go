package security

import (
	"context"
	domain "qingye/backend/internal/domain/security"
)

type UploadRepository interface {
	Restriction(context.Context, string) (domain.UploadRestriction, error)
	Restrict(context.Context, string, string, domain.RestrictInput) (domain.UploadRestriction, error)
}
type UploadService struct{ Repository UploadRepository }

func (s *UploadService) Set(ctx context.Context, actor, role, owner string, in domain.RestrictInput) (domain.UploadRestriction, error) {
	if err := domain.ValidateRestriction(role, in); err != nil {
		return domain.UploadRestriction{}, err
	}
	return s.Repository.Restrict(ctx, actor, owner, in)
}
