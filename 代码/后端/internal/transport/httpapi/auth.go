package httpapi

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"net"
	"net/http"
	"net/url"
	application "qingye/backend/internal/application/identity"
	domain "qingye/backend/internal/domain/identity"
	"qingye/backend/internal/infrastructure/cache"
	"qingye/backend/internal/infrastructure/postgres"
	"qingye/backend/internal/infrastructure/security"
	"strings"
	"sync"
	"time"
)

const sessionCookie = "qingye_session"
const adminSessionCookie = "qingye_admin_session"
const dummyPasswordHash = application.DummyPasswordHash

type User = domain.User

var loginVerifier = security.NewPasswordVerifier(2)

func (a *App) identityService() *application.Service {
	return application.New(postgres.IdentityStore{DB: a.DB}, cache.Limiter{Client: a.Redis}, loginVerifier, a.surface(), randomToken, tokenHash)
}
func (a *App) cookieName() string {
	if a.surface() == "admin" {
		return adminSessionCookie
	}
	return sessionCookie
}
func (a *App) sessionRole() string  { return domain.RoleFor(a.surface()) }
func tokenHash(value string) string { return security.TokenHash(value) }
func randomToken() string           { return security.RandomToken() }

// Nginx覆盖转发头；只信任回环连接提供的单一IP，拒绝客户端伪造链。
func requestIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if peer := net.ParseIP(ip); peer != nil && peer.IsLoopback() {
		if forwarded := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Forwarded-For"))); forwarded != nil {
			return forwarded.String()
		}
	}
	return ip
}
func userWire(u *User) map[string]any {
	phone := u.Phone
	if len(phone) == 11 {
		phone = phone[:3] + "****" + phone[7:]
	}
	return map[string]any{"id": u.ID, "nickname": u.Nickname, "phone_masked": phone, "role": u.Role, "enabled": u.Enabled}
}

type identityCacheKey struct{}
type identityCache struct {
	once sync.Once
	user *User
	err  error
}

// 缓存只活在单次HTTP请求中，减少管理门禁和处理器重复查库。
func (a *App) currentUser(r *http.Request) (*User, error) {
	if cached, ok := r.Context().Value(identityCacheKey{}).(*identityCache); ok {
		cached.once.Do(func() { cached.user, cached.err = a.currentUserBySession(r.Context(), a.requestSessionHash(r)) })
		return cached.user, cached.err
	}
	return a.currentUserBySession(r.Context(), a.requestSessionHash(r))
}
func (a *App) currentUserBySession(ctx context.Context, hash string) (*User, error) {
	u, err := a.identityService().Current(ctx, hash)
	if errors.Is(err, application.ErrNotFound) {
		return nil, pgx.ErrNoRows
	}
	return u, err
}

// 仅服务端票据交换能注入，外部请求不能指定此上下文键。
type chatSessionKey struct{}

func (a *App) requestSessionHash(r *http.Request) string {
	if hash, ok := r.Context().Value(chatSessionKey{}).(string); ok {
		return hash
	}
	cookie, err := r.Cookie(a.cookieName())
	if err != nil || len(cookie.Value) != 64 {
		return ""
	}
	return tokenHash(cookie.Value)
}
func (a *App) requireUser(w http.ResponseWriter, r *http.Request) (*User, bool) {
	u, err := a.currentUser(r)
	if err == nil {
		return u, true
	}
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 401, "UNAUTHENTICATED", "请先登录")
	} else {
		fail(w, 503, "SESSION_UNAVAILABLE", "会话服务暂不可用")
	}
	return nil, false
}
func (a *App) requireAdmin(w http.ResponseWriter, r *http.Request) (*User, bool) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return nil, false
	}
	if u.Role != "admin" {
		fail(w, 403, "FORBIDDEN", "需要管理员权限")
		return nil, false
	}
	return u, true
}

func (a *App) memberMutation(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" && r.Referer() != "" {
			if u, e := url.Parse(r.Referer()); e == nil {
				origin = u.Scheme + "://" + u.Host
			}
		}
		if origin != "" {
			allowed := false
			for _, v := range a.Config.AllowedOrigins {
				if origin == strings.TrimRight(v, "/") {
					allowed = true
					break
				}
			}
			if !allowed {
				fail(w, 403, "ORIGIN_REJECTED", "请求来源不允许")
				return
			}
		} else if _, e := r.Cookie(a.cookieName()); e == nil {
			fail(w, 403, "ORIGIN_REQUIRED", "缺少请求来源")
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			fail(w, 403, "ORIGIN_REJECTED", "不允许跨站写入")
			return
		}
		next(w, r)
	}
}

func (a *App) registerAuthMember(mux routeRegistrar) {
	mux.HandleFunc("POST /api/v1/auth/login", a.memberMutation(a.login))
	mux.HandleFunc("POST /api/v1/auth/logout", a.memberMutation(a.logout))
	if a.surface() == "public" {
		unavailable := func(w http.ResponseWriter, r *http.Request) {
			fail(w, 503, "SMS_NOT_CONFIGURED", "短信服务尚未配置，请使用已创建账户的密码登录")
		}
		for _, path := range []string{"sms", "register", "password-reset"} {
			mux.HandleFunc("POST /api/v1/auth/"+path, a.memberMutation(unavailable))
		}
	}
	mux.HandleFunc("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		if u, ok := a.requireUser(w, r); ok {
			writeJSON(w, 200, map[string]any{"data": userWire(u)})
		}
	})
	if a.surface() == "public" {
		mux.HandleFunc("PATCH /api/v1/me", a.memberMutation(a.updateProfile))
		a.registerMember(mux)
	}
	a.registerMedia(mux)
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Phone       string `json:"phone"`
		Password    string `json:"password"`
		Method      string `json:"method"`
		Code        string `json:"code"`
		ChallengeID string `json:"challenge_id"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	session, err := a.identityService().Login(r.Context(), application.LoginInput{Phone: input.Phone, Password: input.Password, Method: input.Method, IP: requestIP(r)})
	if err != nil {
		writeAppError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(), Value: session.Token, Path: "/", HttpOnly: true, Secure: a.Config.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: 7 * 86400, Expires: session.Expires})
	writeJSON(w, 200, map[string]any{"data": userWire(&session.User)})
}
func (a *App) memberLimit(w http.ResponseWriter, r *http.Request, key string, max int, period time.Duration) bool {
	if err := (cache.Limiter{Client: a.Redis}).Allow(r.Context(), key, max, period); err != nil {
		writeAppError(w, err)
		return false
	}
	return true
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	var hash string
	if cookie, err := r.Cookie(a.cookieName()); err == nil {
		hash = tokenHash(cookie.Value)
	}
	if err := a.identityService().Logout(r.Context(), hash); err != nil {
		writeAppError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(), Value: "", Path: "/", HttpOnly: true, Secure: a.Config.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	writeJSON(w, 200, map[string]any{"data": map[string]any{"ok": true}})
}
func (a *App) updateProfile(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	var input struct {
		Nickname string `json:"nickname"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	updated, err := a.identityService().UpdateNickname(r.Context(), *u, input.Nickname)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": userWire(&updated)})
}
