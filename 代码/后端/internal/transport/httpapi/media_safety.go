package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/url"
	domain "qingye/backend/internal/domain/security"
	"qingye/backend/internal/infrastructure/cache"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (a *App) registerMediaSafety(m routeRegistrar) {
	m.HandleFunc("GET /api/v1/security/media-access", a.mediaAccessStatus)
	m.HandleFunc("POST /api/v1/security/challenges", a.createMediaChallenge)
	m.HandleFunc("POST /api/v1/security/challenges/verify", a.verifyMediaChallenge)
	if a.surface() == "admin" {
		m.HandleFunc("GET /api/v1/admin/security/media-access", a.adminOnly(a.mediaAccessRecords))
		m.HandleFunc("PUT /api/v1/admin/security/media-access/{id}", a.adminOnly(a.controlMediaAccess))
	}
}
func (a *App) mediaMAC(value string) string {
	mac := hmac.New(sha256.New, []byte(a.Config.SessionSecret))
	mac.Write([]byte("media-safety-v1:" + value))
	return hex.EncodeToString(mac.Sum(nil))
}
func (a *App) mediaSubject(w http.ResponseWriter, r *http.Request) (domain.ReadSubject, error) {
	surface := a.surface()
	name := "qingye_media_" + surface
	anon := ""
	if c, e := r.Cookie(name); e == nil && len(c.Value) < 200 {
		p := strings.Split(c.Value, ".")
		if len(p) == 3 && len(p[0]) == 64 {
			ts, e := strconv.ParseInt(p[1], 10, 64)
			if e == nil && ts <= time.Now().Unix()+30 && ts > time.Now().Add(-24*time.Hour).Unix() && hmac.Equal([]byte(p[2]), []byte(a.mediaMAC(surface+":"+p[0]+"."+p[1]))) {
				anon = p[0]
			}
		}
	}
	if anon == "" {
		anon = randomToken()
		v := anon + "." + strconv.FormatInt(time.Now().Unix(), 10)
		http.SetCookie(w, &http.Cookie{Name: name, Value: v + "." + a.mediaMAC(surface+":"+v), Path: "/api/v1/", MaxAge: 86400, HttpOnly: true, Secure: a.Config.SecureCookies, SameSite: http.SameSiteLaxMode})
	}
	identity := "anonymous:" + anon
	kind := "anonymous"
	if a.requestSessionHash(r) != "" {
		u, e := a.currentUser(r)
		if e == nil {
			identity = "account:" + u.ID
			kind = "member"
			if surface == "admin" {
				kind = "admin"
			}
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return domain.ReadSubject{}, domain.ErrUnavailable
		}
	}
	return domain.ReadSubject{Key: a.mediaMAC(surface + ":" + identity), Binding: a.mediaMAC(surface + ":" + identity + ":" + anon + ":" + a.requestSessionHash(r)), IPKey: a.mediaMAC("ip:" + requestIP(r)), Surface: surface, Kind: kind}, nil
}
func (a *App) safetyReady(w http.ResponseWriter) bool {
	if a.ReadSafety == nil || a.HumanSafety == nil {
		fail(w, 503, "MEDIA_PROTECTION_UNAVAILABLE", "安全验证暂不可用，请稍后重试")
		return false
	}
	return true
}
func (a *App) mediaAccessStatus(w http.ResponseWriter, r *http.Request) {
	if !a.Config.MediaSafety.Enabled {
		writeJSON(w, 200, map[string]any{"enabled": false, "state": "allowed"})
		return
	}
	if !a.safetyReady(w) {
		return
	}
	who, err := a.mediaSubject(w, r)
	if err != nil {
		a.mediaSafetyError(w, err)
		return
	}
	if err = (cache.Limiter{Client: a.Redis}).Allow(r.Context(), "media-status:"+who.IPKey, 1200, time.Minute); err != nil {
		writeAppError(w, err)
		return
	}
	d, err := a.ReadSafety.Store.Status(r.Context(), who)
	if err != nil {
		a.mediaSafetyError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": true, "site_key": a.Config.MediaSafety.SiteKey, "action": domain.MediaReadAction, "state": d.State, "code": d.Code, "retry_after": d.RetryAfter, "until": d.Until})
}
func (a *App) mediaChallengeOrigin(w http.ResponseWriter, r *http.Request) (string, bool) {
	origin := r.Header.Get("Origin")
	u, e := url.Parse(origin)
	if e != nil || origin == "" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		fail(w, 403, "ORIGIN_REQUIRED", "请从当前网站重新操作")
		return "", false
	}
	allowed := false
	for _, o := range a.Config.AllowedOrigins {
		if o == origin {
			allowed = true
		}
	}
	hostname := false
	for _, h := range a.Config.MediaSafety.Hostnames {
		if h == u.Hostname() {
			hostname = true
		}
	}
	if !allowed || !hostname {
		fail(w, 403, "ORIGIN_REJECTED", "请求来源不允许")
		return "", false
	}
	return u.Hostname(), true
}
func (a *App) challengeSubject(w http.ResponseWriter, r *http.Request) (domain.ReadSubject, string, bool) {
	if !a.safetyReady(w) {
		return domain.ReadSubject{}, "", false
	}
	host, ok := a.mediaChallengeOrigin(w, r)
	if !ok {
		return domain.ReadSubject{}, "", false
	}
	who, err := a.mediaSubject(w, r)
	if err != nil {
		a.mediaSafetyError(w, err)
		return who, "", false
	}
	limiter := cache.Limiter{Client: a.Redis}
	if err = limiter.Allow(r.Context(), "human-ip:"+who.IPKey, 60, time.Minute); err == nil {
		err = limiter.Allow(r.Context(), "human-subject:"+who.Key, 12, time.Minute)
	}
	if err != nil {
		writeAppError(w, err)
		return who, "", false
	}
	return who, host, true
}
func (a *App) createMediaChallenge(w http.ResponseWriter, r *http.Request) {
	who, host, ok := a.challengeSubject(w, r)
	if !ok {
		return
	}
	var in struct{}
	if !readJSON(w, r, &in) {
		return
	}
	c, err := a.HumanSafety.Create(r.Context(), who, host)
	if err != nil {
		a.mediaSafetyError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"data": c, "site_key": a.Config.MediaSafety.SiteKey})
}
func (a *App) verifyMediaChallenge(w http.ResponseWriter, r *http.Request) {
	who, _, ok := a.challengeSubject(w, r)
	if !ok {
		return
	}
	var in struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if !readJSON(w, r, &in) {
		return
	}
	if err := a.HumanSafety.Verify(r.Context(), who, in.ID, in.Token, requestIP(r)); err != nil {
		a.mediaSafetyError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": map[string]bool{"verified": true}})
}
func (a *App) mediaSafetyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrChallenge):
		fail(w, 422, "VERIFICATION_INVALID", "验证已过期或无效，请重新验证")
	case errors.Is(err, domain.ErrBusy):
		w.Header().Set("Retry-After", "2")
		fail(w, 409, "VERIFICATION_BUSY", "正在确认验证结果，请稍候")
	default:
		w.Header().Set("Retry-After", "5")
		fail(w, 503, "MEDIA_PROTECTION_UNAVAILABLE", "图片服务暂不可用，请稍后重试")
	}
}
func writeReadDecision(w http.ResponseWriter, d domain.ReadDecision) {
	w.Header().Set("Retry-After", strconv.Itoa(max(1, d.RetryAfter)))
	status := 429
	message := "图片请求较多，请稍后重试"
	if d.State == "challenge" {
		status = 403
		message = "请完成安全验证后继续查看图片"
	} else if d.State == "restricted" {
		message = "图片访问暂时受限，请稍后重试或联系客服"
	}
	writeJSON(w, status, map[string]any{"code": d.Code, "message": message, "retry_after": d.RetryAfter})
}
func (a *App) mediaAccessRecords(w http.ResponseWriter, r *http.Request) {
	if !a.safetyReady(w) {
		return
	}
	records, metrics, err := a.ReadSafety.Store.Records(r.Context())
	if err != nil {
		a.mediaSafetyError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": map[string]any{"items": records, "metrics": metrics, "coverage": "同源媒体入口；R20对象经受保护代理读取", "retention": "最近活跃访问主体，最多200条；运行计数有时效", "policy": a.Config.MediaSafety.Policy}})
}
func (a *App) controlMediaAccess(w http.ResponseWriter, r *http.Request) {
	if !a.safetyReady(w) {
		return
	}
	u, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var in struct {
		Version string `json:"expected_version"`
		Seconds int    `json:"duration_seconds"`
		Reason  string `json:"reason"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	_, e := hex.DecodeString(id)
	if len(id) != 64 || e != nil || len(in.Version) > 20 || in.Version == "" || (in.Seconds != 0 && in.Seconds != 600) || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 2 || utf8.RuneCountInString(in.Reason) > 160 {
		fail(w, 422, "INVALID_RESTRICTION", "请填写处置原因并刷新列表后操作")
		return
	}
	if err := a.ReadSafety.Control(r.Context(), u.ID, id, in.Version, in.Seconds, in.Reason); err != nil {
		fail(w, 409, "CONTROL_NOT_CONFIRMED", "状态可能已变化，请刷新后核对；操作已留档")
		return
	}
	writeJSON(w, 200, map[string]any{"data": map[string]bool{"ok": true}})
}

type measuredMediaWriter struct {
	http.ResponseWriter
	Context context.Context
	Bytes   int64
}

func (w *measuredMediaWriter) Write(p []byte) (int, error) {
	if err := w.Context.Err(); err != nil {
		return 0, err
	}
	n, e := w.ResponseWriter.Write(p)
	w.Bytes += int64(n)
	return n, e
}
func (w *measuredMediaWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
