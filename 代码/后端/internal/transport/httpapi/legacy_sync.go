package httpapi

import (
	"errors"
	"net/http"
	"reflect"
	"slices"

	syncapp "qingye/backend/internal/application/legacysync"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/infrastructure/postgres"
)

func (a *App) registerLegacySync(m routeRegistrar) {
	m.HandleFunc("GET /api/v1/admin/legacy-sync/batches", a.adminOnly(a.legacySyncBatches))
	m.HandleFunc("POST /api/v1/admin/legacy-sync/batches", a.adminOnly(a.legacySyncStart))
	m.HandleFunc("GET /api/v1/admin/legacy-sync/batches/{id}", a.adminOnly(a.legacySyncDetails))
	m.HandleFunc("POST /api/v1/admin/legacy-sync/batches/{id}/rollback", a.adminOnly(a.legacySyncRollback))
	m.HandleFunc("PUT /api/v1/admin/legacy-sync/schedule", a.adminOnly(a.legacySyncSchedule))
}

func (a *App) legacyStore() postgres.LegacySyncStore {
	return postgres.LegacySyncStore{DB: a.DB}
}

func (a *App) legacySyncBatches(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	limit, offset, err := decodePage(r.URL.Query())
	if err != nil {
		fail(w, 422, "INVALID_CURSOR", err.Error())
		return
	}
	page, err := a.legacyStore().Batches(r.Context(), limit, offset)
	memberResponse(w, map[string]any{"data": map[string]any{
		"batches": page.Batches, "controls": page.Controls, "latest_batch": page.Latest, "can_start": a.LegacySync != nil,
	},
		"total": page.Total, "next_cursor": nextPage(offset, limit, page.Total)}, err)
}

func (a *App) legacySyncStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var input struct {
		RequestID string `json:"request_id"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if a.LegacySync == nil {
		fail(w, http.StatusServiceUnavailable, "LEGACY_SYNC_UNAVAILABLE", "当前环境暂不能启动同步")
		return
	}
	batch, err := a.LegacySync.Start(user.ID, input.RequestID)
	if errors.Is(err, syncapp.ErrBusy) {
		fail(w, http.StatusConflict, "LEGACY_SYNC_BUSY", "已有同步任务在运行")
		return
	}
	if err != nil {
		memberResponse(w, nil, err)
		return
	}
	status := http.StatusOK
	if batch.Status == "running" {
		status = http.StatusAccepted
	}
	writeJSON(w, status, map[string]any{"data": batch})
}

type legacySyncProduct struct {
	Title      string                `json:"title"`
	Category   string                `json:"category"`
	Status     string                `json:"status"`
	Price      *string               `json:"price"`
	Unit       string                `json:"unit"`
	Quotes     []catalog.RentalQuote `json:"quotes"`
	ImageCount int                   `json:"image_count"`
}

func legacySyncSummary(p *syncapp.Product) *legacySyncProduct {
	if p == nil {
		return nil
	}
	return &legacySyncProduct{Title: p.Title, Category: p.SourceCategory,
		Status: p.Status, Price: p.Amount, Unit: p.Unit, Quotes: p.Quotes, ImageCount: len(p.Images)}
}

func (a *App) legacySyncDetails(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	limit, offset, err := decodePage(r.URL.Query())
	if err != nil {
		fail(w, 422, "INVALID_CURSOR", err.Error())
		return
	}
	filter := r.URL.Query().Get("filter")
	if !slices.Contains([]string{"", "create", "update", "restore", "offline", "issues"}, filter) {
		fail(w, 422, "INVALID_FILTER", "请选择有效的商品变更类型")
		return
	}
	batch, items, total, err := a.legacyStore().Details(r.Context(), r.PathValue("id"), filter, limit, offset)
	if err != nil {
		memberResponse(w, nil, err)
		return
	}
	details := make([]map[string]any, 0, len(items))
	for _, item := range items {
		details = append(details, map[string]any{
			"legacy_id": item.LegacyID, "code": item.Code, "action": item.Action, "source_price": item.SourcePrice,
			"source_updated_at": item.SourceUpdatedAt, "notices": item.Notices, "blocking": item.Blocking,
			"before": legacySyncSummary(item.Before), "after": legacySyncSummary(item.After),
			"images_changed":      item.After != nil && (item.Before == nil || !slices.Equal(item.Before.Images, item.After.Images)),
			"attributes_changed":  item.Before != nil && item.After != nil && !reflect.DeepEqual(item.Before.Attributes, item.After.Attributes),
			"description_changed": item.Before != nil && item.After != nil && item.Before.Description != item.After.Description,
		})
	}
	memberResponse(w, map[string]any{"data": map[string]any{"batch": batch, "items": details},
		"total": total, "next_cursor": nextPage(offset, limit, total)}, nil)
}

func (a *App) legacySyncRollback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var input struct {
		Confirm bool `json:"confirm"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if !input.Confirm {
		fail(w, 422, "CONFIRM_REQUIRED", "请先核对该批次的变更")
		return
	}
	err := a.legacyStore().RollbackBatch(r.Context(), r.PathValue("id"), user.ID)
	memberResponse(w, map[string]any{"data": map[string]any{"ok": true}}, err)
}

func (a *App) legacySyncSchedule(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var input struct {
		Enabled         *bool `json:"enabled"`
		ExpectedVersion int64 `json:"expected_version"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if input.Enabled == nil || input.ExpectedVersion < 1 {
		fail(w, 422, "INVALID_REQUEST", "请选择定时同步状态并提供所见设置版本")
		return
	}
	controls, err := a.legacyStore().SetSchedule(r.Context(), user.ID, *input.Enabled, input.ExpectedVersion)
	memberResponse(w, map[string]any{"data": controls}, err)
}
