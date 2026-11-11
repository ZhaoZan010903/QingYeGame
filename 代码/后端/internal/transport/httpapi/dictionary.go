package httpapi

import (
	"net/http"
	app "qingye/backend/internal/application/dictionary"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/management"
	"qingye/backend/internal/infrastructure/postgres"
)

func (a *App) dictionaryService() app.Service {
	return app.Service{Reader: &postgres.CatalogStore{DB: a.DB}, Writer: &postgres.ManagementStore{DB: a.DB, NewID: newID}}
}
func (a *App) publicDictionaries(w http.ResponseWriter, r *http.Request) {
	data, err := a.dictionaryService().Public(r.Context())
	memberResponse(w, data, err)
}
func (a *App) adminDictionaries(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	actor := management.Actor{ID: u.ID, Role: u.Role}
	if r.Method == "GET" {
		data, err := a.dictionaryService().Admin(r.Context(), actor)
		memberResponse(w, data, err)
		return
	}
	var in catalog.DictionaryInput
	if !readJSON(w, r, &in) {
		return
	}
	data, err := a.dictionaryService().Save(r.Context(), actor, r.PathValue("key"), in)
	memberResponse(w, data, err)
}
