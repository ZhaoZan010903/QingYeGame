package httpapi

import (
	"context"
	"qingye/backend/internal/application/recommendation"
	"qingye/backend/internal/infrastructure/postgres"
	"time"
)

func (a *App) recommendationService() *recommendation.Service {
	return recommendation.New(&postgres.RecommendationStore{DB: a.DB}, newID, func(ctx context.Context, batch string) {
		if a.Redis != nil {
			_ = a.Redis.Set(ctx, "qingye:recommendations:batch", batch, time.Hour).Err()
		}
	})
}
func (a *App) RefreshRecommendations(ctx context.Context) error {
	return a.recommendationService().RefreshPopular(ctx, time.Time{})
}
func (a *App) refreshRecommendationsAt(ctx context.Context, at time.Time) error {
	return a.recommendationService().RefreshPopular(ctx, at)
}
