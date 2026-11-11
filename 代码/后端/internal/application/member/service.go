package member

import (
	"context"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/member"
)

type Repository interface {
	Detail(context.Context, member.Actor, string) (map[string]any, error)
	List(ctx context.Context, u member.Actor, in member.ListInput) (map[string]any, error)
	Favorite(ctx context.Context, u member.Actor, id string, add bool) (map[string]any, error)
	Create(ctx context.Context, u member.Actor, input catalog.ProductInput) (map[string]any, error)
	Edit(ctx context.Context, u member.Actor, id string, input catalog.ProductInput) (map[string]any, error)
	Transition(ctx context.Context, u member.Actor, id string, input member.TransitionInput, submit bool) (map[string]any, error)
	Gallery(ctx context.Context, u member.Actor, id string, in member.GalleryInput) (map[string]any, error)
	AppendImages(ctx context.Context, u member.Actor, id string, input member.AppendImagesInput) (map[string]any, error)
	ChangeImage(ctx context.Context, u member.Actor, id, mid string, input member.ChangeImageInput, remove bool) (map[string]any, error)
}
type Service struct{ store Repository }

func New(store Repository) *Service { return &Service{store: store} }
func (s *Service) List(ctx context.Context, u member.Actor, in member.ListInput) (map[string]any, error) {
	if err := member.ValidateActor(u); err != nil {
		return nil, err
	}
	if err := member.ValidateList(in); err != nil {
		return nil, err
	}
	return s.store.List(ctx, u, in)
}
func (s *Service) Favorite(ctx context.Context, u member.Actor, id string, add bool) (map[string]any, error) {
	if err := member.ValidateActor(u); err != nil {
		return nil, err
	}
	return s.store.Favorite(ctx, u, id, add)
}
func (s *Service) Create(ctx context.Context, u member.Actor, input catalog.ProductInput) (map[string]any, error) {
	if err := member.ValidateActor(u); err != nil {
		return nil, err
	}
	return s.store.Create(ctx, u, input)
}
func (s *Service) Edit(ctx context.Context, u member.Actor, id string, input catalog.ProductInput) (map[string]any, error) {
	if err := member.ValidateActor(u); err != nil {
		return nil, err
	}
	return s.store.Edit(ctx, u, id, input)
}
func (s *Service) Transition(ctx context.Context, u member.Actor, id string, input member.TransitionInput, submit bool) (map[string]any, error) {
	if err := member.ValidateActor(u); err != nil {
		return nil, err
	}
	return s.store.Transition(ctx, u, id, input, submit)
}
func (s *Service) Gallery(ctx context.Context, u member.Actor, id string, in member.GalleryInput) (map[string]any, error) {
	if err := member.ValidateActor(u); err != nil {
		return nil, err
	}
	if err := member.ValidatePage(in.Offset, in.Limit); err != nil {
		return nil, err
	}
	if in.ReviewID != "" && !u.Admin {
		return nil, fault.New(fault.Forbidden, "FORBIDDEN", "审核记录仅管理员可查看")
	}
	return s.store.Gallery(ctx, u, id, in)
}
func (s *Service) AppendImages(ctx context.Context, u member.Actor, id string, input member.AppendImagesInput) (map[string]any, error) {
	if err := member.ValidateActor(u); err != nil {
		return nil, err
	}
	if len(input.MediaIDs) < 1 || len(input.MediaIDs) > 20 {
		return nil, fault.New(fault.Invalid, "BATCH_SIZE", "每批追加1至20张，可继续分批添加")
	}
	return s.store.AppendImages(ctx, u, id, input)
}
func (s *Service) ChangeImage(ctx context.Context, u member.Actor, id, mid string, input member.ChangeImageInput, remove bool) (map[string]any, error) {
	if err := member.ValidateActor(u); err != nil {
		return nil, err
	}
	return s.store.ChangeImage(ctx, u, id, mid, input, remove)
}

func (s *Service) Detail(ctx context.Context, u member.Actor, id string) (map[string]any, error) {
	if err := member.ValidateActor(u); err != nil {
		return nil, err
	}
	return s.store.Detail(ctx, u, id)
}
