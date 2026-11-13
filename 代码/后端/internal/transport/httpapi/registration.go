package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	domain "qingye/backend/internal/domain/registration"
	"strconv"
	"strings"
	"time"
)

func (a *App) registrationMAC(value string) string {
	mac := hmac.New(sha256.New, []byte(a.Config.SessionSecret))
	mac.Write([]byte("registration-v1:" + value))
	return hex.EncodeToString(mac.Sum(nil))
}
func (a *App) registrationSubject(w http.ResponseWriter, r *http.Request, phone string, create bool) (domain.Subject, bool) {
	if a.Registration == nil {
		fail(w, 503, "SMS_NOT_CONFIGURED", "注册短信暂未启用，请稍后再试")
		return domain.Subject{}, false
	}
	host, ok := a.mediaChallengeOrigin(w, r)
	if !ok {
		return domain.Subject{}, false
	}
	if !domain.ValidPhone(phone) {
		fail(w, 400, "INVALID_PHONE", "请输入有效的11位手机号")
		return domain.Subject{}, false
	}
	const name = "qingye_registration"
	value := ""
	if cookie, err := r.Cookie(name); err == nil && len(cookie.Value) < 200 {
		parts := strings.Split(cookie.Value, ".")
		if len(parts) == 3 && len(parts[0]) == 64 {
			ts, e := strconv.ParseInt(parts[1], 10, 64)
			if e == nil && ts <= time.Now().Unix()+30 && ts > time.Now().Add(-30*time.Minute).Unix() && hmac.Equal([]byte(parts[2]), []byte(a.registrationMAC("cookie:"+parts[0]+"."+parts[1]))) {
				value = parts[0] + "." + parts[1]
			}
		}
	}
	if value == "" {
		if !create {
			writeAppError(w, domain.ErrChallenge)
			return domain.Subject{}, false
		}
		value = randomToken() + "." + strconv.FormatInt(time.Now().Unix(), 10)
	}
	if create {
		http.SetCookie(w, &http.Cookie{Name: name, Value: value + "." + a.registrationMAC("cookie:"+value), Path: "/api/v1/auth/", HttpOnly: true, Secure: a.Config.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: 1800})
	}
	ip := requestIP(r)
	return domain.Subject{Binding: a.registrationMAC("binding:" + value), IP: ip, IPKey: a.registrationMAC("ip:" + ip), PhoneKey: a.registrationMAC("phone:" + phone), Hostname: host}, true
}
func (a *App) registrationCapabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"data": map[string]any{"registration_sms": a.Registration != nil, "sms_login": false, "password_reset": false}})
}
func (a *App) registrationChallenge(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Phone   string `json:"phone"`
		Purpose string `json:"purpose"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Purpose != "register" {
		fail(w, 400, "SMS_PURPOSE_DISABLED", "此短信功能暂未启用")
		return
	}
	who, ok := a.registrationSubject(w, r, in.Phone, true)
	if !ok {
		return
	}
	c, err := a.Registration.Create(r.Context(), who, in.Phone)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"data": map[string]any{"challenge_id": c.ID, "site_key": a.Config.MediaSafety.SiteKey, "action": domain.Action, "expires_in": 300}})
}
func (a *App) registrationSend(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Phone       string `json:"phone"`
		Purpose     string `json:"purpose"`
		ChallengeID string `json:"challenge_id"`
		Token       string `json:"token"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Purpose != "register" {
		fail(w, 503, "SMS_PURPOSE_DISABLED", "此短信功能暂未启用")
		return
	}
	who, ok := a.registrationSubject(w, r, in.Phone, false)
	if !ok {
		return
	}
	if err := a.Registration.Send(r.Context(), who, in.Phone, in.ChallengeID, in.Token); err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": map[string]any{"challenge_id": in.ChallengeID, "retry_after": 60, "expires_in": 300}})
}
func (a *App) registrationComplete(w http.ResponseWriter, r *http.Request) {
	var in domain.Input
	if !readJSON(w, r, &in) {
		return
	}
	who, ok := a.registrationSubject(w, r, in.Phone, false)
	if !ok {
		return
	}
	session, err := a.Registration.Register(r.Context(), who, in)
	if err != nil {
		writeAppError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(), Value: session.Token, Path: "/", HttpOnly: true, Secure: a.Config.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: 7 * 86400, Expires: session.Expires})
	writeJSON(w, 200, map[string]any{"data": userWire(&session.User)})
}
