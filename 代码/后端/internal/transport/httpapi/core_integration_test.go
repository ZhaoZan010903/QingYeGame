package httpapi

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type coreHTTP struct{ public, admin memberHTTP }

func (h coreHTTP) call(method, path string, body any, cookie *http.Cookie, want int) *httptest.ResponseRecorder {
	if strings.HasPrefix(path, "/api/v1/admin/") {
		return h.admin.call(method, path, body, cookie, want)
	}
	return h.public.call(method, path, body, cookie, want)
}

func TestCoreCatalogImportAndAdministration(t *testing.T) {
	a := integrationMemberApp(t)
	a.Config.SessionSecret = strings.Repeat("test-session-key-", 3)
	ctx := context.Background()
	adminApp := App{DB: a.DB, Redis: a.Redis, Config: a.Config}
	adminApp.Config = a.Config
	adminApp.Config.Surface = "admin"
	adminApp.Config.AllowedOrigins = []string{"http://qingye-admin.test"}
	h := coreHTTP{public: memberHTTP{t: t, handler: a.Routes(), ip: "192.0.2.77"}, admin: memberHTTP{t: t, handler: adminApp.Routes(), ip: "192.0.2.77", origin: "http://qingye-admin.test"}}
	raw := `[{"_id":"legacy-check-1","title":"保留原报价的账号","product_id":"QY-CHECK-1","price":350,"categorys_name":"周月租号","status":"出售中","content":"//example.test/a.jpg?x-oss-process=image/resize,w_800,h_600,https://example.test/b.jpg","游戏区服":"一区","角色性别":"女","XVIP等级":"彩钻","create_time":"2026-11-10T14:06:28Z","update_time":"2026-11-10T15:00:00Z"}]`
	count, err := a.ImportLegacy(ctx, strings.NewReader(raw))
	if err != nil || count != 1 {
		t.Fatalf("import: %d %v", count, err)
	}
	id := deterministicID("legacy-product:legacy-check-1")
	count, err = a.ImportLegacy(ctx, strings.NewReader(raw))
	if err != nil || count != 0 {
		t.Fatalf("idempotent import: %d %v", count, err)
	}
	var amount *string
	var unit, legacy string
	var images int
	if err = a.DB.QueryRow(ctx, "SELECT price_amount::text,price_unit,legacy_price,(SELECT count(*) FROM product_images WHERE product_id=$1) FROM products WHERE id=$1", id).Scan(&amount, &unit, &legacy, &images); err != nil || amount == nil || *amount != "350.00" || unit != "month" || legacy != "350" || images != 2 {
		t.Fatalf("legacy values changed: %v %s %s images=%d err=%v", amount, unit, legacy, images, err)
	}
	w := h.call("GET", "/api/v1/products/"+id, nil, nil, 200)
	for _, private := range []string{"example.test", "legacy_payload", "owner_id", "review_note", "storage_key"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatalf("private field leaked: %s", private)
		}
	}
	h.call("GET", "/api/v1/products?sort=price_asc", nil, nil, 422)
	h.call("GET", "/api/v1/products?sort=price_asc&price_unit=day", nil, nil, 200)
	if err = os.MkdirAll(filepath.Join(a.Config.MediaDir, "legacy"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, source := range legacyImageURLs("//example.test/a.jpg?x-oss-process=image/resize,w_800,h_600,https://example.test/b.jpg") {
		_, key, _ := legacyMediaLocation(source)
		f, e := os.Create(filepath.Join(a.Config.MediaDir, filepath.FromSlash(key)))
		if e != nil {
			t.Fatal(e)
		}
		im := image.NewRGBA(image.Rect(0, 0, 8, 8))
		im.Set(0, 0, color.RGBA{R: 200, A: 255})
		e = png.Encode(f, im)
		f.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
	result, err := a.ReconcileMedia(ctx, 0)
	if err != nil || result.Ready != 2 {
		t.Fatalf("reconcile: %+v %v", result, err)
	}
	// An admin session is server-issued; no public role-selection API exists.
	adminID := newID()
	token := randomToken()
	_, err = a.DB.Exec(ctx, "INSERT INTO users(id,phone,nickname,role,password_hash) VALUES($1,'19900007777','审核测试','admin','unused-test-hash')", adminID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.DB.Exec(ctx, "INSERT INTO sessions(token_hash,user_id,expires_at,audience) VALUES($1,$2,now()+interval '1 hour','admin')", tokenHash(token), adminID)
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: adminSessionCookie, Value: token}
	h.call("GET", "/api/v1/admin/products", nil, nil, 401)
	t.Run("admin create replay", func(t *testing.T) {
		input := map[string]any{"category_id": "qq-dance", "title": "平台新草稿", "trade_type": "sale", "price_unit": "account", "price_amount": "100.00", "attributes": map[string]any{}, "description": "测试介绍", "request_id": newID()}
		first := memberData(t, h.call("POST", "/api/v1/admin/products", input, cookie, 200))
		again := memberData(t, h.call("POST", "/api/v1/admin/products", input, cookie, 200))
		if first["id"] != again["id"] {
			t.Fatal("admin retry created duplicate")
		}
		input["title"] = "不同载荷"
		h.call("POST", "/api/v1/admin/products", input, cookie, 409)
	})
	t.Run("editable filters and arrays", func(t *testing.T) {
		filter := `[{"key":"tags","label":"标签","multiple":true,"options":[{"value":"active","label":"启用"},{"value":"retired","label":"停用","enabled":false}]}]`
		_, err := a.DB.Exec(ctx, "UPDATE categories SET filter_config=$1 WHERE id='legacy-long-rent'", filter)
		if err != nil {
			t.Fatal(err)
		}
		_, err = a.DB.Exec(ctx, `UPDATE products SET attributes=attributes||'{"tags":["active"]}'::jsonb WHERE id=$1`, id)
		if err != nil {
			t.Fatal(err)
		}
		w := h.call("GET", `/api/v1/products?filters=%7B%22tags%22%3A%5B%22active%22%5D%7D`, nil, nil, 200)
		if !strings.Contains(w.Body.String(), id) {
			t.Fatal("array filter missed product")
		}
		h.call("GET", `/api/v1/products?filters=%7B%22tags%22%3A%5B%22retired%22%5D%7D`, nil, nil, 422)
		w = h.call("GET", "/api/v1/categories", nil, nil, 200)
		if strings.Contains(w.Body.String(), "retired") {
			t.Fatal("disabled option exposed")
		}
		category := map[string]any{"name": "周月租号", "price_units": []string{"week", "month", "unknown"}, "filter_config": []any{}, "sort_order": 1, "enabled": true}
		h.call("PUT", "/api/v1/admin/categories/legacy-long-rent", category, cookie, 409)
	})
	t.Run("pages and hidden settings", func(t *testing.T) {
		h.call("PUT", "/api/v1/admin/pages/test-about", map[string]any{"title": "说明", "body": "测试正文", "published": false}, cookie, 200)
		h.call("GET", "/api/v1/pages/test-about", nil, nil, 404)
		h.call("PUT", "/api/v1/admin/pages/test-about", map[string]any{"title": "说明", "body": "测试正文", "published": true}, cookie, 200)
		h.call("GET", "/api/v1/pages/test-about", nil, nil, 200)
		h.call("PUT", "/api/v1/admin/settings/session_secret", map[string]any{"value": "bad"}, cookie, 422)
	})
	h.call("PUT", "/api/v1/admin/products/"+id+"/featured", map[string]any{"manual_rank": 1}, cookie, 200)
	input := map[string]any{"category_id": "legacy-long-rent", "title": "核对后的展示标题", "trade_type": "rent", "price_unit": "unknown", "price_amount": nil, "attributes": map[string]any{"server": "一区", "gender": "female", "level": "彩钻"}, "description": "保留完整旧介绍", "expected_version": 1}
	h.call("PATCH", "/api/v1/admin/products/"+id, input, cookie, 200)
	h.call("GET", "/api/v1/products/"+id, nil, nil, 404)
	h.call("PATCH", "/api/v1/admin/products/"+id, input, cookie, 409)
	h.call("POST", "/api/v1/admin/products/"+id+"/publish", map[string]any{"expected_version": 2}, cookie, 200)
	h.call("GET", "/api/v1/products/"+id, nil, nil, 200)
	// Category disable immediately blocks public detail and media, regardless of stored rank.
	_, err = a.DB.Exec(ctx, "UPDATE categories SET enabled=false WHERE id='legacy-long-rent'")
	if err != nil {
		t.Fatal(err)
	}
	h.call("GET", "/api/v1/products/"+id, nil, nil, 404)
	_, err = a.DB.Exec(ctx, "UPDATE categories SET enabled=true WHERE id='legacy-long-rent'")
	if err != nil {
		t.Fatal(err)
	}
	config := defaultRecommendations()
	_, err = a.DB.Exec(ctx, "UPDATE products SET published_at=now() WHERE id=$1", id)
	if err != nil {
		t.Fatal(err)
	}
	config.Version = "config-test"
	config.NewHours = 720
	config.NewBonus = 37
	h.call("PUT", "/api/v1/admin/settings/recommendation", map[string]any{"value": config}, cookie, 200)
	if err = a.RefreshRecommendations(ctx); err != nil {
		t.Fatal(err)
	}
	var bonus int
	if err = a.DB.QueryRow(ctx, "SELECT new_bonus FROM product_hot_scores WHERE product_id=$1", id).Scan(&bonus); err != nil || bonus != 0 {
		t.Fatalf("legacy creation time must not earn a first-publication bonus: %d %v", bonus, err)
	}
	w = h.call("GET", "/api/v1/admin/recommendations", nil, cookie, 200)
	if !strings.Contains(w.Body.String(), "config-test") {
		t.Fatal("applied rule version absent")
	}
	w = h.call("GET", "/api/v1/home", nil, nil, 200)
	var home struct {
		Data struct{ Featured, Hot, Newest []map[string]any }
	}
	if err = json.Unmarshal(w.Body.Bytes(), &home); err != nil {
		t.Fatal(err)
	}
	for _, list := range [][]map[string]any{home.Data.Featured, home.Data.Hot, home.Data.Newest} {
		seen := map[string]bool{}
		for _, p := range list {
			pid := p["id"].(string)
			if seen[pid] {
				t.Fatal("homepage section duplicated product")
			}
			seen[pid] = true
		}
	}
	h.call("POST", "/api/v1/admin/products/"+id+"/status", map[string]any{"expected_version": 3, "action": "offline", "reason": "测试下架"}, cookie, 200)
	h.call("GET", "/api/v1/products/"+id, nil, nil, 410)
	t.Run("user disable revokes sessions", func(t *testing.T) {
		other := newID()
		otherToken := randomToken()
		_, err := a.DB.Exec(ctx, "INSERT INTO users(id,phone,nickname,password_hash) VALUES($1,'19900007778','待停用用户','unused')", other)
		if err != nil {
			t.Fatal(err)
		}
		_, err = a.DB.Exec(ctx, "INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 hour')", tokenHash(otherToken), other)
		if err != nil {
			t.Fatal(err)
		}
		otherCookie := &http.Cookie{Name: sessionCookie, Value: otherToken}
		h.call("GET", "/api/v1/me", nil, otherCookie, 200)
		h.call("PATCH", "/api/v1/admin/users/"+other, map[string]any{"enabled": false}, cookie, 200)
		h.call("GET", "/api/v1/me", nil, otherCookie, 401)
		h.call("PATCH", "/api/v1/admin/users/"+adminID, map[string]any{"enabled": false}, cookie, 422)
	})
}
