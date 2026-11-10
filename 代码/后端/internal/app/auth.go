package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "qingye_session"
const adminSessionCookie = "qingye_admin_session"

// A valid fixed cost-12 hash, matching SeedUser. Its preimage is test-only and
// is never an account password; missing accounts are rejected independently.
const dummyPasswordHash = "$2a$12$TlUW/qGz0irw3rm5.GkCDeUp7g/NPfVdsplL993IqESbWNVDbtelK"

func (a *App) cookieName() string {
	if a.surface() == "admin" {
		return adminSessionCookie
	}
	return sessionCookie
}
func (a *App) sessionRole() string {
	if a.surface() == "admin" {
		return "admin"
	}
	return "member"
}

type User struct {
	ID, Phone, Nickname, Role string
	Enabled                   bool
}

func tokenHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// The dedicated nginx instance overwrites X-Forwarded-For. Only a loopback
// socket peer may supply this single-IP value; direct clients cannot spoof it.
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
func (a *App) currentUser(r *http.Request) (*User, error) {
	cookie, err := r.Cookie(a.cookieName())
	if err != nil || len(cookie.Value) != 64 {
		return nil, pgx.ErrNoRows
	}
	var u User
	err = a.DB.QueryRow(r.Context(), `SELECT u.id,u.phone,u.nickname,u.role,u.enabled FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.audience=$2 AND s.expires_at>now() AND u.enabled AND u.role=$3`, tokenHash(cookie.Value), a.surface(), a.sessionRole()).Scan(&u.ID, &u.Phone, &u.Nickname, &u.Role, &u.Enabled)
	if err != nil {
		return nil, err
	}
	return &u, nil
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

// Browser writes require an exact permitted origin; no wildcard or suffix matching.
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

func (a *App) registerAuthMember(mux *http.ServeMux) {
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
	if input.Method != "" && input.Method != "password" {
		fail(w, 503, "SMS_NOT_CONFIGURED", "短信服务尚未配置")
		return
	}
	input.Phone = strings.TrimSpace(input.Phone)
	if len(input.Phone) > 32 || len(input.Password) > 72 || len(input.Password) < 1 {
		fail(w, 401, "INVALID_CREDENTIALS", "手机号或密码不正确")
		return
	}
	ip := requestIP(r)
	if !a.memberLimit(w, r, "login-ip:"+a.surface()+":"+tokenHash(ip), 30, 15*time.Minute) || !a.memberLimit(w, r, "login-phone:"+a.surface()+":"+tokenHash(input.Phone), 12, 15*time.Minute) {
		return
	}
	var u User
	var hash string
	err := a.DB.QueryRow(r.Context(), `SELECT id,phone,nickname,role,enabled,password_hash FROM users WHERE phone=$1`, strings.TrimSpace(input.Phone)).Scan(&u.ID, &u.Phone, &u.Nickname, &u.Role, &u.Enabled, &hash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		fail(w, 503, "DATABASE_UNAVAILABLE", "登录服务暂不可用")
		return
	}
	// Valid bcrypt value makes an unknown account take a comparable password-check path.
	if hash == "" {
		hash = dummyPasswordHash
	}
	passwordErr := bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password))
	if err != nil || passwordErr != nil || !u.Enabled || u.Role != a.sessionRole() {
		fail(w, 401, "INVALID_CREDENTIALS", "手机号或密码不正确")
		return
	}
	token := randomToken()
	expires := time.Now().Add(7 * 24 * time.Hour)
	if _, err = a.DB.Exec(r.Context(), `INSERT INTO sessions(token_hash,user_id,expires_at,audience) VALUES($1,$2,$3,$4)`, tokenHash(token), u.ID, expires, a.surface()); err != nil {
		fail(w, 503, "SESSION_UNAVAILABLE", "登录服务暂不可用")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(), Value: token, Path: "/", HttpOnly: true, Secure: a.Config.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: 7 * 86400, Expires: expires})
	writeJSON(w, 200, map[string]any{"data": userWire(&u)})
}
func (a *App) memberLimit(w http.ResponseWriter, r *http.Request, key string, max int, period time.Duration) bool {
	if a.Redis == nil {
		fail(w, 503, "RATE_LIMIT_UNAVAILABLE", "请求限流服务暂不可用")
		return false
	}
	value, err := a.Redis.Eval(r.Context(), `local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('EXPIRE',KEYS[1],ARGV[1]) end; return n`, []string{"qingye:limit:" + key}, int(period.Seconds())).Int()
	if err != nil {
		fail(w, 503, "RATE_LIMIT_UNAVAILABLE", "请求限流服务暂不可用")
		return false
	}
	if value > max {
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "RATE_LIMITED", "操作频繁，请稍后重试")
		return false
	}
	return true
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(a.cookieName()); err == nil {
		if _, err = a.DB.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash=$1 AND audience=$2`, tokenHash(cookie.Value), a.surface()); err != nil {
			fail(w, 503, "DATABASE_UNAVAILABLE", "退出失败，请重试")
			return
		}
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
	input.Nickname = strings.TrimSpace(input.Nickname)
	if n := utf8.RuneCountInString(input.Nickname); n < 1 || n > 30 {
		fail(w, 422, "INVALID_NICKNAME", "昵称需为1至30个字符")
		return
	}
	if _, err := a.DB.Exec(r.Context(), `UPDATE users SET nickname=$1,updated_at=now() WHERE id=$2`, input.Nickname, u.ID); err != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE", "保存失败")
		return
	}
	u.Nickname = input.Nickname
	writeJSON(w, 200, map[string]any{"data": userWire(u)})
}
