package httpapi

import (
	"net/http"
	"slices"

	syncapp "qingye/backend/internal/application/legacysync"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/infrastructure/postgres"
)

func (a *App) registerLegacySync(m routeRegistrar) {
	m.HandleFunc("GET /api/v1/admin/legacy-sync/batches", a.adminOnly(a.legacySyncBatches))
	m.HandleFunc("GET /api/v1/admin/legacy-sync/batches/{id}", a.adminOnly(a.legacySyncDetails))
	m.HandleFunc("POST /api/v1/admin/legacy-sync/batches/{id}/apply", a.adminOnly(a.legacySyncApply))
	m.HandleFunc("POST /api/v1/admin/legacy-sync/batches/{id}/rollback", a.adminOnly(a.legacySyncRollback))
	m.HandleFunc("PUT /api/v1/admin/legacy-sync/mode", a.adminOnly(a.legacySyncMode))
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
	batches, controls, total, err := a.legacyStore().Batches(r.Context(), limit, offset)
	memberResponse(w, map[string]any{"data": map[string]any{"batches": batches, "controls": controls},
		"total": total, "next_cursor": nextPage(offset, limit, total)}, err)
}

type legacySyncProduct struct {
	Title      string                `json:"title"`
	Category   string                `json:"category"`
	Attributes map[string]any        `json:"attributes"`
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
	return &legacySyncProduct{Title: p.Title, Category: p.SourceCategory, Attributes: p.Attributes,
		Status: p.Status, Price: p.Amount, Unit: p.Unit, Quotes: p.Quotes, ImageCount: len(p.Images)}
}

func (a *App) legacySyncDetails(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	limit, offset, err := decodePage(r.URL.Query())
	if err != nil {
		fail(w, 422, "INVALID_CURSOR", err.Error())
		return
	}
	batch, items, total, err := a.legacyStore().Details(r.Context(), r.PathValue("id"), limit, offset)
	if err != nil {
		memberResponse(w, nil, err)
		return
	}
	details := make([]map[string]any, 0, len(items))
	for _, item := range items {
		details = append(details, map[string]any{
			"legacy_id": item.LegacyID, "code": item.Code, "action": item.Action, "source_price": item.SourcePrice,
			"source_updated_at": item.SourceUpdatedAt, "notices": item.Notices,
			"before": legacySyncSummary(item.Before), "after": legacySyncSummary(item.After),
			"images_changed": item.After != nil && (item.Before == nil || !slices.Equal(item.Before.Images, item.After.Images)),
		})
	}
	memberResponse(w, map[string]any{"data": map[string]any{"batch": batch, "items": details},
		"total": total, "next_cursor": nextPage(offset, limit, total)}, nil)
}

func (a *App) legacySyncApply(w http.ResponseWriter, r *http.Request) {
	a.legacySyncChange(w, r, false)
}

func (a *App) legacySyncRollback(w http.ResponseWriter, r *http.Request) {
	a.legacySyncChange(w, r, true)
}

func (a *App) legacySyncChange(w http.ResponseWriter, r *http.Request, rollback bool) {
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
	store := a.legacyStore()
	var err error
	if rollback {
		err = store.RollbackBatch(r.Context(), r.PathValue("id"), user.ID)
	} else {
		err = store.ApplyBatch(r.Context(), r.PathValue("id"), user.ID)
	}
	memberResponse(w, map[string]any{"data": map[string]any{"ok": true}}, err)
}

func (a *App) legacySyncMode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var input struct {
		Mode            string `json:"mode"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	controls, err := a.legacyStore().SetMode(r.Context(), user.ID, input.Mode, input.ExpectedVersion)
	memberResponse(w, map[string]any{"data": controls}, err)
}
