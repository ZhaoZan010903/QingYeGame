package security

import (
	"context"
	domain "qingye/backend/internal/domain/security"
)

// DeliveryStore converts a budgeted admission into a short delivery grant.
// OSS issuance has no Go transfer lease; Go consumes the grant once and takes
// the real transfer lease without charging the same image a second time.
type DeliveryStore interface {
	CompletePlan(context.Context, domain.ReadSubject, string, string, string, string, int64) error
	BeginGrant(context.Context, domain.ReadSubject, string, string, string) (domain.ReadDecision, error)
}
