package httpapi

import (
	"net/http"
	securityapp "qingye/backend/internal/application/security"
	securitydomain "qingye/backend/internal/domain/security"
	"qingye/backend/internal/infrastructure/postgres"
)

func (a *App) registerMediaAdministration(m routeRegistrar) {
	m.HandleFunc("GET /api/v1/admin/products/{id}/media-sync", a.adminOnly(a.mediaSyncStatus))
	m.HandleFunc("POST /api/v1/admin/products/{id}/media-sync/retry", a.adminOnly(a.retryMediaSync))
	m.HandleFunc("GET /api/v1/admin/users/{id}/upload-restriction", a.adminOnly(a.uploadRestriction))
	m.HandleFunc("PUT /api/v1/admin/users/{id}/upload-restriction", a.adminOnly(a.setUploadRestriction))
}
func (a *App) mediaSyncStatus(w http.ResponseWriter, r *http.Request) {
	out, err := (&postgres.MediaLifecycleStore{DB: a.DB, NewID: newID}).Status(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": out})
}
func (a *App) retryMediaSync(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var in struct {
		ExpectedVersion int `json:"expected_version"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := (&postgres.MediaLifecycleStore{DB: a.DB, NewID: newID}).Retry(r.Context(), u.ID, r.PathValue("id"), in.ExpectedVersion); err != nil {
		writeAppError(w, err)
		return
	}
	a.mediaSyncStatus(w, r)
}
func (a *App) uploadRestriction(w http.ResponseWriter, r *http.Request) {
	out, err := (&postgres.UploadSecurityStore{DB: a.DB, NewID: newID}).Restriction(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": out})
}
func (a *App) setUploadRestriction(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var in securitydomain.RestrictInput
	if !readJSON(w, r, &in) {
		return
	}
	service := &securityapp.UploadService{Repository: &postgres.UploadSecurityStore{DB: a.DB, NewID: newID}}
	out, err := service.Set(r.Context(), u.ID, u.Role, r.PathValue("id"), in)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": out})
}
