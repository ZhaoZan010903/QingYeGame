package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strings"
	"time"
)

func (a *App) registerPublic(m routeRegistrar) {
	m.HandleFunc("GET /api/v1/dictionaries", a.publicDictionaries)
	m.HandleFunc("GET /api/v1/publish-form", a.publicPublishForm)
	m.HandleFunc("GET /api/v1/categories", a.publicCategories)
	m.HandleFunc("GET /api/v1/products", a.publicProducts)
	m.HandleFunc("GET /api/v1/products/{id}", a.publicProduct)
	m.HandleFunc("GET /api/v1/products/{id}/images", a.publicImages)
	m.HandleFunc("POST /api/v1/products/{id}/view", a.memberMutation(a.recordView))
	m.HandleFunc("GET /api/v1/home", a.home)
	m.HandleFunc("GET /api/v1/recommendations/profile", a.getInterestProfile)
	m.HandleFunc("PATCH /api/v1/recommendations/profile", a.memberMutation(a.updateInterestProfile))
	m.HandleFunc("GET /api/v1/contacts", a.publicContacts)
	m.HandleFunc("GET /api/v1/pages/{slug}", a.publicPage)
}

func (a *App) publicCategories(w http.ResponseWriter, r *http.Request) {
	rows, e := a.catalogService().Categories(r.Context())
	if e != nil {
		writeAppError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows})
}
func (a *App) publicContacts(w http.ResponseWriter, r *http.Request) {
	rows, e := a.catalogService().Contacts(r.Context(), r.URL.Query().Get("purpose"))
	if e != nil {
		writeAppError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows})
}
func (a *App) publicPage(w http.ResponseWriter, r *http.Request) {
	row, e := a.catalogService().ContentPage(r.Context(), r.PathValue("slug"))
	if e != nil {
		writeAppError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": row})
}
func (a *App) home(w http.ResponseWriter, r *http.Request) {
	id, e := a.recommendationIdentity(r)
	if e != nil {
		a.dbError(w, e)
		return
	}
	out, e := a.catalogService().Home(r.Context(), id)
	if e != nil {
		writeAppError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": out})
}
func (a *App) visitorSignature(id string) string {
	h := hmac.New(sha256.New, []byte(a.Config.SessionSecret))
	h.Write([]byte(id))
	return hex.EncodeToString(h.Sum(nil))
}
func (a *App) recordView(w http.ResponseWriter, r *http.Request) {
	if a.Config.Surface == "admin" {
		writeJSON(w, 200, map[string]any{"data": map[string]bool{"recorded": false}})
		return
	}
	ip := requestIP(r)
	if !a.memberLimit(w, r, "view:"+tokenHash(ip), 120, time.Minute) {
		return
	}
	id := r.PathValue("id")
	user, userErr := a.currentUser(r)
	if userErr != nil && !errors.Is(userErr, pgx.ErrNoRows) {
		a.dbError(w, userErr)
		return
	}
	member := ""
	if user != nil {
		member = user.ID
	}
	eligible, e := a.catalogService().ViewEligible(r.Context(), id, member)
	if e != nil {
		writeAppError(w, e)
		return
	}
	if !eligible {
		writeJSON(w, 200, map[string]any{"data": map[string]bool{"recorded": false}})
		return
	}
	// Signed server-issued visitor identity: forged cookie values do not create views.
	visitor := ""
	if user != nil {
		visitor = "member:" + user.ID
	}
	if c, e := r.Cookie("qy_visitor"); user == nil && e == nil {
		parts := strings.Split(c.Value, ".")
		if len(parts) == 2 && hmac.Equal([]byte(parts[1]), []byte(a.visitorSignature(parts[0]))) {
			visitor = parts[0]
		}
	}
	if visitor == "" {
		visitor = newID()
		http.SetCookie(w, &http.Cookie{Name: "qy_visitor", Value: visitor + "." + a.visitorSignature(visitor), Path: "/", MaxAge: 31536000, HttpOnly: true, Secure: a.Config.SecureCookies, SameSite: http.SameSiteLaxMode})
	}
	hash := sha256.Sum256([]byte(visitor))
	recorded, e := a.catalogService().RecordView(r.Context(), id, hex.EncodeToString(hash[:]))
	if e != nil {
		a.dbError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": map[string]bool{"recorded": recorded}})
}

func (a *App) publicPublishForm(w http.ResponseWriter, r *http.Request) {
	data, err := a.catalogService().PublishForm(r.Context())
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": data})
}
