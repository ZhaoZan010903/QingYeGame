package adminaccounts

import (
	"context"
	"errors"
	identityapp "qingye/backend/internal/application/identity"
	domain "qingye/backend/internal/domain/adminaccounts"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
	"qingye/backend/internal/domain/profile"
)

type Repository interface {
	List(context.Context, string, int, int) (map[string]any, error)
	Create(context.Context, management.Actor, domain.CreateInput, string) (map[string]any, error)
	Update(context.Context, management.Actor, string, domain.UpdateInput) (map[string]any, error)
	ResetPassword(context.Context, management.Actor, string, domain.PasswordInput, string) (map[string]any, error)
}
type Hasher interface {
	Hash(context.Context, string) (string, error)
}
type Service struct {
	store  Repository
	hasher Hasher
}

func New(store Repository, hasher Hasher) *Service { return &Service{store: store, hasher: hasher} }
func (s *Service) List(ctx context.Context, actor management.Actor, q string, limit, offset int) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	if len(q) > 100 || !profile.ValidText(q, 100) {
		return nil, fault.New(fault.Invalid, "INVALID_QUERY", "查询条件无效")
	}
	if limit < 1 || limit > 50 || offset < 0 || offset > 1000000 {
		return nil, fault.New(fault.Invalid, "INVALID_CURSOR", "分页参数无效")
	}
	return s.store.List(ctx, q, limit, offset)
}
func (s *Service) passwordHash(ctx context.Context, password string) (string, error) {
	hash, err := s.hasher.Hash(ctx, password)
	if errors.Is(err, identityapp.ErrVerificationBusy) {
		return "", fault.New(fault.Unavailable, "PASSWORD_BUSY", "密码处理繁忙，请稍后重试")
	}
	if err != nil {
		return "", fault.Wrap(fault.Unavailable, "PASSWORD_UNAVAILABLE", "密码处理暂不可用", err)
	}
	return hash, nil
}
func (s *Service) Create(ctx context.Context, actor management.Actor, in domain.CreateInput) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	if err := domain.ValidateCreate(&in); err != nil {
		return nil, err
	}
	hash, err := s.passwordHash(ctx, in.Password)
	if err != nil {
		return nil, err
	}
	return s.store.Create(ctx, actor, in, hash)
}
func (s *Service) Update(ctx context.Context, actor management.Actor, id string, in domain.UpdateInput) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	if err := domain.ValidateUpdate(actor.ID, id, &in); err != nil {
		return nil, err
	}
	return s.store.Update(ctx, actor, id, in)
}
func (s *Service) ResetPassword(ctx context.Context, actor management.Actor, id string, in domain.PasswordInput) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	if err := domain.ValidateReset(in); err != nil {
		return nil, err
	}
	hash, err := s.passwordHash(ctx, in.Password)
	if err != nil {
		return nil, err
	}
	return s.store.ResetPassword(ctx, actor, id, in, hash)
}
