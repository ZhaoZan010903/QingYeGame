package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func surfaceRequest(t *testing.T, h http.Handler, method, path, origin string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	switch b := body.(type) {
	case []byte:
		raw = b
	case nil:
	default:
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func statusIs(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("got %d want %d: %s", w.Code, want, w.Body.String())
	}
}

func TestSurfaceIsolationIntegration(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	publicOrigin, adminOrigin := "http://127.0.0.1:5179", "http://127.0.0.1:5181"
	a.Config.Surface = "public"
	a.Config.AllowedOrigins = []string{publicOrigin}
	admin := App{DB: a.DB, Redis: a.Redis, Config: a.Config}
	admin.Config = a.Config
	admin.Config.Surface = "admin"
	admin.Config.AllowedOrigins = []string{adminOrigin}
	pubAPI, adminAPI := a.Routes(), admin.Routes()
	password := "EphemeralSurfaceTest!"
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	memberID, adminID := newID(), newID()
	phoneMember, phoneAdmin := "sm-"+memberID[:8], "sa-"+adminID[:8]
	for _, user := range []struct{ id, phone, role string }{{memberID, phoneMember, "member"}, {adminID, phoneAdmin, "admin"}} {
		if _, err := a.DB.Exec(ctx, `INSERT INTO users(id,phone,nickname,password_hash,role) VALUES($1,$2,'隔离测试',$3,$4)`, user.id, user.phone, string(hash), user.role); err != nil {
			t.Fatal(err)
		}
	}
	cleanup := []string{"qingye:limit:upload:" + adminID, "qingye:limit:upload:" + memberID}
	for _, surface := range []string{"public", "admin"} {
		cleanup = append(cleanup, "qingye:limit:login-ip:"+surface+":"+tokenHash("192.0.2.1"))
		for _, phone := range []string{phoneMember, phoneAdmin} {
			cleanup = append(cleanup, "qingye:limit:login-phone:"+surface+":"+tokenHash(phone))
		}
	}
	t.Cleanup(func() { _ = a.Redis.Del(ctx, cleanup...).Err() })
	loginBody := func(phone string) any {
		return map[string]any{"method": "password", "phone": phone, "password": password}
	}
	statusIs(t, surfaceRequest(t, pubAPI, "POST", "/api/v1/auth/login", publicOrigin, loginBody(phoneAdmin)), 401)
	statusIs(t, surfaceRequest(t, adminAPI, "POST", "/api/v1/auth/login", adminOrigin, loginBody(phoneMember)), 401)
	loginMember := surfaceRequest(t, pubAPI, "POST", "/api/v1/auth/login", publicOrigin, loginBody(phoneMember))
	statusIs(t, loginMember, 200)
	memberCookie := loginMember.Result().Cookies()[0]
	loginAdmin := surfaceRequest(t, adminAPI, "POST", "/api/v1/auth/login", adminOrigin, loginBody(phoneAdmin))
	statusIs(t, loginAdmin, 200)
	adminCookie := loginAdmin.Result().Cookies()[0]
	if memberCookie.Name != sessionCookie || adminCookie.Name != adminSessionCookie {
		t.Fatal("surfaces share a session cookie name")
	}
	var audience string
	if err := a.DB.QueryRow(ctx, "SELECT audience FROM sessions WHERE token_hash=$1", tokenHash(adminCookie.Value)).Scan(&audience); err != nil || audience != "admin" {
		t.Fatal("administrator token has incorrect database audience")
	}
	for _, entry := range []struct {
		api    http.Handler
		origin string
		cookie *http.Cookie
	}{{pubAPI, publicOrigin, adminCookie}, {adminAPI, adminOrigin, memberCookie}, {pubAPI, publicOrigin, &http.Cookie{Name: sessionCookie, Value: adminCookie.Value}}, {adminAPI, adminOrigin, &http.Cookie{Name: adminSessionCookie, Value: memberCookie.Value}}} {
		statusIs(t, surfaceRequest(t, entry.api, "GET", "/api/v1/me", entry.origin, nil, entry.cookie), 401)
	}
	// Browsers send both same-host cookies regardless of port. Each API must pick
	// only its own cookie and then independently validate the stored audience/role.
	bothPublic := surfaceRequest(t, pubAPI, "GET", "/api/v1/me", publicOrigin, nil, memberCookie, adminCookie)
	statusIs(t, bothPublic, 200)
	if memberData(t, bothPublic)["id"] != memberID {
		t.Fatal("public API selected administrator cookie")
	}
	bothAdmin := surfaceRequest(t, adminAPI, "GET", "/api/v1/me", adminOrigin, nil, memberCookie, adminCookie)
	statusIs(t, bothAdmin, 200)
	if memberData(t, bothAdmin)["id"] != adminID {
		t.Fatal("admin API selected member cookie")
	}
	for _, path := range []string{"/api/v1/admin/products", "/api/v1/admin/users", "/api/v1/admin/settings"} {
		statusIs(t, surfaceRequest(t, pubAPI, "GET", path, publicOrigin, nil, memberCookie, adminCookie), 404)
	}
	for _, path := range []string{"/api/v1/me/products", "/api/v1/me/favorites", "/api/v1/auth/register"} {
		statusIs(t, surfaceRequest(t, adminAPI, "POST", path, adminOrigin, map[string]any{}, adminCookie), 404)
	}
	for _, path := range []string{"/api/v1/categories", "/api/v1/products", "/api/v1/home", "/api/v1/contacts", "/api/v1/media/not-found/file"} {
		statusIs(t, surfaceRequest(t, adminAPI, "GET", path, adminOrigin, nil), 401)
	}
	statusIs(t, surfaceRequest(t, adminAPI, "GET", "/healthz", "", nil), 200)
	statusIs(t, surfaceRequest(t, adminAPI, "POST", "/api/v1/admin/categories", publicOrigin, map[string]any{}, adminCookie), 403)
	statusIs(t, surfaceRequest(t, pubAPI, "PATCH", "/api/v1/me", adminOrigin, map[string]any{"nickname": "wrong origin"}, memberCookie), 403)
	statusIs(t, surfaceRequest(t, adminAPI, "POST", "/api/v1/admin/categories", "", map[string]any{}, adminCookie), 403)
	// Even renaming an admin token to the public cookie cannot revoke that token.
	statusIs(t, surfaceRequest(t, pubAPI, "POST", "/api/v1/auth/logout", publicOrigin, nil, &http.Cookie{Name: sessionCookie, Value: adminCookie.Value}), 200)
	statusIs(t, surfaceRequest(t, adminAPI, "GET", "/api/v1/me", adminOrigin, nil, adminCookie), 200)
	// Signed upload tickets are bound to their issuing surface as well.
	imageBytes := testPNG(t)
	ticketBody := map[string]any{"filename": "scope.png", "mime_type": "image/png", "byte_size": len(imageBytes)}
	adminTicketResponse := surfaceRequest(t, adminAPI, "POST", "/api/v1/uploads", adminOrigin, ticketBody, adminCookie)
	statusIs(t, adminTicketResponse, 201)
	adminTicket := memberData(t, adminTicketResponse)
	publicTicketResponse := surfaceRequest(t, pubAPI, "POST", "/api/v1/uploads", publicOrigin, ticketBody, memberCookie)
	statusIs(t, publicTicketResponse, 201)
	publicTicket := memberData(t, publicTicketResponse)
	statusIs(t, surfaceRequest(t, pubAPI, "PUT", adminTicket["upload_url"].(string), publicOrigin, imageBytes, memberCookie), 403)
	statusIs(t, surfaceRequest(t, adminAPI, "PUT", publicTicket["upload_url"].(string), adminOrigin, imageBytes, adminCookie), 403)
	statusIs(t, surfaceRequest(t, adminAPI, "PUT", adminTicket["upload_url"].(string), adminOrigin, imageBytes), 401)
	statusIs(t, surfaceRequest(t, adminAPI, "PUT", adminTicket["upload_url"].(string), adminOrigin, imageBytes, adminCookie), 200)
	statusIs(t, surfaceRequest(t, adminAPI, "POST", "/api/v1/uploads/"+adminTicket["media_id"].(string)+"/complete", adminOrigin, nil, adminCookie), 200)
	statusIs(t, surfaceRequest(t, pubAPI, "GET", "/api/v1/media/"+adminTicket["media_id"].(string)+"/file", publicOrigin, nil, adminCookie), 404)
	// Role changes invalidate previously correct audience tokens without waiting
	// for expiry, even if a cookie is retained by a browser.
	_, err := a.DB.Exec(ctx, "UPDATE users SET role='member' WHERE id=$1", adminID)
	if err != nil {
		t.Fatal(err)
	}
	statusIs(t, surfaceRequest(t, adminAPI, "GET", "/api/v1/me", adminOrigin, nil, adminCookie), 401)
}

func TestSeededAndUnknownPasswordCostsMatch(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	id, err := a.SeedUser(ctx, strings.NewReader(`{"phone":"cost-acceptance","nickname":"密码成本测试","password":"Ephemeral-Cost-Only-Password","role":"member"}`))
	if err != nil {
		t.Fatal(err)
	}
	var seeded string
	if err = a.DB.QueryRow(ctx, "SELECT password_hash FROM users WHERE id=$1", id).Scan(&seeded); err != nil {
		t.Fatal(err)
	}
	actual, err := bcrypt.Cost([]byte(seeded))
	if err != nil {
		t.Fatal(err)
	}
	dummy, err := bcrypt.Cost([]byte(dummyPasswordHash))
	if err != nil {
		t.Fatal(err)
	}
	if actual != 12 || actual != dummy {
		t.Fatalf("real and unknown-account password work differs: seeded=%d dummy=%d", actual, dummy)
	}
}

func TestAudienceMigrationPreservesOnlyCorrectRole(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	id, token := newID(), randomToken()
	_, err := a.DB.Exec(ctx, "ALTER TABLE sessions DROP COLUMN audience; ALTER TABLE media_files DROP COLUMN upload_audience")
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.DB.Exec(ctx, `INSERT INTO users(id,phone,nickname,password_hash) VALUES($1,'migration-test','兼容测试','unused')`, id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.DB.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 hour')`, tokenHash(token), id)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = a.Migrate(ctx); err != nil {
		t.Fatal("audience migration is not idempotent")
	}
	cookie := &http.Cookie{Name: sessionCookie, Value: token}
	h := a.Routes()
	statusIs(t, surfaceRequest(t, h, "GET", "/api/v1/me", "http://qingye.test", nil, cookie), 200)
	_, err = a.DB.Exec(ctx, "UPDATE users SET role='admin' WHERE id=$1", id)
	if err != nil {
		t.Fatal(err)
	}
	statusIs(t, surfaceRequest(t, h, "GET", "/api/v1/me", "http://qingye.test", nil, cookie), 401)
	admin := App{DB: a.DB, Redis: a.Redis, Config: a.Config}
	admin.Config = a.Config
	admin.Config.Surface = "admin"
	statusIs(t, surfaceRequest(t, admin.Routes(), "GET", "/api/v1/me", "http://qingye.test", nil, &http.Cookie{Name: adminSessionCookie, Value: token}), 401)
}

func TestConcurrentVersionAndIdempotencyIntegration(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	id, token := newID(), randomToken()
	_, err := a.DB.Exec(ctx, `INSERT INTO users(id,phone,nickname,password_hash) VALUES($1,'concurrent-test','并发测试','unused')`, id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.DB.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 hour')`, tokenHash(token), id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.DB.Exec(ctx, `INSERT INTO categories(id,name,trade_type,price_units) VALUES('parallel','并发测试','sale','["account"]')`)
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: sessionCookie, Value: token}
	handler := a.Routes()
	input := map[string]any{"category_id": "parallel", "title": "并发草稿", "trade_type": "sale", "price_unit": "account", "price_amount": "100.00", "description": "并发版本和幂等测试", "request_id": newID()}
	var responses [2]*httptest.ResponseRecorder
	var wg sync.WaitGroup
	gate := make(chan struct{})
	for i := range 2 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-gate
			responses[index] = surfaceRequest(t, handler, "POST", "/api/v1/me/products", "http://qingye.test", input, cookie)
		}(i)
	}
	close(gate)
	wg.Wait()
	for _, response := range responses {
		statusIs(t, response, 200)
	}
	first, second := memberData(t, responses[0]), memberData(t, responses[1])
	if first["id"] != second["id"] {
		t.Fatal("concurrent replay created duplicate product")
	}
	pid := first["id"].(string)
	var total int
	if err = a.DB.QueryRow(ctx, "SELECT count(*) FROM products WHERE owner_id=$1", id).Scan(&total); err != nil || total != 1 {
		t.Fatal("concurrent create persisted duplicate rows")
	}
	gate = make(chan struct{})
	for i := range 2 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			edit := map[string]any{}
			for k, v := range input {
				edit[k] = v
			}
			edit["expected_version"] = 1
			edit["title"] = strings.Repeat("并", index+1)
			<-gate
			responses[index] = surfaceRequest(t, handler, "PATCH", "/api/v1/me/products/"+pid, "http://qingye.test", edit, cookie)
		}(i)
	}
	close(gate)
	wg.Wait()
	codes := map[int]int{}
	for _, response := range responses {
		codes[response.Code]++
	}
	if codes[200] != 1 || codes[409] != 1 {
		t.Fatalf("concurrent edits did not serialize: %v", codes)
	}
	var version int
	if err = a.DB.QueryRow(ctx, "SELECT version FROM products WHERE id=$1", pid).Scan(&version); err != nil || version != 2 {
		t.Fatal("concurrent update lost version guard")
	}
}
