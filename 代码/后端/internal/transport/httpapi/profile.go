package httpapi

import (
	"net/http"
	application "qingye/backend/internal/application/profile"
	domain "qingye/backend/internal/domain/profile"
	"qingye/backend/internal/infrastructure/postgres"
	"strconv"
)

func (a *App) profileService() *application.Service {
	return application.New(postgres.ProfileStore{DB: a.DB, NewID: newID})
}
func (a *App) registerProfile(m routeRegistrar) {
	m.HandleFunc("GET /api/v1/avatar-library", a.avatarLibrary)
	if a.surface() == "public" {
		m.HandleFunc("GET /api/v1/me/profile", a.memberProfile)
	}
	if a.surface() == "admin" {
		m.HandleFunc("GET /api/v1/admin/nickname-requests", a.adminOnly(a.nicknameRequests))
		m.HandleFunc("POST /api/v1/admin/nickname-requests/{id}/review", a.adminOnly(a.reviewNickname))
		m.HandleFunc("GET /api/v1/admin/avatar-library", a.adminOnly(a.avatarLibrary))
		m.HandleFunc("POST /api/v1/admin/avatar-library", a.adminOnly(a.saveAvatar))
		m.HandleFunc("PATCH /api/v1/admin/avatar-library/{id}", a.adminOnly(a.saveAvatar))
	}
}
func (a *App) memberProfile(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	data, err := a.profileService().Profile(r.Context(), *u)
	memberResponse(w, data, err)
}
func (a *App) nicknameRequests(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	page, limit := 1, 20
	q := r.URL.Query()
	var err error
	if q.Get("page") != "" {
		page, err = strconv.Atoi(q.Get("page"))
		if err != nil {
			page = 0
		}
	}
	if q.Get("limit") != "" {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil {
			limit = 0
		}
	}
	data, err := a.profileService().NicknameRequests(r.Context(), *u, q.Get("status"), page, limit)
	memberResponse(w, data, err)
}
func (a *App) reviewNickname(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var in domain.ReviewInput
	if !readJSON(w, r, &in) {
		return
	}
	err := a.profileService().ReviewNickname(r.Context(), *u, r.PathValue("id"), in)
	memberResponse(w, map[string]any{"data": map[string]bool{"ok": true}}, err)
}
func (a *App) avatarLibrary(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	data, err := a.profileService().Avatars(r.Context(), *u, r.URL.Path == "/api/v1/admin/avatar-library")
	memberResponse(w, data, err)
}
func (a *App) saveAvatar(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var in domain.AvatarInput
	if !readJSON(w, r, &in) {
		return
	}
	data, err := a.profileService().SaveAvatar(r.Context(), *u, r.PathValue("id"), in)
	memberResponse(w, data, err)
}
