package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

// Every run owns a new schema. It never truncates shared or imported data.
func integrationMemberApp(t *testing.T) *App {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	redisURL := os.Getenv("TEST_REDIS_URL")
	if dsn == "" || redisURL == "" {
		t.Skip("TEST_DATABASE_URL and TEST_REDIS_URL enable real database integration checks")
	}
	ctx := context.Background()
	base, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("cannot connect to integration database")
	}
	schemaName := "member_test_" + strings.ReplaceAll(newID(), "-", "")
	quoted := pgx.Identifier{schemaName}.Sanitize()
	if _, err = base.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		base.Close()
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			t.Fatalf("cannot create isolated integration schema: %s %s", pgErr.Code, pgErr.Message)
		}
		t.Fatal("cannot create isolated integration schema")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid integration database configuration")
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schemaName
	cfg.MaxConns = 5
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("cannot create integration connection pool")
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal("invalid integration Redis configuration")
	}
	cache := redis.NewClient(opts)
	a := &App{DB: db, Redis: cache, Config: Config{MediaDir: t.TempDir(), MaxUploadBytes: 1 << 20, AllowedOrigins: []string{"http://qingye.test"}}}
	t.Cleanup(func() {
		cache.Close()
		db.Close()
		_, _ = base.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
		base.Close()
	})
	if err = a.Migrate(ctx); err != nil {
		t.Fatalf("isolated migration failed: %v", err)
	}
	if cache.Ping(ctx).Err() != nil {
		t.Fatal("integration Redis unavailable")
	}
	return a
}

type memberHTTP struct {
	t       *testing.T
	handler http.Handler
	ip      string
	origin  string
}

func (h memberHTTP) call(method, path string, body any, cookie *http.Cookie, want int) *httptest.ResponseRecorder {
	h.t.Helper()
	var raw []byte
	switch v := body.(type) {
	case nil:
	case []byte:
		raw = v
	default:
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.RemoteAddr = h.ip + ":1234"
	origin := h.origin
	if origin == "" {
		origin = "http://qingye.test"
	}
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, r)
	if w.Code != want {
		h.t.Fatalf("%s %s returned %d, wanted %d: %s", method, path, w.Code, want, w.Body.String())
	}
	return w
}
func memberData(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

type pausedUploadReader struct {
	reader          io.Reader
	entered, resume chan struct{}
	once            sync.Once
}

func (p *pausedUploadReader) Read(b []byte) (int, error) {
	p.once.Do(func() { close(p.entered); <-p.resume })
	return p.reader.Read(b)
}

func TestUploadStreamDoesNotHoldDatabase(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	id, token := newID(), randomToken()
	_, err := a.DB.Exec(ctx, `INSERT INTO users(id,phone,nickname,password_hash) VALUES($1,'stream-test','流式上传测试','unused')`, id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.DB.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 hour')`, tokenHash(token), id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Redis.Del(ctx, "qingye:limit:upload:"+id).Err() })
	cookie := &http.Cookie{Name: sessionCookie, Value: token}
	h := memberHTTP{t: t, handler: a.Routes(), ip: "stream-test"}
	data := testPNG(t)
	ticket := memberData(t, h.call("POST", "/api/v1/uploads", map[string]any{"filename": "stream.png", "mime_type": "image/png", "byte_size": len(data)}, cookie, 201))
	paused := &pausedUploadReader{reader: bytes.NewReader(data), entered: make(chan struct{}), resume: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(paused.resume) })
	r := httptest.NewRequest("PUT", ticket["upload_url"].(string), paused)
	r.Header.Set("Origin", "http://qingye.test")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); h.handler.ServeHTTP(w, r) }()
	select {
	case <-paused.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not reach body stream")
	}
	if acquired := a.DB.Stat().AcquiredConns(); acquired != 0 {
		t.Errorf("slow HTTP body holds %d database connections", acquired)
	}
	release.Do(func() { close(paused.resume) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not finish")
	}
	if w.Code != 200 {
		t.Fatalf("upload failed: %d %s", w.Code, w.Body.String())
	}
	h.call("PUT", ticket["upload_url"].(string), bytes.Repeat([]byte{'x'}, len(data)), cookie, 200)
	h.call("POST", "/api/v1/uploads/"+ticket["media_id"].(string)+"/complete", nil, cookie, 200)
}
func TestMemberMediaIntegration(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	run := newID()
	h := memberHTTP{t: t, handler: a.Routes(), ip: "test-" + run}
	adminApp := App{DB: a.DB, Redis: a.Redis, Config: a.Config}
	adminApp.Config = a.Config
	adminApp.Config.Surface = "admin"
	adminApp.Config.AllowedOrigins = []string{"http://qingye-admin.test"}
	ah := memberHTTP{t: t, handler: adminApp.Routes(), ip: h.ip, origin: "http://qingye-admin.test"}
	ownerID, otherID, adminID := newID(), newID(), newID()
	password := "OnlyForEphemeralIntegration!"
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	phones := map[string]string{ownerID: "t-owner-" + run[:8], otherID: "t-other-" + run[:8], adminID: "t-admin-" + run[:8]}
	for _, id := range []string{ownerID, otherID, adminID} {
		role := "member"
		if id == adminID {
			role = "admin"
		}
		if _, err := a.DB.Exec(ctx, `INSERT INTO users(id,phone,nickname,password_hash,role) VALUES($1,$2,'测试账户',$3,$4)`, id, phones[id], string(hash), role); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		keys := []string{"qingye:limit:login-ip:public:" + tokenHash(h.ip), "qingye:limit:login-ip:admin:" + tokenHash(h.ip), "qingye:limit:upload:" + ownerID}
		for _, phone := range phones {
			keys = append(keys, "qingye:limit:login-phone:public:"+tokenHash(phone), "qingye:limit:login-phone:admin:"+tokenHash(phone))
		}
		_ = a.Redis.Del(ctx, keys...).Err()
	})
	_, err := a.DB.Exec(ctx, `INSERT INTO categories(id,name,trade_type,price_units) VALUES('test-sale','测试出售','sale','["account"]')`)
	if err != nil {
		t.Fatal(err)
	}
	login := func(id string) *http.Cookie {
		client := h
		if id == adminID {
			client = ah
		}
		w := client.call("POST", "/api/v1/auth/login", map[string]any{"method": "password", "phone": phones[id], "password": password}, nil, 200)
		cookies := w.Result().Cookies()
		if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
			t.Fatal("session cookie is missing safety attributes")
		}
		var stored string
		if err := a.DB.QueryRow(ctx, "SELECT token_hash FROM sessions WHERE user_id=$1", id).Scan(&stored); err != nil || stored == cookies[0].Value || stored != tokenHash(cookies[0].Value) {
			t.Fatal("session token not stored as hash")
		}
		return cookies[0]
	}
	owner, other, admin := login(ownerID), login(otherID), login(adminID)
	h.call("POST", "/api/v1/auth/sms", map[string]any{"phone": phones[ownerID]}, nil, 503)
	h.call("POST", "/api/v1/auth/register", map[string]any{}, nil, 503)
	h.call("POST", "/api/v1/me/products", map[string]any{"owner_id": otherID}, owner, 422)
	if _, err := a.DB.Exec(ctx, `INSERT INTO product_dictionary_options(dictionary_key,value,label) VALUES('server','测试区','测试区')`); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"category_id": "test-sale", "title": "联调商品", "trade_type": "sale", "price_unit": "account", "price_amount": "123.45", "attributes": map[string]any{"server": "测试区", "gender": "female"}, "description": "集成测试资料", "request_id": newID()}
	first := memberData(t, h.call("POST", "/api/v1/me/products", input, owner, 200))
	pid := first["id"].(string)
	path := "/api/v1/me/products/" + pid
	replay := memberData(t, h.call("POST", "/api/v1/me/products", input, owner, 200))
	if replay["id"] != pid {
		t.Fatal("create replay made a second product")
	}
	input["title"] = "changed"
	h.call("POST", "/api/v1/me/products", input, owner, 409)
	input["title"] = "联调商品"
	h.call("GET", path, nil, other, 404)
	h.call("GET", "/api/v1/admin/products/"+pid, nil, other, 404)
	ah.call("GET", "/api/v1/admin/products/"+pid, nil, other, 401)
	h.call("POST", path+"/submit", map[string]any{"expected_version": 1}, owner, 422)
	pngBytes := testPNG(t)
	signed := memberData(t, h.call("POST", "/api/v1/uploads", map[string]any{"filename": "test.png", "mime_type": "image/png", "byte_size": len(pngBytes)}, owner, 201))
	mid := signed["media_id"].(string)
	uploadURL := signed["upload_url"].(string)
	h.call("PUT", uploadURL, pngBytes, owner, 200)
	h.call("PUT", uploadURL, pngBytes, owner, 200)
	h.call("POST", "/api/v1/uploads/"+mid+"/complete", nil, other, 404)
	h.call("POST", "/api/v1/uploads/"+mid+"/complete", nil, owner, 200)
	h.call("POST", "/api/v1/uploads/"+mid+"/complete", nil, owner, 200)
	mediaURL := "/api/v1/media/" + mid + "/file"
	h.call("GET", mediaURL, nil, nil, 404)
	h.call("GET", mediaURL, nil, other, 404)
	h.call("GET", mediaURL, nil, owner, 200)
	appendInput := map[string]any{"media_ids": []string{mid}, "expected_version": 1, "request_id": newID()}
	appended := memberData(t, h.call("POST", path+"/images", appendInput, owner, 200))
	if appended["version"] != float64(2) {
		t.Fatal("append version did not advance")
	}
	again := memberData(t, h.call("POST", path+"/images", appendInput, owner, 200))
	if again["version"] != float64(2) || again["image_total"] != float64(1) {
		t.Fatal("image append was not idempotent")
	}
	h.call("DELETE", path+"/images/"+mid, map[string]any{"expected_version": 1}, owner, 409)
	h.call("POST", path+"/submit", map[string]any{"expected_version": 2}, owner, 200)
	ah.call("POST", "/api/v1/admin/products/"+pid+"/publish", map[string]any{"expected_version": 3}, admin, 409)
	published := memberData(t, ah.call("POST", "/api/v1/admin/products/"+pid+"/review", map[string]any{"expected_version": 3, "decision": "approve"}, admin, 200))
	version := int(published["version"].(float64))
	h.call("GET", mediaURL, nil, nil, 200)
	thumbResponse := h.call("GET", mediaURL+"?variant=thumb", nil, nil, 200)
	if thumbResponse.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatal("thumbnail variant did not return a JPEG")
	}
	favoritePath := "/api/v1/me/favorites/" + pid
	h.call("PUT", favoritePath, nil, other, 200)
	_, err = a.DB.Exec(ctx, "UPDATE products SET review_note='INTERNAL-REVIEW-ONLY' WHERE id=$1", pid)
	if err != nil {
		t.Fatal(err)
	}
	publicFavorite := h.call("GET", "/api/v1/me/favorites", nil, other, 200)
	if strings.Contains(publicFavorite.Body.String(), "INTERNAL-REVIEW-ONLY") || strings.Contains(publicFavorite.Body.String(), "approved_version") {
		t.Fatal("favorite of another user's product leaked review internals")
	}
	var firstFavorite time.Time
	if err = a.DB.QueryRow(ctx, "SELECT created_at FROM favorites WHERE user_id=$1 AND product_id=$2", otherID, pid).Scan(&firstFavorite); err != nil {
		t.Fatal(err)
	}
	h.call("DELETE", favoritePath, nil, other, 200)
	h.call("PUT", favoritePath, nil, other, 200)
	var againFavorite time.Time
	_ = a.DB.QueryRow(ctx, "SELECT created_at FROM favorites WHERE user_id=$1 AND product_id=$2", otherID, pid).Scan(&againFavorite)
	if !firstFavorite.Equal(againFavorite) {
		t.Fatal("favorite reactivation changed first-created timestamp")
	}
	edit := map[string]any{}
	for k, v := range input {
		edit[k] = v
	}
	edit["expected_version"] = version
	h.call("PATCH", path, edit, other, 404)
	changed := memberData(t, h.call("PATCH", path, edit, owner, 200))
	version = int(changed["version"].(float64))
	if changed["status"] != "draft" {
		t.Fatal("published edit did not withdraw approval")
	}
	h.call("GET", mediaURL, nil, nil, 404)
	h.call("GET", "/api/v1/products/"+pid, nil, nil, 404)
	h.call("GET", mediaURL+"?variant=thumb", nil, nil, 404)
	favorites := h.call("GET", "/api/v1/me/favorites", nil, other, 200)
	if strings.Contains(favorites.Body.String(), "集成测试资料") || strings.Contains(favorites.Body.String(), mid) {
		t.Fatal("offline favorite exposed private content")
	}
	ah.call("GET", "/api/v1/admin/products/"+pid+"/images", nil, admin, 200)
	var reviewID string
	err = a.DB.QueryRow(ctx, "SELECT id FROM product_reviews WHERE product_id=$1 ORDER BY created_at DESC LIMIT 1", pid).Scan(&reviewID)
	if err != nil {
		t.Fatal(err)
	}
	history := ah.call("GET", "/api/v1/admin/products/"+pid+"/images?review_id="+reviewID, nil, admin, 200)
	if !strings.Contains(history.Body.String(), mid) {
		t.Fatal("review snapshot lost original media")
	}
	h.call("GET", path+"/images?review_id="+reviewID, nil, owner, 403)
	ah.call("GET", "/api/v1/admin/products/not-the-reviewed-product/images?review_id="+reviewID, nil, admin, 404)
	// Add more than 200 images in legal batches. This is a per-request limit, never a product limit.
	var storageKey string
	_ = a.DB.QueryRow(ctx, "SELECT storage_key FROM media_files WHERE id=$1", mid).Scan(&storageKey)
	// Imported originals have no owner and remain readable only through a visible
	// approved product. A stale approval or disabled category must hide them.
	legacyProduct, legacyMedia := newID(), newID()
	_, err = a.DB.Exec(ctx, `INSERT INTO products(id,product_code,category_id,title,trade_type,price_unit,status,source,version,approved_version,legacy_price) VALUES($1,'LEGACY-TEST','test-sale','旧站原始资料','sale','unknown','published','legacy',1,1,'原价待核对')`, legacyProduct)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.DB.Exec(ctx, `INSERT INTO media_files(id,filename,mime_type,storage_key,status,source_url) VALUES($1,'legacy.png','image/png',$2,'ready','https://invalid.example/no-dynamic-proxy')`, legacyMedia, storageKey)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.DB.Exec(ctx, `INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,$2,1)`, legacyProduct, legacyMedia)
	if err != nil {
		t.Fatal(err)
	}
	legacyURL := "/api/v1/media/" + legacyMedia + "/file"
	h.call("GET", legacyURL, nil, nil, 200)
	_, _ = a.DB.Exec(ctx, "UPDATE products SET approved_version=NULL WHERE id=$1", legacyProduct)
	h.call("GET", legacyURL, nil, nil, 404)
	_, _ = a.DB.Exec(ctx, "UPDATE products SET approved_version=version WHERE id=$1", legacyProduct)
	_, _ = a.DB.Exec(ctx, "UPDATE categories SET enabled=false WHERE id='test-sale'")
	h.call("GET", legacyURL, nil, nil, 404)
	_, _ = a.DB.Exec(ctx, "UPDATE categories SET enabled=true WHERE id='test-sale'")
	imageIDs := []string{}
	for i := 0; i < 201; i++ {
		id := newID()
		_, err = a.DB.Exec(ctx, `INSERT INTO media_files(id,owner_id,filename,mime_type,storage_key,status) VALUES($1,$2,'fixture.png','image/png',$3,'ready')`, id, ownerID, storageKey)
		if err != nil {
			t.Fatal(err)
		}
		imageIDs = append(imageIDs, id)
	}
	for start := 0; start < len(imageIDs); start += 20 {
		end := start + 20
		if end > len(imageIDs) {
			end = len(imageIDs)
		}
		p := memberData(t, h.call("POST", path+"/images", map[string]any{"media_ids": imageIDs[start:end], "expected_version": version, "request_id": newID()}, owner, 200))
		version = int(p["version"].(float64))
	}
	pages := h.call("GET", fmt.Sprintf("%s/images?limit=50&expected_version=%d", path, version), nil, owner, 200)
	var page map[string]any
	_ = json.Unmarshal(pages.Body.Bytes(), &page)
	if page["total"] != float64(202) || len(page["data"].([]any)) != 50 || page["next_cursor"] == nil {
		t.Fatal("large image gallery pagination failed")
	}
	moved := memberData(t, h.call("PATCH", path+"/images/"+imageIDs[200], map[string]any{"expected_version": version, "before_media_id": mid}, owner, 200))
	oldVersion := version
	version = int(moved["version"].(float64))
	h.call("GET", fmt.Sprintf("%s/images?expected_version=%d", path, oldVersion), nil, owner, 409)
	// Admin image edits operate on other owners' products and leave an auditable draft.
	ah.call("DELETE", "/api/v1/admin/products/"+pid+"/images/"+imageIDs[0], map[string]any{"expected_version": version}, admin, 200)
	var audits int
	_ = a.DB.QueryRow(ctx, "SELECT count(*) FROM admin_logs WHERE action='images.delete' AND user_id=$1", adminID).Scan(&audits)
	if audits != 1 {
		t.Fatal("admin image mutation missing audit")
	}
	r := httptest.NewRequest("PATCH", "/api/v1/me", strings.NewReader(`{"nickname":"forged"}`))
	r.AddCookie(owner)
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cookie write without Origin/Referer was allowed")
	}
	_, err = a.DB.Exec(ctx, "UPDATE users SET enabled=false WHERE id=$1", ownerID)
	if err != nil {
		t.Fatal(err)
	}
	h.call("GET", "/api/v1/me", nil, owner, 401)
	h.call("POST", "/api/v1/auth/logout", nil, other, 200)
	h.call("GET", "/api/v1/me", nil, other, 401)
	// Redis limits are active, rather than silently bypassed when a password is wrong.
	for i := 0; i < 11; i++ {
		h.call("POST", "/api/v1/auth/login", map[string]any{"method": "password", "phone": phones[otherID], "password": "wrong"}, nil, 401)
	}
	h.call("POST", "/api/v1/auth/login", map[string]any{"method": "password", "phone": phones[otherID], "password": "wrong"}, nil, 429)
}
