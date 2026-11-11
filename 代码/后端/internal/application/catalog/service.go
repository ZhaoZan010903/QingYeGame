package catalog

import (
	"context"
	"encoding/json"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/recommendation"
	"time"
)

type Repository interface {
	Search(context.Context, catalog.SearchInput) ([]map[string]any, int, error)
	Section(context.Context, catalog.SearchInput) ([]map[string]any, error)
	FilterOptions(context.Context) (map[string]map[string]bool, error)
	PublicProduct(context.Context, string) (map[string]any, error)
	PublicImages(context.Context, string, string, int, int) ([]map[string]any, int, int, error)
	Categories(context.Context) ([]map[string]any, error)
	Contacts(context.Context, string) ([]map[string]any, error)
	Page(context.Context, string) (json.RawMessage, error)
	VisibleOwner(context.Context, string) (*string, error)
	RecordView(context.Context, string, string) (bool, error)
	Featured(context.Context, json.RawMessage) ([]map[string]any, error)
	HomePresentation(context.Context) (json.RawMessage, error)
	RecommendationState(context.Context) (map[string]any, time.Time, error)
}
type Personalizer interface {
	Personalized(context.Context, string) (recommendation.Profile, string, error)
}
type Service struct {
	repo         Repository
	personalizer Personalizer
}

func New(repo Repository, personalizer Personalizer) *Service { return &Service{repo, personalizer} }

func storageError(err error) error {
	if err == nil {
		return nil
	}
	if _, known := fault.As(err); known {
		return err
	}
	return fault.Wrap(fault.Unavailable, "DATABASE_UNAVAILABLE", "数据暂时不可用，请稍后重试", err)
}

type Page struct {
	Data       []map[string]any `json:"data"`
	Total      int              `json:"total"`
	NextCursor any              `json:"next_cursor"`
}
type ImagePage struct {
	Page
	Version int `json:"version"`
}

func (s *Service) Search(ctx context.Context, q catalog.SearchInput, identity string) (Page, error) {
	// 推荐权重只能来自服务计算，调用方不能覆盖排序画像。
	q.Weights = nil
	if e := q.Validate(); e != nil {
		return Page{}, storageError(e)
	}
	if len(q.Filters) > 0 {
		allowed, e := s.repo.FilterOptions(ctx)
		if e != nil {
			return Page{}, storageError(e)
		}
		if e = q.ValidateFilters(allowed); e != nil {
			return Page{}, storageError(e)
		}
	}
	if q.Sort == "recommended" {
		p, _, e := s.personalizer.Personalized(ctx, identity)
		if e != nil {
			return Page{}, storageError(e)
		}
		if p.Enabled && p.SampleCount > 0 {
			q.Weights = p.Weights
		}
	}
	rows, total, e := s.repo.Search(ctx, q)
	if e != nil {
		return Page{}, storageError(e)
	}
	for _, p := range rows {
		catalog.PublicSummary(p)
	}
	return Page{rows, total, catalog.NextPage(q.Offset, q.Limit, total)}, nil
}
func (s *Service) Product(ctx context.Context, id string) (map[string]any, error) {
	data, err := s.repo.PublicProduct(ctx, id)
	return data, storageError(err)
}
func (s *Service) Images(ctx context.Context, id, expected string, limit, offset int) (ImagePage, error) {
	if e := (catalog.SearchInput{Limit: limit, Offset: offset}).Validate(); e != nil {
		return ImagePage{}, storageError(e)
	}
	rows, total, version, e := s.repo.PublicImages(ctx, id, expected, limit, offset)
	if e != nil {
		return ImagePage{}, storageError(e)
	}
	return ImagePage{Page{rows, total, catalog.NextPage(offset, limit, total)}, version}, nil
}
func (s *Service) Categories(ctx context.Context) ([]map[string]any, error) {
	rows, e := s.repo.Categories(ctx)
	if e != nil {
		return nil, storageError(e)
	}
	for _, row := range rows {
		raw, _ := json.Marshal(row["filter_config"])
		var groups []catalog.FilterGroup
		_ = json.Unmarshal(raw, &groups)
		active := []catalog.FilterGroup{}
		for _, g := range groups {
			if g.Enabled != nil && !*g.Enabled {
				continue
			}
			options := []catalog.FilterOption{}
			for _, o := range g.Options {
				if o.Enabled == nil || *o.Enabled {
					options = append(options, o)
				}
			}
			g.Options = options
			active = append(active, g)
		}
		row["filter_config"] = active
	}
	return rows, nil
}
func (s *Service) Contacts(ctx context.Context, purpose string) ([]map[string]any, error) {
	data, err := s.repo.Contacts(ctx, purpose)
	return data, storageError(err)
}
func (s *Service) ContentPage(ctx context.Context, slug string) (json.RawMessage, error) {
	data, err := s.repo.Page(ctx, slug)
	return data, storageError(err)
}
func (s *Service) ViewEligible(ctx context.Context, id, member string) (bool, error) {
	owner, e := s.repo.VisibleOwner(ctx, id)
	if e != nil {
		return false, storageError(e)
	}
	return owner == nil || *owner != member, nil
}
func (s *Service) RecordView(ctx context.Context, id, hash string) (bool, error) {
	recorded, err := s.repo.RecordView(ctx, id, hash)
	return recorded, storageError(err)
}

type homeSection struct {
	Source string `json:"source"`
	Limit  int    `json:"limit"`
}

func (s *Service) Home(ctx context.Context, identity string) (map[string]any, error) {
	presentation, e := s.repo.HomePresentation(ctx)
	if e != nil {
		return nil, storageError(e)
	}
	featured, e := s.repo.Featured(ctx, presentation)
	if e != nil {
		return nil, storageError(e)
	}
	for _, p := range featured {
		catalog.PublicFields(p)
	}
	sections := []homeSection{{"hot", 6}, {"recommended", 6}, {"newest", 6}}
	if len(presentation) > 0 {
		var cfg struct {
			Sections []homeSection `json:"sections"`
		}
		if json.Unmarshal(presentation, &cfg) == nil {
			sections = cfg.Sections
		}
	}
	result := map[string]any{"featured": featured, "hot": []map[string]any{}, "recommended": []map[string]any{}, "newest": []map[string]any{}, "recommendation_source": "newest-fallback", "personalization_source": "cold-start"}
	for _, section := range sections {
		q := catalog.SearchInput{Sort: section.Source, Limit: section.Limit}
		if section.Limit > 24 || q.Validate() != nil {
			continue
		}
		if section.Source == "recommended" {
			p, source, e := s.personalizer.Personalized(ctx, identity)
			if e != nil {
				return nil, storageError(e)
			}
			if p.Enabled && p.SampleCount > 0 {
				q.Weights = p.Weights
			}
			result["personalization_source"] = source
		}
		items, e := s.repo.Section(ctx, q)
		if e != nil {
			return nil, storageError(e)
		}
		for _, p := range items {
			catalog.PublicSummary(p)
		}
		result[section.Source] = items
	}
	if len(presentation) > 0 {
		result["presentation"] = presentation
	}
	if state, databaseNow, e := s.repo.RecommendationState(ctx); e == nil {
		result["computed_at"] = state["computed_at"]
		result["rule_version"] = state["rule_version"]
		computed, _ := state["computed_at"].(string)
		at, e := time.Parse(time.RFC3339Nano, computed)
		age := databaseNow.Sub(at)
		if e == nil && age >= 0 && age <= time.Hour && state["has_engagement"] == true {
			result["recommendation_source"] = "local-view-favorite-score"
		}
	}
	return result, nil
}

func (s *Service) PublishForm(ctx context.Context) (json.RawMessage, error) {
	repo, ok := s.repo.(interface {
		PublishForm(context.Context) (json.RawMessage, error)
	})
	if !ok {
		return nil, fault.New(fault.Unavailable, "FORM_UNAVAILABLE", "发布表单暂不可用")
	}
	data, err := repo.PublishForm(ctx)
	return data, storageError(err)
}
