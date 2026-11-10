package app

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

//go:embed schema.sql
var schema string

type Config struct {
	Addr, DatabaseURL, RedisURL, MediaDir, PublicBaseURL, SessionSecret string
	Surface                                                             string
	AllowedOrigins                                                      []string
	SecureCookies                                                       bool
	MaxUploadBytes                                                      int64
}
type App struct {
	DB     *pgxpool.Pool
	Redis  *redis.Client
	Config Config
}

func EnvConfig() Config {
	surface := env("APP_SURFACE", "public")
	addr, origin := "127.0.0.1:5180", "http://127.0.0.1:5179"
	if surface == "admin" {
		addr, origin = "127.0.0.1:5182", "http://127.0.0.1:5181"
	}
	c := Config{Surface: surface, Addr: env("QINGYE_ADDR", env("HTTP_ADDR", addr)), DatabaseURL: os.Getenv("DATABASE_URL"), RedisURL: env("REDIS_URL", "redis://127.0.0.1:6381/0"), MediaDir: env("MEDIA_DIR", "./data/media"), PublicBaseURL: os.Getenv("PUBLIC_BASE_URL"), SessionSecret: os.Getenv("SESSION_SECRET"), AllowedOrigins: strings.Split(env("ALLOWED_ORIGINS", origin), ","), SecureCookies: env("SECURE_COOKIES", os.Getenv("COOKIE_SECURE")) == "true", MaxUploadBytes: 10 << 20}
	if n, e := strconv.ParseInt(env("MAX_UPLOAD_BYTES", os.Getenv("UPLOAD_MAX_BYTES")), 10, 64); e == nil && n > 0 {
		c.MaxUploadBytes = n
	}
	return c
}
func env(k, f string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return f
}
func New(ctx context.Context, c Config) (*App, error) {
	if c.Surface == "" {
		c.Surface = "public"
	}
	if c.Surface != "public" && c.Surface != "admin" {
		return nil, errors.New("APP_SURFACE must be public or admin")
	}
	if c.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	if len(c.SessionSecret) < 32 {
		return nil, errors.New("SESSION_SECRET must contain at least 32 characters")
	}
	cfg, e := pgxpool.ParseConfig(c.DatabaseURL)
	if e != nil {
		return nil, e
	}
	cfg.MaxConns = 5
	cfg.MinConns = 0
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	db, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		return nil, e
	}
	if e = db.Ping(ctx); e != nil {
		db.Close()
		return nil, e
	}
	ro, e := redis.ParseURL(c.RedisURL)
	if e != nil {
		db.Close()
		return nil, e
	}
	ro.DialTimeout = time.Second
	ro.ReadTimeout = time.Second
	ro.WriteTimeout = time.Second
	ro.PoolSize = 5
	return &App{DB: db, Redis: redis.NewClient(ro), Config: c}, nil
}
func (a *App) Close() { a.DB.Close(); a.Redis.Close() }
func (a *App) Migrate(ctx context.Context) error {
	tx, e := a.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(1834179)"); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, schema); e != nil {
		return e
	}
	if e = applyDataMigrations(ctx, tx); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	if a.surface() == "admin" {
		a.registerAdmin(mux)
		a.registerAdminImages(mux)
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
	return a.middleware(handler)
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
		next.ServeHTTP(w, r)
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
func newID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", s[:8], s[8:12], s[12:16], s[16:20], s[20:])
}
func (a *App) dbError(w http.ResponseWriter, e error) {
	slog.Error("database operation failed", "error", e)
	fail(w, 503, "DATABASE_UNAVAILABLE", "数据暂时不可用，请稍后重试")
}
