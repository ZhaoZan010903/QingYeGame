package profile

import (
	"context"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/identity"
	domain "qingye/backend/internal/domain/profile"
	"strings"
)

type Repository interface {
	Profile(context.Context, string) (map[string]any, error)
	NicknameRequests(context.Context, string, int, int) (map[string]any, error)
	ReviewNickname(context.Context, string, string, domain.ReviewInput) error
	Avatars(context.Context, bool) (map[string]any, error)
	SaveAvatar(context.Context, string, string, domain.AvatarInput) (map[string]any, error)
}
type Service struct{ store Repository }

func New(store Repository) *Service { return &Service{store: store} }
func member(actor identity.User) error {
	if !actor.Enabled || actor.Role != "member" {
		return fault.New(fault.Forbidden, "FORBIDDEN", "需要会员身份")
	}
	return nil
}
func admin(actor identity.User) error {
	if !actor.Enabled || actor.Role != "admin" {
		return fault.New(fault.Forbidden, "FORBIDDEN", "需要管理员身份")
	}
	return nil
}
func (s *Service) Profile(ctx context.Context, actor identity.User) (map[string]any, error) {
	if err := member(actor); err != nil {
		return nil, err
	}
	return s.store.Profile(ctx, actor.ID)
}
func (s *Service) NicknameRequests(ctx context.Context, actor identity.User, status string, page, limit int) (map[string]any, error) {
	if err := admin(actor); err != nil {
		return nil, err
	}
	if status == "" {
		status = "pending"
	}
	if (status != "pending" && status != "approved" && status != "rejected") || page < 1 || page > 100000 || limit < 1 || limit > 50 {
		return nil, fault.New(fault.Invalid, "INVALID_QUERY", "审核列表筛选或分页无效")
	}
	return s.store.NicknameRequests(ctx, status, page, limit)
}
func (s *Service) ReviewNickname(ctx context.Context, actor identity.User, id string, in domain.ReviewInput) error {
	if err := admin(actor); err != nil {
		return err
	}
	in.Note = strings.TrimSpace(in.Note)
	if id == "" || len(id) > 100 || (in.Decision != "approved" && in.Decision != "rejected") || !domain.ValidText(in.Note, 300) {
		return fault.New(fault.Invalid, "INVALID_REVIEW", "请选择通过或拒绝，审核备注最多300字")
	}
	return s.store.ReviewNickname(ctx, actor.ID, id, in)
}
func (s *Service) Avatars(ctx context.Context, actor identity.User, all bool) (map[string]any, error) {
	if all {
		if err := admin(actor); err != nil {
			return nil, err
		}
	} else if !actor.Enabled || (actor.Role != "member" && actor.Role != "admin") {
		return nil, fault.New(fault.Forbidden, "FORBIDDEN", "请先登录")
	}
	return s.store.Avatars(ctx, all)
}
func (s *Service) SaveAvatar(ctx context.Context, actor identity.User, id string, in domain.AvatarInput) (map[string]any, error) {
	if err := admin(actor); err != nil {
		return nil, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if !domain.ValidNickname(in.Name) || in.SortOrder < 0 || in.SortOrder > 1000000 || in.Enabled == nil || (id != "" && (in.Version < 1 || len(id) > 100 || in.MediaID != "")) || (id == "" && (in.MediaID == "" || len(in.MediaID) > 100)) {
		return nil, fault.New(fault.Invalid, "INVALID_AVATAR", "头像名称、图片或排序无效，请重新检查")
	}
	return s.store.SaveAvatar(ctx, actor.ID, id, in)
}
