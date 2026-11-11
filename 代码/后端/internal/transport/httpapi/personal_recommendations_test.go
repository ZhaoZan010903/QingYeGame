package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func personalFixture(t *testing.T) (*App, memberHTTP, *http.Cookie, *http.Cookie) {
	a := integrationMemberApp(t)
	a.Config.SessionSecret = strings.Repeat("personal-test-secret", 3)
	recommendationCategory(t, a, "sale", true)
	recommendationCategory(t, a, "rental", true)
	for i, item := range []struct{ id, category, status string }{{"sale-old", "sale", "published"}, {"sale-new", "sale", "published"}, {"rent-old", "rental", "published"}, {"rent-new", "rental", "published"}, {"private", "rental", "draft"}} {
		recommendationProduct(t, a, item.id, item.category, "platform", item.status, time.Now().Add(time.Duration(i-10)*time.Hour), true)
	}
	if _, e := a.DB.Exec(context.Background(), `UPDATE products SET trade_type='rent',price_unit='day',price_amount=100,attributes='{"server":"华东一区"}' WHERE category_id='rental';UPDATE products SET price_amount=1000,attributes='{"server":"华南一区"}' WHERE category_id='sale'`); e != nil {
		t.Fatal(e)
	}
	return a, memberHTTP{t: t, handler: a.Routes(), ip: "personal-" + newID()}, recommendationUser(t, a, "personal-a", "member"), recommendationUser(t, a, "personal-b", "member")
}
func personalList(t *testing.T, h memberHTTP, cookie *http.Cookie, query string) []string {
	t.Helper()
	w := h.call("GET", "/api/v1/products?sort="+query, nil, cookie, 200)
	var data struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &data); e != nil {
		t.Fatal(e)
	}
	ids := []string{}
	for _, p := range data.Data {
		ids = append(ids, p.ID)
	}
	return ids
}
func TestPersonalRecommendationIsolationResetAndOptOut(t *testing.T) {
	a, h, one, two := personalFixture(t)
	h.call("POST", "/api/v1/products/sale-old/view", nil, one, 200)
	h.call("POST", "/api/v1/products/rent-old/view", nil, two, 200)
	if got := personalList(t, h, one, "recommended"); got[0] != "sale-new" {
		t.Fatal("member A preference ignored", got)
	}
	if got := personalList(t, h, two, "recommended"); got[0] != "rent-new" {
		t.Fatal("member B preference ignored", got)
	}
	for _, id := range personalList(t, h, two, "recommended") {
		if id == "private" {
			t.Fatal("private candidate leaked")
		}
	}
	if got := personalList(t, h, one, "newest"); got[0] != "rent-new" {
		t.Fatal("newest influenced by profile", got)
	}
	profile := memberData(t, h.call("GET", "/api/v1/recommendations/profile", nil, one, 200))
	if profile["sample_count"] != float64(1) || strings.Contains(string(mustJSON(profile)), "personal-a") {
		t.Fatal("profile summary incorrect", profile)
	}
	h.call("PATCH", "/api/v1/recommendations/profile", map[string]bool{"enabled": false}, one, 200)
	if got := personalList(t, h, one, "recommended"); got[0] != "rent-new" {
		t.Fatal("optout failed", got)
	}
	h.call("PATCH", "/api/v1/recommendations/profile", map[string]bool{"enabled": true, "reset": true}, one, 200)
	if got := memberData(t, h.call("GET", "/api/v1/recommendations/profile", nil, one, 200))["sample_count"]; got != float64(0) {
		t.Fatal("reset retained old interests", got)
	}
	// Same-day viewing after reset rebuilds interests without adding another hot view.
	if memberData(t, h.call("POST", "/api/v1/products/sale-old/view", nil, one, 200))["recorded"] != false {
		t.Fatal("reset inflated aggregate views")
	}
	if got := personalList(t, h, one, "recommended"); got[0] != "sale-new" {
		t.Fatal("same-day post-reset interest lost", got)
	}
	var count int
	_ = a.DB.QueryRow(context.Background(), "SELECT count(*) FROM product_views_daily WHERE product_id='sale-old'").Scan(&count)
	if count != 1 {
		t.Fatal("repeat views inflated popularity", count)
	}
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func TestPersonalRecommendationGuestForgeryAndVisibility(t *testing.T) {
	a, h, _, _ := personalFixture(t)
	w := h.call("POST", "/api/v1/products/sale-old/view", nil, nil, 200)
	var guest *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "qy_visitor" {
			guest = c
		}
	}
	if guest == nil {
		t.Fatal("guest cookie absent")
	}
	if got := personalList(t, h, guest, "recommended"); got[0] != "sale-new" {
		t.Fatal(got)
	}
	forged := *guest
	forged.Value += "x"
	if got := personalList(t, h, &forged, "recommended"); got[0] != "rent-new" {
		t.Fatal("forged identity used another profile", got)
	}
	_, e := a.DB.Exec(context.Background(), "UPDATE products SET status='offline' WHERE id='sale-old'")
	if e != nil {
		t.Fatal(e)
	}
	if got := personalList(t, h, guest, "recommended"); got[0] != "rent-new" {
		t.Fatal("offline source still shapes profile", got)
	}
	r := httptest.NewRequest("PATCH", "/api/v1/recommendations/profile", strings.NewReader(`{"enabled":false}`))
	r.AddCookie(guest)
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	a.Routes().ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Fatal("cross-site profile mutation accepted")
	}
}
func TestPersonalRecommendationWindowAndHomepageSections(t *testing.T) {
	a, h, one, _ := personalFixture(t)
	ctx := context.Background()
	h.call("POST", "/api/v1/products/sale-old/view", nil, one, 200)
	_, e := a.DB.Exec(ctx, "UPDATE product_views_daily SET created_at=now()-interval '31 days',last_seen_at=now()-interval '31 days'")
	if e != nil {
		t.Fatal(e)
	}
	if got := personalList(t, h, one, "recommended"); got[0] != "rent-new" {
		t.Fatal("expired interests retained", got)
	}
	h.call("POST", "/api/v1/products/sale-old/view", nil, one, 200)
	w := h.call("GET", "/api/v1/home", nil, one, 200)
	var data struct {
		Data struct {
			Recommended, Newest, Hot []map[string]any
			Personal                 string `json:"personalization_source"`
		} `json:"data"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &data); e != nil {
		t.Fatal(e)
	}
	if data.Data.Personal != "browsing" || data.Data.Recommended[0]["id"] != "sale-new" || data.Data.Newest[0]["id"] != "rent-new" || len(data.Data.Hot) != 4 {
		t.Fatal("three independent sections incorrect", w.Body.String())
	}
	// Price affinity must never compare daily rentals with account sale totals.
	if _, e = a.DB.Exec(ctx, "UPDATE products SET category_id='rental',trade_type='rent',price_unit='account',price_amount=100,attributes='{}' WHERE id='sale-new'"); e != nil {
		t.Fatal(e)
	}
	h.call("PATCH", "/api/v1/recommendations/profile", map[string]bool{"reset": true}, one, 200)
	h.call("POST", "/api/v1/products/rent-old/view", nil, one, 200)
	if got := personalList(t, h, one, "recommended"); got[0] != "rent-new" {
		t.Fatal("cross-unit price influenced recommendation", got)
	}
}

func TestPersonalRecommendationConcurrentIsolation(t *testing.T) {
	_, h, one, two := personalFixture(t)
	h.call("POST", "/api/v1/products/sale-old/view", nil, one, 200)
	h.call("POST", "/api/v1/products/rent-old/view", nil, two, 200)
	started := time.Now()
	var wg sync.WaitGroup
	for worker := 0; worker < 10; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			cookie, want := one, "sale-new"
			if worker%2 == 1 {
				cookie, want = two, "rent-new"
			}
			for i := 0; i < 10; i++ {
				r := httptest.NewRequest("GET", "/api/v1/products?sort=recommended&limit=2", nil)
				r.AddCookie(cookie)
				w := httptest.NewRecorder()
				h.handler.ServeHTTP(w, r)
				var data struct {
					Data []struct {
						ID string `json:"id"`
					} `json:"data"`
				}
				if e := json.Unmarshal(w.Body.Bytes(), &data); e != nil || w.Code != 200 || len(data.Data) == 0 || data.Data[0].ID != want {
					t.Errorf("concurrent profile result mismatch for worker %d status %d", worker, w.Code)
				}
			}
		}(worker)
	}
	wg.Wait()
	t.Logf("100 personalized reads, 10 workers, elapsed=%s", time.Since(started))
}
