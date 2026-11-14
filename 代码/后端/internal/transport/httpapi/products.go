package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	catalogapp "qingye/backend/internal/application/catalog"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/infrastructure/cache"
	"qingye/backend/internal/infrastructure/postgres"
)

type ProductInput = catalog.ProductInput

var moneyPattern = catalog.MoneyPattern

func (a *App) catalogStore() *postgres.CatalogStore {
	return &postgres.CatalogStore{DB: a.DB, HomeCache: cache.HomeDocument{Client: a.Redis}}
}

func decodePage(q url.Values) (int, int, error) {
	return catalog.DecodePage(q.Get("limit"), q.Get("cursor"))
}
func nextPage(offset, limit, total int) any { return catalog.NextPage(offset, limit, total) }

func (a *App) catalogService() *catalogapp.Service {
	return catalogapp.New(a.catalogStore(), a.recommendationService())
}

func (a *App) publicProducts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset, e := decodePage(q)
	if e != nil {
		fail(w, 422, "INVALID_CURSOR", e.Error())
		return
	}
	input := catalog.SearchInput{Query: q.Get("q"), Category: q.Get("category"), TradeType: q.Get("trade_type"), RentalCategory: q.Get("rental_category"), PriceUnit: q.Get("price_unit"), PeriodBasis: q.Get("period_basis"), Sort: q.Get("sort"), Limit: limit, Offset: offset}
	if q.Has("min_price") {
		v := q.Get("min_price")
		input.MinPrice = &v
	}
	if q.Has("max_price") {
		v := q.Get("max_price")
		input.MaxPrice = &v
	}
	if raw := q.Get("filters"); raw != "" {
		if json.Unmarshal([]byte(raw), &input.Filters) != nil {
			fail(w, 422, "INVALID_FILTERS", "筛选格式无效")
			return
		}
	}
	identity := ""
	if input.Sort == "recommended" {
		identity, e = a.recommendationIdentity(r)
		if e != nil {
			a.dbError(w, e)
			return
		}
	}
	page, e := a.catalogService().Search(r.Context(), input, identity)
	if e != nil {
		writeAppError(w, e)
		return
	}
	writeJSON(w, 200, page)
}
func (a *App) publicProduct(w http.ResponseWriter, r *http.Request) {
	p, e := a.catalogService().Product(r.Context(), r.PathValue("id"))
	if e != nil {
		writeAppError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": p})
}
func (a *App) publicImages(w http.ResponseWriter, r *http.Request) {
	limit, offset, e := decodePage(r.URL.Query())
	if e != nil {
		fail(w, 422, "INVALID_CURSOR", e.Error())
		return
	}
	p, e := a.catalogService().Images(r.Context(), r.PathValue("id"), r.URL.Query().Get("expected_version"), limit, offset)
	if e != nil {
		writeAppError(w, e)
		return
	}
	writeJSON(w, 200, p)
}
