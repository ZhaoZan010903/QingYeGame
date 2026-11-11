package httpapi

import (
	"context"
	"io"
	"qingye/backend/internal/maintenance"
)

type ReconcileResult = maintenance.ReconcileResult

func (a *App) maintenanceService() *maintenance.Service {
	return &maintenance.Service{DB: a.DB, MediaDir: a.Config.MediaDir}
}
func (a *App) SeedUser(ctx context.Context, r io.Reader) (string, error) {
	return a.maintenanceService().SeedUser(ctx, r)
}
func (a *App) ImportLegacy(ctx context.Context, r io.Reader) (int, error) {
	return a.maintenanceService().ImportLegacy(ctx, r)
}
func (a *App) ReconcileMedia(ctx context.Context, limit int) (ReconcileResult, error) {
	return a.maintenanceService().ReconcileMedia(ctx, limit)
}

func (a *App) Migrate(ctx context.Context) error { return a.maintenanceService().Migrate(ctx) }
