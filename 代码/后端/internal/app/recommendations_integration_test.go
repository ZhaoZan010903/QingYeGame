package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func recommendationCategory(t *testing.T, a *App, id string, enabled bool) {
	t.Helper()
	_, err := a.DB.Exec(context.Background(), `INSERT INTO categories(id,name,trade_type,price_units,enabled) VALUES($1,'推荐测试','all','["account"]',$2)`, id, enabled)
	if err != nil {
		t.Fatal(err)
	}
}
func recommendationProduct(t *testing.T, a *App, id, category, source, status string, published any, approved bool) {
	t.Helper()
	version := any(nil)
	if approved {
		version = 1
	}
	_, err := a.DB.Exec(context.Background(), `INSERT INTO products(id,product_code,category_id,title,trade_type,price_unit,description,status,source,version,approved_version,published_at) VALUES($1,'DUPLICATE-CODE',$2,$1,'sale','account',$3,$4,$5,1,$6,$7)`, id, category, strings.Repeat("完整介绍", 30), status, source, version, published)
	if err != nil {
		t.Fatal(err)
	}
}
func recommendationUser(t *testing.T, a *App, id, role string) *http.Cookie {
	t.Helper()
	ctx := context.Background()
	_, err := a.DB.Exec(ctx, `INSERT INTO users(id,phone,nickname,password_hash,role) VALUES($1,$1,'统计测试','unused',$2)`, id, role)
	if err != nil {
		t.Fatal(err)
	}
	token := randomToken()
	audience := "public"
	cookieName := sessionCookie
	if role == "admin" {
		audience = "admin"
		cookieName = adminSessionCookie
	}
	_, err = a.DB.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at,audience) VALUES($1,$2,now()+interval '1 hour',$3)`, tokenHash(token), id, audience)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: cookieName, Value: token}
}
func recommendationScores(t *testing.T, a *App) map[string][4]int64 {
	t.Helper()
	rows, err := a.DB.Query(context.Background(), "SELECT product_id,score,views,favorites,new_bonus FROM product_hot_scores ORDER BY product_id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][4]int64{}
	for rows.Next() {
		var id string
		var n [4]int64
		if err = rows.Scan(&id, &n[0], &n[1], &n[2], &n[3]); err != nil {
			t.Fatal(err)
		}
		out[id] = n
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
func recommendationListIDs(t *testing.T, h memberHTTP, path string) []string {
	t.Helper()
	w := h.call("GET", path, nil, nil, 200)
	var data struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, p := range data.Data {
		out = append(out, p.ID)
	}
	return out
}

func TestRecommendationCalendarBoundariesAndRules(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	recommendationCategory(t, a, "rank", true)
	// 2026-01-02 00:00 Beijing: the seven-day window starts 2025-12-27 00:00.
	asOf := time.Date(2026, 1, 1, 16, 0, 0, 0, time.UTC)
	start := asOf.AddDate(0, 0, -6)
	recommendationProduct(t, a, "engaged", "rank", "platform", "published", asOf.Add(-72*time.Hour), true)
	recommendationProduct(t, a, "recent", "rank", "member", "published", asOf.Add(-72*time.Hour+time.Microsecond), true)
	recommendationProduct(t, a, "future", "rank", "platform", "published", asOf.Add(time.Microsecond), true)
	recommendationProduct(t, a, "legacy", "rank", "legacy", "published", asOf.Add(-time.Hour), true)
	view := func(visitor, date string, created time.Time) {
		t.Helper()
		_, err := a.DB.Exec(ctx, `INSERT INTO product_views_daily(product_id,visitor_hash,viewed_date,created_at) VALUES('engaged',$1,$2,$3) ON CONFLICT DO NOTHING`, visitor, date, created)
		if err != nil {
			t.Fatal(err)
		}
	}
	view("old-one", "2025-12-26", start.Add(-time.Hour))
	view("old-two", "2025-12-26", start.Add(-time.Hour))
	view("first-day", "2025-12-27", start)
	view("yesterday", "2026-01-01", asOf.Add(-time.Hour))
	view("today", "2026-01-02", asOf)
	view("today", "2026-01-02", asOf) // unique key makes a retry ineffective.
	view("after-cutoff", "2026-01-02", asOf.Add(time.Microsecond))
	view("future-day", "2026-01-03", asOf.Add(time.Hour))
	for i, fixture := range []struct {
		at     time.Time
		active bool
	}{{start, true}, {start.Add(-time.Microsecond), true}, {asOf, true}, {asOf.Add(time.Microsecond), true}, {asOf.Add(-time.Hour), false}} {
		id := []string{"first", "too-old", "at-cutoff", "future-favorite", "inactive"}[i]
		recommendationUser(t, a, id, "member")
		_, err := a.DB.Exec(ctx, "INSERT INTO favorites(user_id,product_id,created_at,active) VALUES($1,'engaged',$2,$3)", id, fixture.at, fixture.active)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := a.refreshRecommendationsAt(ctx, asOf); err != nil {
		t.Fatal(err)
	}
	scores := recommendationScores(t, a)
	if scores["engaged"] != [4]int64{13, 3, 2, 0} {
		t.Fatalf("calendar/cutoff boundary wrong: %v", scores["engaged"])
	}
	if scores["recent"][3] != 0 || scores["future"][3] != 0 || scores["legacy"][3] != 0 {
		t.Fatalf("publication time must not earn popularity: %v", scores)
	}
	var computed time.Time
	var state map[string]any
	if err := a.DB.QueryRow(ctx, "SELECT computed_at FROM product_hot_scores WHERE product_id='engaged'").Scan(&computed); err != nil || !computed.Equal(asOf) {
		t.Fatalf("batch timestamp %s: %v", computed, err)
	}
	if err := a.DB.QueryRow(ctx, "SELECT value FROM site_settings WHERE key='recommendation_state'").Scan(&state); err != nil {
		t.Fatal(err)
	}
	stateTime, err := time.Parse(time.RFC3339Nano, state["computed_at"].(string))
	if err != nil || !stateTime.Equal(asOf) {
		t.Fatal("metadata cutoff differs from score cutoff")
	}
	// One microsecond before midnight has the previous Beijing calendar window.
	if err := a.refreshRecommendationsAt(ctx, asOf.Add(-time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	if got := recommendationScores(t, a)["engaged"]; got != [4]int64{14, 4, 2, 0} {
		t.Fatalf("midnight rollover wrong: %v", got)
	}
	if err := a.refreshRecommendationsAt(ctx, asOf); err != nil {
		t.Fatal(err)
	}
	if got := recommendationScores(t, a)["engaged"]; got != scores["engaged"] {
		t.Fatalf("same inputs produced another score: %v", got)
	}
	cfg := defaultRecommendations()
	cfg.Version = "rules-v2"
	cfg.ViewWeight = 2
	cfg.FavoriteWeight = 7
	cfg.NewBonus = 37
	_, err = a.DB.Exec(ctx, "INSERT INTO site_settings(key,value) VALUES('recommendation',$1)", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := recommendationScores(t, a)["engaged"]; got != scores["engaged"] {
		t.Fatal("configuration changed scores before refresh")
	}
	if err = a.refreshRecommendationsAt(ctx, asOf); err != nil {
		t.Fatal(err)
	}
	scores = recommendationScores(t, a)
	if scores["engaged"][0] != 20 || scores["recent"][3] != 0 {
		t.Fatalf("new rule not applied: %v", scores)
	}
	if err = a.DB.QueryRow(ctx, "SELECT value FROM site_settings WHERE key='recommendation_state'").Scan(&state); err != nil || state["rule_version"] != "rules-v2" {
		t.Fatal("applied version is incorrect")
	}
	// Invalid storage/config does not partially overwrite the previously committed batch.
	_, err = a.DB.Exec(ctx, `UPDATE site_settings SET value='{"version":"invalid","window_days":0}' WHERE key='recommendation'`)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.refreshRecommendationsAt(ctx, asOf); err == nil {
		t.Fatal("invalid rule was used")
	}
	if got := recommendationScores(t, a)["engaged"]; got != scores["engaged"] {
		t.Fatal("failed refresh changed the previous score")
	}
}

func TestRecommendationColdStartVisibilityAndSummary(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	h := memberHTTP{t: t, handler: a.Routes(), ip: "rank-" + newID()}
	recommendationCategory(t, a, "visible", true)
	recommendationCategory(t, a, "disabled", false)
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	fresh := old.Add(time.Hour)
	recommendationProduct(t, a, "a", "visible", "platform", "published", old, true)
	recommendationProduct(t, a, "b", "visible", "platform", "published", old, true)
	recommendationProduct(t, a, "newest", "visible", "platform", "published", fresh, true)
	recommendationProduct(t, a, "unknown-time", "visible", "legacy", "published", nil, true)
	recommendationProduct(t, a, "draft", "visible", "member", "draft", fresh, true)
	recommendationProduct(t, a, "unapproved", "visible", "member", "published", fresh, false)
	recommendationProduct(t, a, "disabled-category", "disabled", "platform", "published", fresh, true)
	expected := "newest,a,b,unknown-time"
	for i := 0; i < 3; i++ {
		got := recommendationListIDs(t, h, "/api/v1/products?sort=hot")
		if strings.Join(got, ",") != expected {
			t.Fatalf("cold-start or tie order: %v", got)
		}
	}
	asOf := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if err := a.refreshRecommendationsAt(ctx, asOf); err != nil {
		t.Fatal(err)
	}
	if got := recommendationListIDs(t, h, "/api/v1/products?sort=hot"); strings.Join(got, ",") != expected {
		t.Fatalf("zero-score snapshot changed ordering: %v", got)
	}
	if _, err := a.DB.Exec(ctx, "UPDATE product_hot_scores SET score=99,computed_at=now() WHERE product_id='a'"); err != nil {
		t.Fatal(err)
	}
	if got := recommendationListIDs(t, h, "/api/v1/products?sort=hot"); strings.Join(got, ",") != "a,newest,b,unknown-time" {
		t.Fatalf("fresh popularity score not applied: %v", got)
	}
	if _, err := a.DB.Exec(ctx, "UPDATE product_hot_scores SET computed_at=now()-interval '61 minutes' WHERE product_id='a'"); err != nil {
		t.Fatal(err)
	}
	if got := recommendationListIDs(t, h, "/api/v1/products?sort=hot"); strings.Join(got, ",") != expected {
		t.Fatalf("stale score did not fall back to newest: %v", got)
	}
	_, err := a.DB.Exec(ctx, `UPDATE products SET manual_rank=1 WHERE id='a'; INSERT INTO site_settings(key,value) VALUES('home_presentation','{"sections":[{"source":"newest","limit":1},{"source":"hot","limit":2}]}')`)
	if err != nil {
		t.Fatal(err)
	}
	w := h.call("GET", "/api/v1/home", nil, nil, 200)
	var home struct {
		Data struct{ Featured, Newest, Hot []map[string]any } `json:"data"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &home); err != nil {
		t.Fatal(err)
	}
	if len(home.Data.Featured) != 1 || home.Data.Featured[0]["description"] == nil || len(home.Data.Newest) != 1 || home.Data.Newest[0]["id"] != "newest" || len(home.Data.Hot) != 2 {
		t.Fatalf("homepage configured sections not honored: %s", w.Body.String())
	}
	for _, list := range [][]map[string]any{home.Data.Featured, home.Data.Newest, home.Data.Hot} {
		seen := map[string]bool{}
		for _, p := range list {
			id := p["id"].(string)
			if seen[id] {
				t.Fatal("one homepage section repeats a product")
			}
			seen[id] = true
		}
	}
	for _, p := range home.Data.Hot {
		if _, ok := p["description"]; ok {
			t.Fatal("hot list includes full introduction")
		}
	}
	list := h.call("GET", "/api/v1/products?sort=newest", nil, nil, 200)
	if strings.Contains(list.Body.String(), `"description"`) {
		t.Fatal("catalog includes full introduction")
	}
	detail := memberData(t, h.call("GET", "/api/v1/products/newest", nil, nil, 200))
	if detail["description"] != strings.Repeat("完整介绍", 30) || detail["title"] != "newest" {
		t.Fatal("summary optimization discarded detail/title")
	}
	// Visibility takes effect before the next batch, then stale derived scores are removed.
	_, err = a.DB.Exec(ctx, `UPDATE products SET status='offline' WHERE id='newest'; UPDATE categories SET enabled=false WHERE id='visible'`)
	if err != nil {
		t.Fatal(err)
	}
	if got := recommendationListIDs(t, h, "/api/v1/products?sort=hot"); len(got) != 0 {
		t.Fatal("disabled products remained publicly ranked")
	}
	if err = a.refreshRecommendationsAt(ctx, asOf); err != nil {
		t.Fatal(err)
	}
	if len(recommendationScores(t, a)) != 0 {
		t.Fatal("empty eligible set left stale score entries")
	}
}

func TestRecommendationViewAndFavoriteDedup(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	a.Config.SessionSecret = strings.Repeat("rank-secret-", 4)
	ip := "rank-view-" + newID()
	h := memberHTTP{t: t, handler: a.Routes(), ip: ip}
	t.Cleanup(func() { _ = a.Redis.Del(ctx, "qingye:limit:view:"+tokenHash(ip)).Err() })
	recommendationCategory(t, a, "dedup", true)
	recommendationProduct(t, a, "viewed", "dedup", "platform", "published", time.Now().Add(-10*24*time.Hour), true)
	owner := recommendationUser(t, a, "owner", "member")
	viewer := recommendationUser(t, a, "viewer", "member")
	admin := recommendationUser(t, a, "rank-admin", "admin")
	_, err := a.DB.Exec(ctx, "UPDATE products SET owner_id='owner' WHERE id='viewed'")
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/products/viewed/view"
	if memberData(t, h.call("POST", path, nil, owner, 200))["recorded"] != false {
		t.Fatal("publisher view was counted")
	}
	first := h.call("POST", path, nil, nil, 200)
	var guest *http.Cookie
	for _, c := range first.Result().Cookies() {
		if c.Name == "qy_visitor" {
			guest = c
		}
	}
	if guest == nil {
		t.Fatal("guest identity not server-issued")
	}
	if memberData(t, h.call("POST", path, nil, guest, 200))["recorded"] != false {
		t.Fatal("same guest/day view was counted twice")
	}
	if memberData(t, h.call("POST", path, nil, viewer, 200))["recorded"] != true {
		t.Fatal("member view not counted")
	}
	secondToken := randomToken()
	_, err = a.DB.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at,audience) VALUES($1,'viewer',now()+interval '1 hour','public')`, tokenHash(secondToken))
	if err != nil {
		t.Fatal(err)
	}
	if memberData(t, h.call("POST", path, nil, &http.Cookie{Name: sessionCookie, Value: secondToken}, 200))["recorded"] != false {
		t.Fatal("same member across sessions was counted twice")
	}
	adminApp := *a
	adminApp.Config = a.Config
	adminApp.Config.Surface = "admin"
	adminApp.Config.AllowedOrigins = []string{"http://qingye-admin.test"}
	adminHTTP := memberHTTP{t: t, handler: adminApp.Routes(), ip: ip, origin: "http://qingye-admin.test"}
	// Admin surface intentionally exposes public reads only; no tracking endpoint is required.
	r := adminHTTP.call("POST", path, nil, admin, 404)
	_ = r
	// Taking a product offline and publishing it again never resets its first date.
	var firstPublished time.Time
	if err = a.DB.QueryRow(ctx, "SELECT published_at FROM products WHERE id='viewed'").Scan(&firstPublished); err != nil {
		t.Fatal(err)
	}
	mediaID := newID()
	_, err = a.DB.Exec(ctx, "INSERT INTO media_files(id,owner_id,status) VALUES($1,'rank-admin','ready')", mediaID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.DB.Exec(ctx, "INSERT INTO product_images(product_id,media_id,sort_order) VALUES('viewed',$1,1)", mediaID)
	if err != nil {
		t.Fatal(err)
	}
	adminHTTP.call("POST", "/api/v1/admin/products/viewed/status", map[string]any{"expected_version": 1, "action": "offline", "reason": "重新上架日期测试"}, admin, 200)
	adminHTTP.call("POST", "/api/v1/admin/products/viewed/publish", map[string]any{"expected_version": 2}, admin, 200)
	var republished time.Time
	if err = a.DB.QueryRow(ctx, "SELECT published_at FROM products WHERE id='viewed'").Scan(&republished); err != nil || !republished.Equal(firstPublished) {
		t.Fatal("republishing reset first publication time")
	}
	var count int
	if err = a.DB.QueryRow(ctx, "SELECT count(*) FROM product_views_daily WHERE product_id='viewed'").Scan(&count); err != nil || count != 2 {
		t.Fatalf("view dedup count %d: %v", count, err)
	}
	favPath := "/api/v1/me/favorites/viewed"
	h.call("PUT", favPath, nil, viewer, 200)
	var original time.Time
	old := time.Now().UTC().Add(-10 * 24 * time.Hour)
	if _, err = a.DB.Exec(ctx, "UPDATE favorites SET created_at=$1 WHERE user_id='viewer' AND product_id='viewed'", old); err != nil {
		t.Fatal(err)
	}
	h.call("DELETE", favPath, nil, viewer, 200)
	h.call("PUT", favPath, nil, viewer, 200)
	h.call("PUT", favPath, nil, viewer, 200)
	if err = a.DB.QueryRow(ctx, "SELECT created_at FROM favorites WHERE user_id='viewer' AND product_id='viewed'").Scan(&original); err != nil || original.Sub(old).Abs() > time.Microsecond {
		t.Fatal("cancel/re-favorite refreshed initial timestamp")
	}
	if err = a.RefreshRecommendations(ctx); err != nil {
		t.Fatal(err)
	}
	if got := recommendationScores(t, a)["viewed"]; got[2] != 0 || got[1] != 2 {
		t.Fatalf("re-favorite or repeated views inflated score: %v", got)
	}
}
