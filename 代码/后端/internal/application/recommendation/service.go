package recommendation

import (
	"context"
	"errors"
	"log/slog"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/recommendation"
	"time"
)

type Repository interface {
	Preferences(context.Context, string) (bool, time.Time, error)
	History(context.Context, string, time.Time) ([]recommendation.View, error)
	SavePreference(context.Context, string, *bool, bool) error
	ItemScores(context.Context, map[string]float64) (map[string]float64, error)
	RefreshItemCF(context.Context, time.Time, string) error
	RefreshPopular(context.Context, time.Time, string) error
	Report(context.Context) (map[string]any, error)
}
type Service struct {
	repo         Repository
	newID        func() string
	publishBatch func(context.Context, string)
}

func New(repo Repository, newID func() string, publishBatch func(context.Context, string)) *Service {
	return &Service{repo, newID, publishBatch}
}

func (s *Service) Report(ctx context.Context) (map[string]any, error) {
	data, err := s.repo.Report(ctx)
	if err != nil {
		if _, known := fault.As(err); !known {
			err = fault.Wrap(fault.Unavailable, "DATABASE_UNAVAILABLE", "数据暂时不可用，请稍后重试", err)
		}
	}
	return data, err
}
func (s *Service) Profile(ctx context.Context, id string) (recommendation.Profile, error) {
	p := recommendation.NewProfile()
	if id == "" {
		return p, nil
	}
	enabled, reset, e := s.repo.Preferences(ctx, id)
	if e != nil {
		return p, e
	}
	p.Enabled = enabled
	if !enabled {
		return p, nil
	}
	rows, e := s.repo.History(ctx, id, reset)
	if e != nil {
		return p, e
	}
	p.ApplyHistory(rows)
	return p, nil
}
func (s *Service) Personalized(ctx context.Context, id string) (recommendation.Profile, string, error) {
	p, e := s.Profile(ctx, id)
	if e != nil {
		return p, "", e
	}
	if !p.Enabled {
		return p, "disabled", nil
	}
	if p.SampleCount == 0 {
		return p, "cold-start", nil
	}
	e = s.ApplyItemCF(ctx, &p)
	return p, "browsing", e
}
func (s *Service) SavePreference(ctx context.Context, id string, enabled *bool, reset bool) error {
	return s.repo.SavePreference(ctx, id, enabled, reset)
}
func (s *Service) ItemScores(ctx context.Context, seeds map[string]float64) (map[string]float64, error) {
	return s.repo.ItemScores(ctx, seeds)
}
func (s *Service) RefreshPopular(ctx context.Context, asOf time.Time) error {
	batch := s.newID()
	if e := s.repo.RefreshPopular(ctx, asOf, batch); e != nil {
		return e
	}
	if s.publishBatch != nil {
		s.publishBatch(ctx, batch)
	}
	return nil
}
func (s *Service) RefreshItemCF(ctx context.Context, asOf time.Time) error {
	return s.repo.RefreshItemCF(ctx, asOf, s.newID())
}
func (a *Service) ApplyItemCF(ctx context.Context, p *recommendation.Profile) error {
	scores, err := a.repo.ItemScores(ctx, p.Products)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A missing, stale or temporarily unavailable derived index must not turn
		// the catalog into an error page. The attribute path stays authoritative.
		if !errors.Is(err, context.DeadlineExceeded) {
			slog.Warn("ItemCF read unavailable; using attributes", "error", err)
		}
		scores = map[string]float64{}
	}
	p.Weights["itemcf"] = scores
	return nil
}
func (a *Service) RunItemCF(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := a.RefreshItemCF(ctx, time.Time{}); err != nil && ctx.Err() == nil {
			slog.Warn("ItemCF refresh failed; attribute recommendations remain available", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
