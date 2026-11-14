package httpapi

import (
	"net/http"
	application "qingye/backend/internal/application/adminaccounts"
	domain "qingye/backend/internal/domain/adminaccounts"
	"qingye/backend/internal/infrastructure/postgres"
)

func (a *App) adminAccountService() *application.Service {
	return application.New(postgres.AdminAccountStore{DB: a.DB, NewID: newID, CredentialKey: a.Config.ProductCredentialKey}, loginVerifier)
}
func (a *App) registerAdminAccounts(m routeRegistrar) {
	m.HandleFunc("GET /api/v1/admin/administrators", a.adminOnly(a.adminAccounts))
	m.HandleFunc("POST /api/v1/admin/administrators", a.adminOnly(a.createAdminAccount))
	m.HandleFunc("PATCH /api/v1/admin/administrators/{id}", a.adminOnly(a.updateAdminAccount))
	m.HandleFunc("POST /api/v1/admin/administrators/{id}/password", a.adminOnly(a.resetAdminAccountPassword))
}
func (a *App) adminAccounts(w http.ResponseWriter, r *http.Request) {
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
	data, err := a.adminAccountService().List(r.Context(), managementActor(u), r.URL.Query().Get("q"), limit, offset)
	memberResponse(w, data, err)
}
func (a *App) createAdminAccount(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var in domain.CreateInput
	if !readJSON(w, r, &in) {
		return
	}
	data, err := a.adminAccountService().Create(r.Context(), managementActor(u), in)
	memberResponse(w, data, err)
}
func (a *App) updateAdminAccount(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var in domain.UpdateInput
	if !readJSON(w, r, &in) {
		return
	}
	data, err := a.adminAccountService().Update(r.Context(), managementActor(u), r.PathValue("id"), in)
	memberResponse(w, data, err)
}
func (a *App) resetAdminAccountPassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var in domain.PasswordInput
	if !readJSON(w, r, &in) {
		return
	}
	data, err := a.adminAccountService().ResetPassword(r.Context(), managementActor(u), r.PathValue("id"), in)
	if err == nil && u.ID == r.PathValue("id") {
		http.SetCookie(w, &http.Cookie{Name: a.cookieName(), Value: "", Path: "/", HttpOnly: true, Secure: a.Config.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	}
	memberResponse(w, data, err)
}
