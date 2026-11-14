package httpapi

import (
	"net/http"
	"qingye/backend/internal/domain/management"
)

func (a *App) adminTrashPreview(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		fail(w, http.StatusBadRequest, "INVALID_PURGE_INPUT", "清空预览仅支持整个回收站，不接受筛选条件")
		return
	}
	data, err := a.managementService().TrashPreview(r.Context(), managementActor(u))
	memberResponse(w, data, err)
}

func (a *App) adminPurgeProduct(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var in management.PurgeProductInput
	if !readJSON(w, r, &in) {
		return
	}
	data, err := a.managementService().PurgeProduct(r.Context(), managementActor(u), r.PathValue("id"), in)
	memberResponse(w, data, err)
}

func (a *App) adminPurgeTrash(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var in management.PurgeTrashInput
	if !readJSON(w, r, &in) {
		return
	}
	data, err := a.managementService().PurgeTrash(r.Context(), managementActor(u), in)
	memberResponse(w, data, err)
}
