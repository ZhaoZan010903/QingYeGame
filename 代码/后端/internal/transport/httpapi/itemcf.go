package httpapi

import (
	"context"
	"qingye/backend/internal/domain/recommendation"
	"time"
)

const itemCFVersion = recommendation.ItemCFVersion
const itemCFLock = recommendation.ItemCFLock

func (a *App) RunItemCF(ctx context.Context) { a.recommendationService().RunItemCF(ctx) }
func (a *App) RefreshItemCF(ctx context.Context) error {
	return a.recommendationService().RefreshItemCF(ctx, time.Time{})
}
func (a *App) refreshItemCFAt(ctx context.Context, at time.Time) error {
	return a.recommendationService().RefreshItemCF(ctx, at)
}
func (a *App) itemCFScores(ctx context.Context, seeds map[string]float64) (map[string]float64, error) {
	return a.recommendationService().ItemScores(ctx, seeds)
}
