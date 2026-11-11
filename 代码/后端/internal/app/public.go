package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strings"
	"time"
)

func (a *App) registerPublic(m *http.ServeMux) {
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
func (a *App) jsonRows(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	rows, e := a.DB.Query(ctx, sql, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var row map[string]any
		if e = json.Unmarshal(b, &row); e != nil {
			return nil, e
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
func (a *App) publicCategories(w http.ResponseWriter, r *http.Request) {
	rows, e := a.jsonRows(r.Context(), "SELECT to_jsonb(c) FROM categories c WHERE enabled ORDER BY sort_order,id")
	if e != nil {
		a.dbError(w, e)
		return
	}
	for _, row := range rows {
		raw, _ := json.Marshal(row["filter_config"])
		var groups []filterGroup
		_ = json.Unmarshal(raw, &groups)
		active := []filterGroup{}
		for _, g := range groups {
			if g.Enabled != nil && !*g.Enabled {
				continue
			}
			options := []filterOption{}
			for _, o := range g.Options {
				if o.Enabled == nil || *o.Enabled {
					options = append(options, o)
				}
			}
			g.Options = options
			active = append(active, g)
		}
		row["filter_config"] = active
	}
	writeJSON(w, 200, map[string]any{"data": rows})
}
func (a *App) publicContacts(w http.ResponseWriter, r *http.Request) {
	rows, e := a.jsonRows(r.Context(), "SELECT to_jsonb(c)-'qr_media_id' || jsonb_build_object('qr_url',CASE WHEN qr_media_id IS NOT NULL THEN '/api/v1/media/'||qr_media_id||'/file' ELSE qr_url END) FROM contacts c WHERE enabled AND ($1='' OR purpose ? $1 OR purpose ? 'general') ORDER BY id", r.URL.Query().Get("purpose"))
	if e != nil {
		a.dbError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows})
}
func (a *App) publicPage(w http.ResponseWriter, r *http.Request) {
	var b []byte
	e := a.DB.QueryRow(r.Context(), "SELECT to_jsonb(p) FROM content_pages p WHERE slug=$1 AND published", r.PathValue("slug")).Scan(&b)
	if errors.Is(e, pgx.ErrNoRows) {
		fail(w, 404, "NOT_FOUND", "说明页尚未发布")
		return
	}
	if e != nil {
		a.dbError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": json.RawMessage(b)})
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
	var id string
	var ownerID *string
	e := a.DB.QueryRow(r.Context(), "SELECT p.id,p.owner_id FROM products p JOIN categories c ON c.id=p.category_id WHERE p.id=$1 AND "+visibleSQL, r.PathValue("id")).Scan(&id, &ownerID)
	if errors.Is(e, pgx.ErrNoRows) {
		fail(w, 404, "NOT_FOUND", "商品不存在")
		return
	}
	if e != nil {
		a.dbError(w, e)
		return
	}
	user, userErr := a.currentUser(r)
	if userErr != nil && !errors.Is(userErr, pgx.ErrNoRows) {
		a.dbError(w, userErr)
		return
	}
	if user != nil && ownerID != nil && *ownerID == user.ID {
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
	var recorded bool
	e = a.DB.QueryRow(r.Context(), `WITH inserted AS (
 INSERT INTO product_views_daily(product_id,visitor_hash,viewed_date,last_seen_at) VALUES($1,$2,(now() AT TIME ZONE 'Asia/Shanghai')::date,now()) ON CONFLICT DO NOTHING RETURNING 1
 ), touched AS (UPDATE product_views_daily SET last_seen_at=now() WHERE product_id=$1 AND visitor_hash=$2 AND viewed_date=(now() AT TIME ZONE 'Asia/Shanghai')::date)
 SELECT EXISTS(SELECT 1 FROM inserted)`, id, hex.EncodeToString(hash[:])).Scan(&recorded)
	if e != nil {
		a.dbError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": map[string]bool{"recorded": recorded}})
}
func (a *App) visitorSignature(id string) string {
	h := hmac.New(sha256.New, []byte(a.Config.SessionSecret))
	h.Write([]byte(id))
	return hex.EncodeToString(h.Sum(nil))
}
func (a *App) home(w http.ResponseWriter, r *http.Request) {
	featured, _, err := a.productRows(r.Context(), visibleSQL+" AND p.manual_rank IS NOT NULL", nil, "p.manual_rank ASC,p.id", 3, 0)
	if err != nil {
		a.dbError(w, err)
		return
	}
	for _, p := range featured {
		publicProductFields(p)
	}
	var presentation json.RawMessage
	err = a.DB.QueryRow(r.Context(), "SELECT value FROM site_settings WHERE key='home_presentation'").Scan(&presentation)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		a.dbError(w, err)
		return
	}
	sections := []homeSection{{Source: "hot", Limit: 6}, {Source: "recommended", Limit: 6}, {Source: "newest", Limit: 6}}
	if len(presentation) > 0 {
		var cfg homePresentation
		if json.Unmarshal(presentation, &cfg) == nil {
			sections = cfg.Sections
		}
	}
	result := map[string]any{"featured": featured, "hot": []map[string]any{}, "recommended": []map[string]any{}, "newest": []map[string]any{}, "recommendation_source": "newest-fallback", "personalization_source": "cold-start"}
	for _, section := range sections {
		order, err := productOrder(section.Source)
		if err != nil || section.Limit < 1 || section.Limit > 24 {
			continue
		}
		where, args := visibleSQL, []any{}
		if section.Source == "recommended" {
			var source string
			order, source, err = a.personalizedOrder(r, &where, &args)
			if err != nil {
				a.dbError(w, err)
				return
			}
			result["personalization_source"] = source
		}
		items, _, err := a.productRows(r.Context(), where, args, order, section.Limit, 0)
		if err != nil {
			a.dbError(w, err)
			return
		}
		for _, p := range items {
			publicProductSummary(p)
		}
		result[section.Source] = items
	}
	if len(presentation) > 0 {
		result["presentation"] = presentation
	}
	var state map[string]any
	if a.DB.QueryRow(r.Context(), "SELECT value FROM site_settings WHERE key='recommendation_state'").Scan(&state) == nil {
		result["computed_at"] = state["computed_at"]
		result["rule_version"] = state["rule_version"]
		computed, _ := state["computed_at"].(string)
		at, err := time.Parse(time.RFC3339Nano, computed)
		if err == nil && time.Since(at) <= time.Hour && state["has_engagement"] == true {
			result["recommendation_source"] = "local-view-favorite-score"
		}
	}
	writeJSON(w, 200, map[string]any{"data": result})
}
