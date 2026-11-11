package httpapi

import (
	"crypto/hmac"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"qingye/backend/internal/domain/recommendation"
	"strings"
	"time"
)

type interestCategory = recommendation.Category
type interestProfile = recommendation.Profile

func (a *App) recommendationIdentity(r *http.Request) (string, error) {
	if a.surface() == "admin" {
		return "", nil
	}
	u, e := a.currentUser(r)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return "", e
	}
	if u != nil {
		return tokenHash("member:" + u.ID), nil
	}
	if c, e := r.Cookie("qy_visitor"); e == nil {
		parts := strings.Split(c.Value, ".")
		if len(parts) == 2 && len(parts[0]) == 36 && hmac.Equal([]byte(parts[1]), []byte(a.visitorSignature(parts[0]))) {
			return tokenHash(parts[0]), nil
		}
	}
	return "", nil
}

// This is an interest summary, not an identity inference. Fifty distinct,
// recently viewed visible products bound the profile. Repeat refreshes of one
// product on one day never multiply its weight; a week's age halves its weight.
func (a *App) interestProfile(r *http.Request) (interestProfile, error) {
	id, e := a.recommendationIdentity(r)
	if e != nil {
		return recommendation.NewProfile(), e
	}
	return a.recommendationService().Profile(r.Context(), id)
}

func (a *App) getInterestProfile(w http.ResponseWriter, r *http.Request) {
	p, e := a.interestProfile(r)
	if e != nil {
		a.dbError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": p})
}
func (a *App) updateInterestProfile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
		Reset   bool  `json:"reset"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if !a.memberLimit(w, r, "interest:"+tokenHash(requestIP(r)), 30, time.Minute) {
		return
	}
	id, e := a.recommendationIdentity(r)
	if e != nil {
		a.dbError(w, e)
		return
	}
	if id == "" {
		visitor := newID()
		id = tokenHash(visitor)
		http.SetCookie(w, &http.Cookie{Name: "qy_visitor", Value: visitor + "." + a.visitorSignature(visitor), Path: "/", MaxAge: 31536000, HttpOnly: true, Secure: a.Config.SecureCookies, SameSite: http.SameSiteLaxMode})
	}
	e = a.recommendationService().SavePreference(r.Context(), id, input.Enabled, input.Reset)
	if e != nil {
		a.dbError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": map[string]bool{"ok": true}})
}
