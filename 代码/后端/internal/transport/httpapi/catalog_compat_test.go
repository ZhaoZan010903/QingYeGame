package httpapi

import (
	"context"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/infrastructure/postgres"
)

func (a *App) validateProductInput(ctx context.Context, p ProductInput, complete bool) error {
	return postgres.ValidateProductInput(ctx, a.DB, p, complete)
}
func publicProductFields(p map[string]any)     { catalog.PublicFields(p) }
func productOrder(sort string) (string, error) { return postgres.ProductOrder(sort) }
