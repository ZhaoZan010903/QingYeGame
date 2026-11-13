package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	app "qingye/backend/internal/application/security"
	"qingye/backend/internal/config"
	domain "qingye/backend/internal/domain/security"
	"qingye/backend/internal/infrastructure/cache"
	"qingye/backend/internal/infrastructure/postgres"
	"strconv"
	"strings"
	"testing"
)

type readTestVerifier struct{}

func (readTestVerifier) Verify(_ context.Context, token, action, host, nonce, ip string) (bool, error) {
	return token == "isolated-success" && action == domain.MediaReadAction && host == "qingye.test", nil
}
func enableReadProtection(t *testing.T, a *App, namespace string) *cache.MediaReadStore {
	t.Helper()
	a.Config.SessionSecret = strings.Repeat("test-only-secret", 3)
	a.Config.MediaSafety = config.MediaSafetyConfig{Enabled: true, SiteKey: "0xisolated-public-key", Hostnames: []string{"qingye.test", "qingye-admin.test"}, Policy: domain.DefaultReadPolicy()}
	store := &cache.MediaReadStore{Client: a.Redis, Policy: a.Config.MediaSafety.Policy, Namespace: namespace}
	a.ReadSafety = &app.ReadService{Store: store, Audit: postgres.MediaReadAudit{DB: a.DB}, NewID: newID}
	a.HumanSafety = &app.HumanService{Store: store, Verifier: readTestVerifier{}, NewID: newID}
	t.Cleanup(func() {
		ctx := context.Background()
		var cursor uint64
		for {
			keys, next, e := a.Redis.Scan(ctx, cursor, namespace+"*", 100).Result()
			if e != nil {
				return
			}
			if len(keys) > 0 {
				a.Redis.Del(ctx, keys...)
			}
			cursor = next
			if cursor == 0 {
				return
			}
		}
	})
	return store
}
func TestR201MediaChallengeFlowAliasesAndPrivacy(t *testing.T) {
	f := newChatFixture(t)
	mid := f.image(t, f.member, "product", "")
	private := f.image(t, f.member, "product", "")
	if _, e := f.a.DB.Exec(context.Background(), "INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,$2,0)", f.product, mid); e != nil {
		t.Fatal(e)
	}
	s := enableReadProtection(t, f.a, "test:r201-http:"+newID()+":")
	s.Policy.Burst = 1
	s.Policy.PerSecond = .0001
	h := f.a.Routes()
	status := surfaceRequest(t, h, "GET", "/api/v1/security/media-access", "", nil)
	statusIs(t, status, 200)
	cookies := status.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("missing signed anonymous cookie")
	}
	call := func(method, path, origin string, body any, want int) *httptest.ResponseRecorder {
		r := surfaceRequest(t, h, method, path, origin, body, cookies...)
		statusIs(t, r, want)
		return r
	}
	delivered := call("GET", "/api/v1/media/"+mid, "", nil, 200).Body.Len()
	blocked := call("GET", "/api/v1/media/"+mid+"/preview", "", nil, 403)
	if blocked.Header().Get("Retry-After") == "" || !strings.Contains(blocked.Body.String(), "MEDIA_VERIFICATION_REQUIRED") {
		t.Fatal("no machine-readable denial")
	}
	call("POST", "/api/v1/security/challenges", "https://evil.test", map[string]any{}, 403)
	challenge := memberData(t, call("POST", "/api/v1/security/challenges", "http://qingye.test", map[string]any{}, 201))
	payload := map[string]any{"id": challenge["id"], "token": "isolated-success"}
	other := surfaceRequest(t, h, "POST", "/api/v1/security/challenges/verify", "http://qingye.test", payload)
	statusIs(t, other, 422)
	call("POST", "/api/v1/security/challenges/verify", "http://qingye.test", payload, 200)
	call("POST", "/api/v1/security/challenges/verify", "http://qingye.test", payload, 422)
	for _, suffix := range []string{"/file", "/preview?variant=thumb"} {
		delivered += call("GET", "/api/v1/media/"+mid+suffix, "", nil, 200).Body.Len()
	}
	_, metrics, e := s.Records(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	reserved, _ := strconv.Atoi(metrics["reserved_bytes"])
	if reserved < delivered {
		t.Fatalf("larger derived image bypassed reservation: reserved=%d delivered=%d", reserved, delivered)
	}
	call("GET", "/api/v1/media/"+private+"/file", "", nil, 404)
	// Existing publication/ownership checks still execute before admission.
	f.a.DB.Exec(context.Background(), "UPDATE products SET status='offline' WHERE id=$1", f.product)
	call("GET", "/api/v1/media/"+mid+"/file", "", nil, 404)
	call("GET", "/api/v1/admin/security/media-access", "", nil, 404)
}

func TestR201SingleRangeAndMissingImageDoNotBypassAdmission(t *testing.T) {
	f := newChatFixture(t)
	id := f.image(t, f.member, "product", "")
	if _, e := f.a.DB.Exec(context.Background(), "INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,$2,0)", f.product, id); e != nil {
		t.Fatal(e)
	}
	enableReadProtection(t, f.a, "test:r201-range:"+newID()+":")
	h := f.a.Routes()
	for _, tc := range []struct {
		rangeHeader string
		want        int
	}{{"bytes=0-7", 206}, {"bytes=0-1,3-4", 416}, {"bytes=99999999-", 416}} {
		r := httptest.NewRequest("GET", "/api/v1/media/"+id+"/file", nil)
		r.Header.Set("Range", tc.rangeHeader)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		statusIs(t, w, tc.want)
		if tc.want == 206 && w.Body.Len() != 8 {
			t.Fatal("range response expanded")
		}
	}
	statusIs(t, surfaceRequest(t, h, "GET", "/api/v1/media/"+newID()+"/file", "", nil), 404)
}
func TestR201AdminCASAuditAndAnonymousCookieTampering(t *testing.T) {
	f := newChatFixture(t)
	mid := f.image(t, f.member, "product", "")
	ns := "test:r201-audit:" + newID() + ":"
	enableReadProtection(t, f.admin, ns)
	h := f.admin.Routes()
	w := surfaceRequest(t, h, "GET", "/api/v1/media/"+mid+"/file", "", nil, f.cookies[f.first])
	statusIs(t, w, 200)
	records, _, e := f.admin.ReadSafety.Store.Records(context.Background())
	if e != nil || len(records) != 1 {
		t.Fatal(e)
	}
	row := records[0]
	path := "/api/v1/admin/security/media-access/" + row.Subject
	input := map[string]any{"expected_version": row.Version, "duration_seconds": 600, "reason": "隔离验收限制"}
	statusIs(t, surfaceRequest(t, h, "PUT", path, "http://qingye-admin.test", input, f.cookies[f.member]), 401)
	statusIs(t, surfaceRequest(t, h, "PUT", path, "", input, f.cookies[f.first]), 403)
	statusIs(t, surfaceRequest(t, h, "PUT", path, "http://qingye-admin.test", input, f.cookies[f.first]), 200)
	statusIs(t, surfaceRequest(t, h, "PUT", path, "http://qingye-admin.test", input, f.cookies[f.first]), 409)
	statusIs(t, surfaceRequest(t, h, "GET", "/api/v1/media/"+mid+"/file", "", nil, f.cookies[f.first]), 429)
	var applied, rejected int
	if e = f.a.DB.QueryRow(context.Background(), `SELECT count(*) FILTER(WHERE detail->>'outcome'='applied'),count(*) FILTER(WHERE detail->>'outcome'='not-applied') FROM admin_logs WHERE action='security.media-read'`).Scan(&applied, &rejected); e != nil || applied != 1 || rejected != 1 {
		t.Fatal("audit incomplete", applied, rejected, e)
	}
	records, _, _ = f.admin.ReadSafety.Store.Records(context.Background())
	input["expected_version"] = records[0].Version
	input["duration_seconds"] = 0
	statusIs(t, surfaceRequest(t, h, "PUT", path, "http://qingye-admin.test", input, f.cookies[f.first]), 200)
	statusIs(t, surfaceRequest(t, h, "GET", "/api/v1/media/"+mid+"/file", "", nil, f.cookies[f.first]), 200)
}

func TestR201SubjectIgnoresForgedProxyAndCookie(t *testing.T) {
	a := &App{Config: Config{SessionSecret: strings.Repeat("s", 32)}}
	r := httptest.NewRequest("GET", "/api/v1/security/media-access", nil)
	r.RemoteAddr = "192.0.2.8:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.99")
	w := httptest.NewRecorder()
	subject, e := a.mediaSubject(w, r)
	if e != nil || subject.IPKey != a.mediaMAC("ip:192.0.2.8") {
		t.Fatal("untrusted proxy changed budget", e)
	}
	cookie := w.Result().Cookies()[0]
	cookie.Value = cookie.Value[:len(cookie.Value)-1] + "!"
	r.AddCookie(cookie)
	newer, e := a.mediaSubject(httptest.NewRecorder(), r)
	if e != nil || newer.Key == subject.Key {
		t.Fatal("forged anonymous identity accepted", e)
	}
}
