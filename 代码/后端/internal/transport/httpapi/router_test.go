package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGinRoutingContract(t *testing.T) {
	m := newRouter()
	m.HandleFunc("GET /api/v1/products/{id}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"id": r.PathValue("id")})
	})
	m.HandleFunc("POST /api/v1/products/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) })
	for _, tt := range []struct {
		method, path string
		status       int
		contains     string
	}{
		{"GET", "/api/v1/products/item+one", 200, "item+one"},
		{"GET", "/api/v1/products/a%2Fb", 200, "a/b"},
		{"GET", "/api/v1/products/%25", 200, "%"},
		{"GET", "/api/v1/products/%252F", 200, "%2F"},
		{"GET", "/api/v1/products/%E6%B8%85%E5%8F%B6", 200, "清叶"},
		{"HEAD", "/api/v1/products/a", 200, ""},
		{"DELETE", "/api/v1/products/a", 405, "METHOD_NOT_ALLOWED"},
		{"POST", "/api/v1/products/a", 201, ""},
		{"GET", "/api/v1/unknown", 404, "NOT_FOUND"},
		{"GET", "/api/v1/products/a/", 404, "NOT_FOUND"},
	} {
		t.Run(tt.method+tt.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			m.ServeHTTP(w, httptest.NewRequest(tt.method, tt.path, nil))
			if w.Code != tt.status || !strings.Contains(w.Body.String(), tt.contains) {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Location") != "" {
				t.Fatal("unexpected route redirect")
			}
		})
	}
}

func TestJSONContractRejectsAmbiguousInput(t *testing.T) {
	for _, body := range []string{`{"value":"ok","role":"admin"}`, `{"value":"ok"}{}`, `{"value":"` + strings.Repeat("x", 1<<20) + `"}`} {
		var dst struct {
			Value string `json:"value"`
		}
		w := httptest.NewRecorder()
		if readJSON(w, httptest.NewRequest("POST", "/", strings.NewReader(body)), &dst) || w.Code != 422 {
			t.Fatal("ambiguous/oversized body accepted")
		}
	}
}

func TestAdmissionLimitsReleaseCapacityAndExcludeChat(t *testing.T) {
	a := &App{Config: Config{MaxConcurrentRequests: 1, RequestTimeout: time.Second}}
	entered, release := make(chan struct{}), make(chan struct{})
	h := a.requestLimits(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/products" {
			close(entered)
			<-release
		}
		w.WriteHeader(200)
	}))
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/products", nil))
	}()
	<-entered
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/categories", nil))
	if w.Code != 503 {
		t.Fatal("overload did not fail fast")
	}
	for _, path := range []string{"/healthz", "/api/v1/chat/socket", "/api/v1/admin/chat/socket"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatal("long-lived transport shared HTTP slots")
		}
	}
	close(release)
	<-done
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/categories", nil))
	if w.Code != 200 {
		t.Fatal("capacity leaked")
	}
}

func TestRequestDeadlineDoesNotExpireStreaming(t *testing.T) {
	a := &App{Config: Config{RequestTimeout: time.Second}}
	for _, path := range []string{"/api/v1/products", "/api/v1/chat/socket", "/api/v1/admin/chat/socket", "/api/v1/media/id/file", "/api/v1/uploads/id/content"} {
		a.requestLimits(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			at, deadline := r.Context().Deadline()
			stream := strings.HasSuffix(path, "/socket")
			if deadline == stream {
				t.Fatalf("incorrect deadline for %s", path)
			}
			if strings.Contains(path, "/media/") || strings.Contains(path, "/uploads/") {
				if time.Until(at) < 55*time.Second {
					t.Fatal("media budget truncated")
				}
			}
		})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.requestLimits(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != context.Canceled {
			t.Fatal("caller cancellation lost")
		}
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/products", nil).WithContext(ctx))
}

func TestProxyIPCannotBeSpoofed(t *testing.T) {
	for _, tt := range []struct{ peer, forwarded, want string }{{"203.0.113.1:9000", "127.0.0.1", "203.0.113.1"}, {"127.0.0.1:9000", "198.51.100.2", "198.51.100.2"}, {"127.0.0.1:9000", "198.51.100.2, 127.0.0.1", "127.0.0.1"}} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tt.peer
		r.Header.Set("X-Forwarded-For", tt.forwarded)
		if got := requestIP(r); got != tt.want {
			t.Fatalf("got %s want %s", got, tt.want)
		}
	}
}
