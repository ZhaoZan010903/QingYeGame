package httpapi

import (
	"net/http"
	"qingye/backend/internal/domain/management"
	"strconv"
	"strings"
)

func itoa(v int) string { return strconv.Itoa(v) }
func (a *App) adminOnly(h http.HandlerFunc) http.HandlerFunc {
	return a.memberMutation(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.requireAdmin(w, r); ok {
			h(w, r)
		}
	})
}
func (a *App) registerAdmin(m routeRegistrar) {
	a.registerAdminAccounts(m)
	m.HandleFunc("GET /api/v1/admin/dashboard", a.adminOnly(a.adminDashboard))
	m.HandleFunc("GET /api/v1/admin/dictionaries", a.adminOnly(a.adminDictionaries))
	m.HandleFunc("PUT /api/v1/admin/dictionaries/{key}", a.adminOnly(a.adminDictionaries))
	m.HandleFunc("GET /api/v1/admin/products/{id}/private-credentials", a.adminOnly(a.adminPrivateCredentials))
	m.HandleFunc("GET /api/v1/admin/products", a.adminOnly(a.adminProducts))
	m.HandleFunc("GET /api/v1/admin/products/trash/preview", a.adminOnly(a.adminTrashPreview))
	m.HandleFunc("POST /api/v1/admin/products/trash/purge", a.adminOnly(a.adminPurgeTrash))
	m.HandleFunc("POST /api/v1/admin/products/{id}/purge", a.adminOnly(a.adminPurgeProduct))
	m.HandleFunc("GET /api/v1/admin/products/{id}", a.adminOnly(a.adminProduct))
	m.HandleFunc("POST /api/v1/admin/products", a.adminOnly(a.adminSaveProduct))
	m.HandleFunc("PATCH /api/v1/admin/products/{id}", a.adminOnly(a.adminSaveProduct))
	m.HandleFunc("DELETE /api/v1/admin/products/{id}", a.adminOnly(a.adminTrashProduct))
	m.HandleFunc("POST /api/v1/admin/products/{id}/restore", a.adminOnly(a.adminTrashProduct))
	for _, action := range []string{"review", "status", "publish"} {
		m.HandleFunc("POST /api/v1/admin/products/{id}/"+action, a.adminOnly(a.adminTransition))
	}
	m.HandleFunc("PUT /api/v1/admin/products/{id}/featured", a.adminOnly(a.adminFeatured))
	m.HandleFunc("PATCH /api/v1/admin/products/{id}/recommendation", a.adminOnly(a.adminProductRecommendation))
	m.HandleFunc("GET /api/v1/admin/categories", a.adminOnly(a.adminCategories))
	m.HandleFunc("POST /api/v1/admin/categories", a.adminOnly(a.saveCategory))
	m.HandleFunc("PUT /api/v1/admin/categories/{id}", a.adminOnly(a.saveCategory))
	m.HandleFunc("GET /api/v1/admin/contacts", a.adminOnly(a.adminContacts))
	m.HandleFunc("POST /api/v1/admin/contacts", a.adminOnly(a.saveContact))
	m.HandleFunc("PUT /api/v1/admin/contacts/{id}", a.adminOnly(a.saveContact))
	m.HandleFunc("GET /api/v1/admin/pages", a.adminOnly(a.adminPages))
	m.HandleFunc("PUT /api/v1/admin/pages/{slug}", a.adminOnly(a.savePage))
	m.HandleFunc("GET /api/v1/admin/settings", a.adminOnly(a.adminSettings))
	m.HandleFunc("PUT /api/v1/admin/settings/{key}", a.adminOnly(a.saveSetting))
	m.HandleFunc("GET /api/v1/admin/users", a.adminOnly(a.adminUsers))
	m.HandleFunc("PATCH /api/v1/admin/users/{id}", a.adminOnly(a.updateUser))
	m.HandleFunc("GET /api/v1/admin/recommendations", a.adminOnly(a.adminRecommendations))
	m.HandleFunc("POST /api/v1/admin/recommendations/refresh", a.adminOnly(a.refreshRecommendations))
}
func (a *App) adminProducts(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, offset, err := management.DecodeProductPage(q.Get("limit"), q.Get("page"), q.Get("cursor"))
	if err != nil {
		fail(w, 422, "INVALID_CURSOR", err.Error())
		return
	}
	if q.Get("trash") != "" && q.Get("trash") != "1" {
		fail(w, 422, "INVALID_QUERY", "回收站筛选无效")
		return
	}
	data, err := a.managementService().Products(r.Context(), managementActor(u), management.ProductQuery{Status: q.Get("status"), Source: q.Get("source"), Query: q.Get("q"), TradeType: q.Get("trade_type"), RentalCategory: q.Get("rental_category"), Sort: q.Get("sort"), Trash: q.Get("trash") == "1", Limit: limit, Offset: offset})
	memberResponse(w, data, err)
}

func (a *App) adminTrashProduct(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var in management.TransitionInput
	if !readJSON(w, r, &in) {
		return
	}
	data, err := a.managementService().TrashProduct(r.Context(), managementActor(u), r.PathValue("id"), r.Method == http.MethodPost, in)
	memberResponse(w, data, err)
}
func (a *App) adminProduct(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	data, err := a.managementService().Product(r.Context(), managementActor(u), r.PathValue("id"))
	memberResponse(w, data, err)
}
func (a *App) adminSaveProduct(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var in ProductInput
	if !readJSON(w, r, &in) {
		return
	}
	data, err := a.managementService().SaveProduct(r.Context(), managementActor(u), r.PathValue("id"), in)
	memberResponse(w, data, err)
}
func (a *App) adminTransition(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var in management.TransitionInput
	if !readJSON(w, r, &in) {
		return
	}
	parts := strings.Split(r.URL.Path, "/")
	operation := parts[len(parts)-1]
	data, err := a.managementService().TransitionProduct(r.Context(), managementActor(u), r.PathValue("id"), operation, in)
	memberResponse(w, data, err)
}
func (a *App) adminFeatured(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var in struct {
		Rank *int `json:"manual_rank"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	data, err := a.managementService().FeatureProduct(r.Context(), managementActor(u), r.PathValue("id"), in.Rank)
	memberResponse(w, data, err)
}

func (a *App) adminPrivateCredentials(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	data, err := a.managementService().PrivateCredentials(r.Context(), managementActor(u), r.PathValue("id"))
	memberResponse(w, data, err)
}
