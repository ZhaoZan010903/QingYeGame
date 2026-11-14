package httpapi

import (
	"context"
	"net/http"
	managementapp "qingye/backend/internal/application/management"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/management"
	"qingye/backend/internal/infrastructure/postgres"
)

type filterOption = catalog.FilterOption
type filterGroup = catalog.FilterGroup
type homeSection = management.HomeSection
type homePresentation = management.HomePresentation

var codePattern = management.CodePattern

func validateFilters(groups []filterGroup) error { return management.ValidateFilters(groups) }
func validatePresentation(raw []byte) error      { return management.ValidatePresentation(raw) }
func (a *App) managementService() *managementapp.Service {
	return managementapp.New(&postgres.ManagementStore{DB: a.DB, NewID: newID, CredentialKey: a.Config.ProductCredentialKey}, func(raw []byte) error { _, err := parseRecommendations(raw); return err }, func() any { return defaultRecommendations() })
}
func managementActor(u *User) management.Actor { return management.Actor{ID: u.ID, Role: u.Role} }
func (a *App) adminCategories(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	data, err := a.managementService().Categories(r.Context(), managementActor(u))
	memberResponse(w, data, err)
}
func (a *App) adminContacts(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	data, err := a.managementService().Contacts(r.Context(), managementActor(u))
	memberResponse(w, data, err)
}
func (a *App) adminPages(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	data, err := a.managementService().Pages(r.Context(), managementActor(u))
	memberResponse(w, data, err)
}
func (a *App) adminSettings(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	data, err := a.managementService().Settings(r.Context(), managementActor(u))
	memberResponse(w, data, err)
}
func (a *App) saveCategory(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var in management.CategoryInput
	if !readJSON(w, r, &in) {
		return
	}
	data, err := a.managementService().SaveCategory(r.Context(), managementActor(u), r.PathValue("id"), r.Method == "POST", in)
	memberResponse(w, data, err)
}
func (a *App) saveContact(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var in management.ContactInput
	if !readJSON(w, r, &in) {
		return
	}
	data, err := a.managementService().SaveContact(r.Context(), managementActor(u), r.PathValue("id"), in)
	memberResponse(w, data, err)
}
func (a *App) savePage(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var in management.PageInput
	if !readJSON(w, r, &in) {
		return
	}
	data, err := a.managementService().SavePage(r.Context(), managementActor(u), r.PathValue("slug"), in)
	memberResponse(w, data, err)
}
func (a *App) saveSetting(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var in management.SettingInput
	if !readJSON(w, r, &in) {
		return
	}
	data, err := a.managementService().SaveSetting(r.Context(), managementActor(u), r.PathValue("key"), in)
	memberResponse(w, data, err)
}
func (a *App) adminUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	limit, offset, err := decodePage(r.URL.Query())
	if err != nil {
		fail(w, 422, "INVALID_CURSOR", err.Error())
		return
	}
	data, err := a.managementService().Users(r.Context(), managementActor(u), r.URL.Query().Get("q"), r.URL.Query().Get("role"), limit, offset)
	memberResponse(w, data, err)
}
func (a *App) updateUser(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var in management.UserInput
	if !readJSON(w, r, &in) {
		return
	}
	data, err := a.managementService().UpdateUser(r.Context(), managementActor(u), r.PathValue("id"), in)
	memberResponse(w, data, err)
}
func (a *App) adminRecommendations(w http.ResponseWriter, r *http.Request) {
	data, e := a.recommendationService().Report(r.Context())
	if e != nil {
		writeAppError(w, e)
		return
	}
	writeJSON(w, 200, data)
}

func (a *App) refreshRecommendations(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*1000000000)
	defer cancel()
	if e := a.RefreshRecommendations(ctx); e != nil {
		a.dbError(w, e)
		return
	}
	a.adminRecommendations(w, r)
}
