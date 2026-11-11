package management

import (
	"context"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
)

type Repository interface {
	Products(context.Context, management.ProductQuery) (map[string]any, error)
	Product(context.Context, string) (map[string]any, error)
	SaveProduct(ctx context.Context, actor management.Actor, id string, in catalog.ProductInput) (map[string]any, error)
	TransitionProduct(ctx context.Context, actor management.Actor, id, operation string, in management.TransitionInput) (map[string]any, error)
	FeatureProduct(ctx context.Context, actor management.Actor, id string, rank *int) (map[string]any, error)
	SaveCategory(ctx context.Context, actor management.Actor, id string, create bool, in management.CategoryInput) (map[string]any, error)
	SaveContact(ctx context.Context, actor management.Actor, id string, in management.ContactInput) (map[string]any, error)
	SavePage(ctx context.Context, actor management.Actor, slug string, in management.PageInput) (map[string]any, error)
	SaveSetting(ctx context.Context, actor management.Actor, key string, in management.SettingInput) (map[string]any, error)
	Users(ctx context.Context, actor management.Actor, q string, limit, offset int) (map[string]any, error)
	UpdateUser(ctx context.Context, actor management.Actor, id string, in management.UserInput) (map[string]any, error)
	Categories(context.Context) ([]map[string]any, error)
	Contacts(context.Context) ([]map[string]any, error)
	Pages(context.Context) ([]map[string]any, error)
	Settings(context.Context) ([]map[string]any, error)
}
type Service struct {
	store                  Repository
	validateRecommendation func([]byte) error
	defaultRecommendation  func() any
}

func New(store Repository, validate func([]byte) error, defaults func() any) *Service {
	return &Service{store: store, validateRecommendation: validate, defaultRecommendation: defaults}
}
func (s *Service) SaveCategory(ctx context.Context, actor management.Actor, id string, create bool, in management.CategoryInput) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	if id == "" {
		id = in.Code
	}
	if err := management.ValidateCategoryIdentity(id, in); err != nil {
		return nil, err
	}
	if create {
		if err := management.ValidateCategoryRules(in); err != nil {
			return nil, err
		}
	}
	return s.store.SaveCategory(ctx, actor, id, create, in)
}
func (s *Service) SaveContact(ctx context.Context, actor management.Actor, id string, in management.ContactInput) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	if err := management.ValidateContact(in); err != nil {
		return nil, err
	}
	return s.store.SaveContact(ctx, actor, id, in)
}
func (s *Service) SavePage(ctx context.Context, actor management.Actor, slug string, in management.PageInput) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	if err := management.ValidatePage(slug, in); err != nil {
		return nil, err
	}
	return s.store.SavePage(ctx, actor, slug, in)
}
func (s *Service) SaveSetting(ctx context.Context, actor management.Actor, key string, in management.SettingInput) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	if key != "home_presentation" && key != "recommendation" && key != "publish_form" {
		return nil, fault.New(fault.Invalid, "UNKNOWN_SETTING", "此配置项尚未开放编辑")
	}
	var err error
	if key == "publish_form" {
		err = management.ValidatePublishForm(in.Value)
	} else if key == "home_presentation" {
		err = management.ValidatePresentation(in.Value)
	} else {
		err = s.validateRecommendation(in.Value)
	}
	if err != nil {
		return nil, fault.New(fault.Invalid, "INVALID_SETTING", err.Error())
	}
	return s.store.SaveSetting(ctx, actor, key, in)
}
func (s *Service) Users(ctx context.Context, actor management.Actor, q string, limit, offset int) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 50 || offset < 0 || offset > 1000000 {
		return nil, fault.New(fault.Invalid, "INVALID_CURSOR", "分页参数无效")
	}
	if len(q) > 100 {
		return nil, fault.New(fault.Invalid, "INVALID_QUERY", "查询过长")
	}
	return s.store.Users(ctx, actor, q, limit, offset)
}
func (s *Service) UpdateUser(ctx context.Context, actor management.Actor, id string, in management.UserInput) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	if in.Enabled == nil || id == actor.ID {
		return nil, fault.New(fault.Invalid, "INVALID_USER", "不能通过此接口停用当前管理员")
	}
	return s.store.UpdateUser(ctx, actor, id, in)
}
func (s *Service) Categories(ctx context.Context, actor management.Actor) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	rows, err := s.store.Categories(ctx)
	if err != nil {
		return nil, fault.Wrap(fault.Unavailable, "DATABASE_UNAVAILABLE", "数据暂时不可用，请稍后重试", err)
	}
	return map[string]any{"data": rows}, nil
}
func (s *Service) Contacts(ctx context.Context, actor management.Actor) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	rows, err := s.store.Contacts(ctx)
	if err != nil {
		return nil, fault.Wrap(fault.Unavailable, "DATABASE_UNAVAILABLE", "数据暂时不可用，请稍后重试", err)
	}
	return map[string]any{"data": rows}, nil
}
func (s *Service) Pages(ctx context.Context, actor management.Actor) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	rows, err := s.store.Pages(ctx)
	if err != nil {
		return nil, fault.Wrap(fault.Unavailable, "DATABASE_UNAVAILABLE", "数据暂时不可用，请稍后重试", err)
	}
	return map[string]any{"data": rows}, nil
}
func (s *Service) Settings(ctx context.Context, actor management.Actor) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	rows, err := s.store.Settings(ctx)
	if err != nil {
		return nil, fault.Wrap(fault.Unavailable, "DATABASE_UNAVAILABLE", "数据暂时不可用，请稍后重试", err)
	}
	found := false
	for _, row := range rows {
		if row["key"] == "recommendation" {
			found = true
		}
	}
	if !found {
		rows = append(rows, map[string]any{"key": "recommendation", "value": s.defaultRecommendation()})
	}
	return map[string]any{"data": rows}, nil
}
func (s *Service) SaveProduct(ctx context.Context, actor management.Actor, id string, in catalog.ProductInput) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	return s.store.SaveProduct(ctx, actor, id, in)
}
func (s *Service) TransitionProduct(ctx context.Context, actor management.Actor, id, operation string, in management.TransitionInput) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	return s.store.TransitionProduct(ctx, actor, id, operation, in)
}
func (s *Service) FeatureProduct(ctx context.Context, actor management.Actor, id string, rank *int) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	if rank != nil && (*rank < 0 || *rank > 10000) {
		return nil, fault.New(fault.Invalid, "INVALID_RANK", "精选顺序无效")
	}
	return s.store.FeatureProduct(ctx, actor, id, rank)
}

func (s *Service) Products(ctx context.Context, actor management.Actor, in management.ProductQuery) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	if in.Limit < 1 || in.Limit > 50 || in.Offset < 0 || in.Offset > 1000000 {
		return nil, fault.New(fault.Invalid, "INVALID_CURSOR", "分页参数无效")
	}
	if in.Status != "" && in.Status != "all" && in.Status != "draft" && in.Status != "pending" && in.Status != "published" && in.Status != "rejected" && in.Status != "offline" && in.Status != "sold" && in.Status != "rented" {
		return nil, fault.New(fault.Invalid, "INVALID_STATUS", "商品状态无效")
	}
	return s.store.Products(ctx, in)
}
func (s *Service) Product(ctx context.Context, actor management.Actor, id string) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	return s.store.Product(ctx, id)
}

func (s *Service) PrivateCredentials(ctx context.Context, actor management.Actor, id string) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	repo, ok := s.store.(interface {
		PrivateCredentials(context.Context, management.Actor, string) (map[string]any, error)
	})
	if !ok {
		return nil, fault.New(fault.Unavailable, "CREDENTIAL_STORAGE_UNAVAILABLE", "私密资料暂不可用")
	}
	return repo.PrivateCredentials(ctx, actor, id)
}
