package httpapi

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"qingye/backend/internal/config"
	"qingye/backend/internal/infrastructure/responsecrypto"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type responseMemoryKeys struct {
	mu   sync.Mutex
	keys map[string]responsecrypto.Key
}

func (s *responseMemoryKeys) Current(_ context.Context, id string) (responsecrypto.Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.keys[id]; ok {
		return k, nil
	}
	k := responsecrypto.Key{ContextID: id, ID: "500000-" + responsecrypto.RandomID(), Material: responsecrypto.RandomKey(), RotateAt: time.Now().Unix() + 3600, RetainUntil: time.Now().Unix() + 3720}
	s.keys[id] = k
	return k, nil
}
func (s *responseMemoryKeys) Lookup(_ context.Context, id, kid string) (responsecrypto.Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[id]
	if !ok || k.ID != kid {
		return responsecrypto.Key{}, responsecrypto.ErrKey
	}
	return k, nil
}

func responseFixture(t *testing.T) (*App, responsecrypto.Key, *http.Cookie) {
	t.Helper()
	a := &App{Config: Config{SessionSecret: strings.Repeat("x", 32), AllowedOrigins: []string{"http://qingye.test"}, ResponseCrypto: config.ResponseCryptoConfig{Enabled: true}}, ResponseKeys: &responseMemoryKeys{keys: map[string]responsecrypto.Key{}}}
	r := httptest.NewRequest("GET", responseInitPath, nil)
	r.Header.Set("X-QY-Init", "1")
	w := httptest.NewRecorder()
	a.middleware(http.HandlerFunc(a.responseInit)).ServeHTTP(w, r)
	var out struct {
		Data responsecrypto.Key `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Data.Material == "" {
		t.Fatalf("init failed: %d", w.Code)
	}
	cs := w.Result().Cookies()
	if len(cs) != 1 || !cs[0].HttpOnly {
		t.Fatal("visitor cookie missing HttpOnly")
	}
	return a, out.Data, cs[0]
}
func responseRequestFor(key responsecrypto.Key, cookie *http.Cookie, method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.AddCookie(cookie)
	r.Header.Set("X-QY-Protocol", responsecrypto.Protocol)
	r.Header.Set("X-QY-Context", key.ContextID)
	r.Header.Set("X-QY-Key-Id", key.ID)
	r.Header.Set("X-QY-Request-Id", "test-request-12345678")
	return r
}
func decryptResponse(t *testing.T, a *App, key responsecrypto.Key, r *http.Request, w *httptest.ResponseRecorder) []byte {
	t.Helper()
	raw, _ := base64.RawURLEncoding.DecodeString(key.Material)
	block, _ := aes.NewCipher(raw)
	gcm, _ := cipher.NewGCM(block)
	n, err := base64.RawURLEncoding.DecodeString(w.Header().Get("X-QY-Nonce"))
	if err != nil {
		t.Fatal(err)
	}
	aad := []byte(strings.Join([]string{"QY-R24-HTTP", "1", a.surface(), key.ContextID, key.ID, r.Header.Get("X-QY-Request-Id"), r.Method, r.URL.RequestURI(), strconv.Itoa(w.Code), w.Header().Get("X-QY-Content-Type"), w.Header().Get("X-QY-Content-Encoding")}, "\n"))
	body, err := gcm.Open(nil, n, w.Body.Bytes(), aad)
	if err != nil {
		t.Fatal("response not decryptable", err)
	}
	return body
}

func TestResponseEnvelopeHTTPJSONBinaryAndErrors(t *testing.T) {
	for _, tt := range []struct {
		name, content, path string
		status              int
		body                []byte
	}{
		{"json", "application/json; charset=utf-8", "/api/v1/products?q=one&sort=latest", 200, []byte(`{"private":"example"}`)},
		{"binary", "image/jpeg", "/api/v1/media/example/file", 200, []byte{0xff, 0xd8, 0, 0xff, 0xd9}},
		{"business-error", "application/json; charset=utf-8", "/api/v1/me", 403, []byte(`{"code":"FORBIDDEN"}`)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, key, cookie := responseFixture(t)
			r := responseRequestFor(key, cookie, "GET", tt.path)
			w := httptest.NewRecorder()
			a.middleware(a.responseCryptoMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.content)
				w.Header().Set("ETag", "private-cache-tag")
				w.WriteHeader(tt.status)
				_, _ = w.Write(tt.body)
			}))).ServeHTTP(w, r)
			if w.Code != tt.status || w.Header().Get("Content-Type") != responsecrypto.ContentType || w.Header().Get("ETag") != "" || w.Header().Get("Cache-Control") != "no-store" || bytes.Contains(w.Body.Bytes(), tt.body) {
				t.Fatal("clear response or incorrect metadata")
			}
			if !bytes.Equal(decryptResponse(t, a, key, r, w), tt.body) {
				t.Fatal("body changed")
			}
		})
	}
}

func TestResponseEnvelopeRejectsBeforeBusinessMutation(t *testing.T) {
	for _, which := range []string{"protocol", "context", "key", "cookie", "request-id"} {
		t.Run(which, func(t *testing.T) {
			a, key, cookie := responseFixture(t)
			r := responseRequestFor(key, cookie, "POST", "/api/v1/auth/sms")
			switch which {
			case "protocol":
				r.Header.Del("X-QY-Protocol")
			case "context":
				r.Header.Set("X-QY-Context", strings.Repeat("b", 64))
			case "key":
				r.Header.Set("X-QY-Key-Id", "500000-"+strings.Repeat("b", 32))
			case "cookie":
				r.Header.Del("Cookie")
			case "request-id":
				r.Header.Set("X-QY-Request-Id", "short")
			}
			called := false
			w := httptest.NewRecorder()
			a.responseCryptoMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(w, r)
			if called || w.Code < 400 || w.Header().Get("X-QY-Protocol-Error") != "1" {
				t.Fatal("invalid protocol executed business mutation")
			}
		})
	}
}

func TestResponseEnvelopeSurfaceAndMissingKeyIsolation(t *testing.T) {
	a, key, cookie := responseFixture(t)
	admin := &App{Config: a.Config, ResponseKeys: a.ResponseKeys}
	admin.Config.Surface = "admin"
	r := responseRequestFor(key, cookie, "GET", "/api/v1/me")
	w := httptest.NewRecorder()
	admin.responseCryptoMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("public context entered admin") })).ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatalf("cross surface: %d", w.Code)
	}
	r = httptest.NewRequest("GET", responseInitPath+"?key_id="+key.ID, nil)
	r.Header.Set("X-QY-Init", "1")
	w = httptest.NewRecorder()
	a.responseInit(w, r)
	if w.Code != 409 || strings.Contains(w.Body.String(), key.Material) {
		t.Fatal("old key fetched without context cookie")
	}
}

type blockingCryptoWriter struct {
	*httptest.ResponseRecorder
	started, allow chan struct{}
	partial        bool
}

func (w *blockingCryptoWriter) Write(p []byte) (int, error) {
	close(w.started)
	<-w.allow
	if w.partial {
		return 7, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(p)
}

func TestResponseEnvelopeCompletionHoldsUntilActualWrite(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(strconv.FormatBool(partial), func(t *testing.T) {
			a, key, cookie := responseFixture(t)
			a.Config.MaxConcurrentRequests = 1
			r := responseRequestFor(key, cookie, "GET", "/api/v1/media/fixture/file")
			w := &blockingCryptoWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), allow: make(chan struct{}), partial: partial}
			finished := make(chan int64, 1)
			done := make(chan struct{})
			h := a.requestLimits(a.responseCryptoMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !afterResponseWrite(r, func(int64) { panic("one completion cannot skip other leases") }) || !afterResponseWrite(r, func(n int64) { finished <- n }) {
					t.Error("completion not attached")
				}
				w.Header().Set("Content-Type", "image/jpeg")
				_, _ = w.Write([]byte("image-body"))
			})))
			go func() { defer close(done); h.ServeHTTP(w, r) }()
			select {
			case <-w.started:
			case <-time.After(3 * time.Second):
				t.Fatal("wire write not started")
			}
			select {
			case <-finished:
				t.Fatal("lease released before network write")
			default:
			}
			busy := httptest.NewRecorder()
			h.ServeHTTP(busy, responseRequestFor(key, cookie, "GET", "/api/v1/media/fixture/file"))
			if busy.Code != 503 || busy.Header().Get("X-QY-Protocol-Error") != "1" {
				t.Error("wire write released global request admission early")
			}
			close(w.allow)
			<-done
			n := <-finished
			want := int64(len("image-body") + 16)
			if partial {
				want = 7
			}
			if n != want {
				t.Fatalf("counted %d want wire %d", n, want)
			}
		})
	}
}

func TestResponseEnvelopeRangeReadOnlyAndNoBody(t *testing.T) {
	for _, method := range []string{"GET", "PATCH", "HEAD"} {
		t.Run(method, func(t *testing.T) {
			a, key, cookie := responseFixture(t)
			r := responseRequestFor(key, cookie, method, "/api/v1/media/fixture/file")
			r.Header.Set("Range", "bytes=0-1")
			r.Header.Set("If-Match", "version-one")
			w := httptest.NewRecorder()
			a.responseCryptoMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if (r.Header.Get("If-Match") == "") != (method != "PATCH") {
					t.Error("write precondition changed or partial read retained")
				}
				w.WriteHeader(204)
			})).ServeHTTP(w, r)
			if w.Code != 204 || w.Body.Len() != 0 {
				t.Fatal("no-body response gained ciphertext")
			}
		})
	}
}

func TestResponseEnvelopeBudgetCountsGrowthAndReleases(t *testing.T) {
	b := &responseBudget{used: (128 << 20) - 4096}
	w := &responseCapture{header: http.Header{}, limit: 16384, budget: b}
	if _, err := w.Write(make([]byte, 3000)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(make([]byte, 3000)); err == nil {
		t.Fatal("growth exceeded shared capacity")
	}
	w.release()
	if b.used != (128<<20)-4096 {
		t.Fatal("failed growth leaked budget")
	}
	w = &responseCapture{header: http.Header{}, limit: 4}
	if _, err := w.Write([]byte("12345")); err == nil {
		t.Fatal("single response exceeded bound")
	}
}

type gatedResponseKeys struct {
	responsecrypto.Store
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (s *gatedResponseKeys) Lookup(ctx context.Context, id, kid string) (responsecrypto.Key, error) {
	if s.calls.Add(1) == 1 {
		close(s.started)
	}
	select {
	case <-s.release:
		return s.Store.Lookup(ctx, id, kid)
	case <-ctx.Done():
		return responsecrypto.Key{}, ctx.Err()
	}
}

func TestResponseCryptoAdmissionBoundsPreflightAndWire(t *testing.T) {
	a, key, cookie := responseFixture(t)
	a.Config.MaxConcurrentRequests = 1
	gate := &gatedResponseKeys{Store: a.ResponseKeys, started: make(chan struct{}), release: make(chan struct{})}
	a.ResponseKeys = gate
	h := a.Routes()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(), responseRequestFor(key, cookie, "GET", "/api/v1/nonexistent"))
	}()
	select {
	case <-gate.started:
	case <-time.After(time.Second):
		t.Fatal("key preflight not reached")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, responseRequestFor(key, cookie, "GET", "/api/v1/nonexistent"))
	if w.Code != 503 || w.Header().Get("X-QY-Protocol-Error") != "1" || gate.calls.Load() != 1 {
		t.Fatal("saturated request entered key lookup")
	}
	init := httptest.NewRequest("GET", responseInitPath, nil)
	init.Header.Set("X-QY-Init", "1")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, init)
	if w.Code != 503 || w.Header().Get("X-QY-Protocol-Error") != "1" {
		t.Fatal("initialization bypassed saturated admission")
	}
	close(gate.release)
	<-done
}

func TestResponseCryptoPreflightHasRequestDeadline(t *testing.T) {
	a, key, cookie := responseFixture(t)
	a.Config.RequestTimeout = 20 * time.Millisecond
	gate := &gatedResponseKeys{Store: a.ResponseKeys, started: make(chan struct{}), release: make(chan struct{})}
	a.ResponseKeys = gate
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Routes().ServeHTTP(w, responseRequestFor(key, cookie, "GET", "/api/v1/nonexistent"))
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		close(gate.release)
		<-done
		t.Fatal("preflight lookup had no bounded deadline")
	}
	if w.Code != 503 || w.Header().Get("X-QY-Protocol-Error") != "1" {
		t.Fatal("timed out lookup did not fail closed")
	}
}

func TestResponseEnvelopeTrustedLocalImageStreamsWithoutAES(t *testing.T) {
	a, key, cookie := responseFixture(t)
	r := responseRequestFor(key, cookie, "GET", "/api/v1/media/fixture/file")
	w := &blockingCryptoWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), allow: make(chan struct{})}
	returned := make(chan struct{})
	done := make(chan struct{})
	finished := make(chan int64, 1)
	h := a.responseCryptoMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(returned)
		if !markServerLocalImage(r) || !afterResponseWrite(r, func(n int64) { finished <- n }) {
			t.Error("trusted local flag not attached")
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", "10")
		_, _ = w.Write([]byte("image-body"))
	}))
	go func() { defer close(done); h.ServeHTTP(w, r) }()
	select {
	case <-w.started:
	case <-time.After(time.Second):
		t.Fatal("local response did not reach wire")
	}
	select {
	case <-returned:
		t.Fatal("local image was fully captured before transmission")
	default:
	}
	select {
	case <-finished:
		t.Fatal("local lease released before wire completed")
	default:
	}
	close(w.allow)
	<-done
	if n := <-finished; n != 10 {
		t.Fatalf("local image counted encrypted bytes: %d", n)
	}
	if w.Body.String() != "image-body" || w.Header().Get("X-QY-Response-Mode") != "local-image" || w.Header().Get("X-QY-Nonce") != "" || w.Header().Get("X-QY-Context") != key.ContextID || w.Header().Get("Content-Type") != "image/jpeg" || w.Header().Get("X-QY-Content-Encoding") != "identity" {
		t.Fatal("local image response contract mismatch")
	}
}

func TestResponseEnvelopeLocalExceptionRejectsClientFlagsAndErrors(t *testing.T) {
	for _, tt := range []struct {
		name, path, mime string
		mark             bool
		status           int
	}{
		{"client-plain-flag", "/api/v1/media/fixture/file?plain=1", "image/jpeg", false, 200},
		{"marked-json", "/api/v1/media/fixture/file", "application/json", true, 200},
		{"marked-error", "/api/v1/media/fixture/file", "image/jpeg", true, 403},
		{"outside-media", "/api/v1/me", "image/jpeg", true, 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, key, cookie := responseFixture(t)
			r := responseRequestFor(key, cookie, "GET", tt.path)
			r.Header.Set("X-QY-Response-Mode", "local-image")
			w := httptest.NewRecorder()
			a.responseCryptoMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.mark {
					markServerLocalImage(r)
				}
				w.Header().Set("Content-Type", tt.mime)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte("protected-body"))
			})).ServeHTTP(w, r)
			if w.Header().Get("X-QY-Response-Mode") != "" || w.Header().Get("Content-Type") != responsecrypto.ContentType || string(decryptResponse(t, a, key, r, w)) != "protected-body" {
				t.Fatal("untrusted or non-image response bypassed AES")
			}
		})
	}
}
