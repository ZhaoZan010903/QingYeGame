package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	configuration "qingye/backend/internal/config"
	"qingye/backend/internal/infrastructure/security"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Config = configuration.Config
type App struct {
	DB           *pgxpool.Pool
	Redis        *redis.Client
	Config       Config
	chatMu       sync.Mutex
	chatClosing  bool
	chatSessions sync.WaitGroup
}

func (a *App) Routes() http.Handler {
	mux := newRouter()
	if a.surface() == "admin" {
		a.registerAdmin(mux)
		a.registerAdminImages(mux)
		mux.HandleFunc("GET /api/v1/publish-form", a.publicPublishForm)
		// Preview reads run behind the same administrator gate as management writes.
		mux.HandleFunc("GET /api/v1/categories", a.publicCategories)
		mux.HandleFunc("GET /api/v1/products", a.publicProducts)
		mux.HandleFunc("GET /api/v1/products/{id}", a.publicProduct)
		mux.HandleFunc("GET /api/v1/products/{id}/images", a.publicImages)
		mux.HandleFunc("GET /api/v1/home", a.home)
		mux.HandleFunc("GET /api/v1/contacts", a.publicContacts)
		mux.HandleFunc("GET /api/v1/pages/{slug}", a.publicPage)
	} else {
		a.registerPublic(mux)
	}
	a.registerAuthMember(mux)
	a.registerChat(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		db := a.DB.Ping(ctx) == nil
		cache := a.Redis.Ping(ctx).Err() == nil
		status := 200
		if !db || !cache {
			status = 503
		}
		writeJSON(w, status, map[string]any{"status": map[bool]string{true: "ok", false: "unavailable"}[db && cache], "database": db, "redis": cache, "environment": "integration", "surface": a.surface()})
	})
	var handler http.Handler = mux
	if a.surface() == "admin" {
		handler = a.adminSurfaceGate(handler)
	}
	return a.middleware(a.requestLimits(handler))
}
func (a *App) surface() string {
	if a.Config.Surface == "admin" {
		return "admin"
	}
	return "public"
}
func (a *App) adminSurfaceGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/login" || (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := a.requireAdmin(w, r); !ok {
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (a *App) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		origin := r.Header.Get("Origin")
		allowed := false
		for _, v := range a.Config.AllowedOrigins {
			if strings.TrimSpace(v) == origin {
				allowed = true
			}
		}
		if origin != "" {
			if !allowed {
				fail(w, 403, "ORIGIN_REJECTED", "请求来源不允许")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == "OPTIONS" {
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type,X-CSRF-Token")
			w.WriteHeader(204)
			return
		}
		// Browser mutations require a trusted origin. CLI requests without an Origin are allowed;
		// cross-site browser forms and fetches always supply Origin or Sec-Fetch-Site.
		if r.Method != "GET" && r.Method != "HEAD" && (r.Header.Get("Sec-Fetch-Site") == "cross-site" || (r.Header.Get("Sec-Fetch-Site") != "" && origin == "")) {
			fail(w, 403, "CSRF_REJECTED", "请求来源无法验证")
			return
		}
		defer func() {
			if recover() != nil {
				slog.Error("request panic", "path", r.URL.Path)
				fail(w, 500, "INTERNAL_ERROR", "服务暂时不可用")
			}
		}()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityCacheKey{}, &identityCache{})))
	})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"code": code, "message": message})
}
func readJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(target); e != nil {
		fail(w, 422, "INVALID_BODY", "请求字段或格式无效")
		return false
	}
	var rest any
	if d.Decode(&rest) != io.EOF {
		fail(w, 422, "INVALID_BODY", "只允许一个JSON对象")
		return false
	}
	return true
}
func newID() string { return security.NewID() }
func (a *App) dbError(w http.ResponseWriter, e error) {
	slog.Error("database operation failed", "error", e)
	fail(w, 503, "DATABASE_UNAVAILABLE", "数据暂时不可用，请稍后重试")
}
