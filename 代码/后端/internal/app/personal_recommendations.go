package app

import (
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type interestCategory struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Weight float64 `json:"weight"`
}
type interestProfile struct {
	Enabled     bool               `json:"enabled"`
	SampleCount int                `json:"sample_count"`
	Categories  []interestCategory `json:"categories"`
	weights     map[string]map[string]float64
	products    map[string]float64
}

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
	p := interestProfile{Enabled: true, Categories: []interestCategory{}, weights: map[string]map[string]float64{"category": {}, "trade": {}, "server": {}, "price": {}}, products: map[string]float64{}}
	id, e := a.recommendationIdentity(r)
	if e != nil || id == "" {
		return p, e
	}
	reset := time.Unix(0, 0)
	e = a.DB.QueryRow(r.Context(), "SELECT enabled,reset_at FROM recommendation_preferences WHERE visitor_hash=$1", id).Scan(&p.Enabled, &reset)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return p, e
	}
	if !p.Enabled {
		return p, nil
	}
	rows, e := a.DB.Query(r.Context(), `SELECT p.id,p.category_id,c.name,p.trade_type,COALESCE(p.attributes->>'server',''),
 CASE WHEN p.price_amount>0 AND p.price_unit<>'unknown' THEN p.price_unit||':'||floor(log(2,p.price_amount+1))::text ELSE '' END,
 sum(power(0.5,extract(epoch FROM (now()-COALESCE(v.last_seen_at,v.created_at)))/604800))::float8 AS weight
 FROM product_views_daily v JOIN products p ON p.id=v.product_id JOIN categories c ON c.id=p.category_id
 WHERE v.visitor_hash=$1 AND v.viewed_date>=(now() AT TIME ZONE 'Asia/Shanghai')::date-30
 AND COALESCE(v.last_seen_at,v.created_at)>GREATEST($2::timestamptz,now()-interval '30 days')
 AND COALESCE(v.last_seen_at,v.created_at)<=now() AND `+visibleSQL+`
 GROUP BY p.id,c.name ORDER BY max(COALESCE(v.last_seen_at,v.created_at)) DESC,p.id LIMIT 50`, id, reset)
	if e != nil {
		return p, e
	}
	defer rows.Close()
	names := map[string]string{}
	total := 0.0
	for rows.Next() {
		var id, category, name, trade, server, price string
		var weight float64
		if e = rows.Scan(&id, &category, &name, &trade, &server, &price, &weight); e != nil {
			return p, e
		}
		for group, value := range map[string]string{"category": category, "trade": trade, "server": server, "price": price} {
			if value != "" {
				p.weights[group][value] += weight
			}
		}
		names[category] = name
		p.products[id] = weight
		p.SampleCount++
		total += weight
	}
	if e = rows.Err(); e != nil {
		return p, e
	}
	if total > 0 {
		for id, weight := range p.products {
			p.products[id] = weight / total
		}
		for _, group := range p.weights {
			for key, w := range group {
				group[key] = w / total
			}
		}
	}
	for id, w := range p.weights["category"] {
		p.Categories = append(p.Categories, interestCategory{id, names[id], w})
	}
	sort.Slice(p.Categories, func(i, j int) bool {
		if p.Categories[i].Weight == p.Categories[j].Weight {
			return p.Categories[i].ID < p.Categories[j].ID
		}
		return p.Categories[i].Weight > p.Categories[j].Weight
	})
	return p, nil
}

func (a *App) personalizedOrder(r *http.Request, where *string, args *[]any) (string, string, error) {
	hot, _ := productOrder("hot")
	profile, e := a.interestProfile(r)
	if e != nil {
		return "", "", e
	}
	if !profile.Enabled {
		return hot, "disabled", nil
	}
	if profile.SampleCount == 0 {
		return hot, "cold-start", nil
	}
	if e = a.applyItemCF(r.Context(), &profile); e != nil {
		return "", "", e
	}
	raw, e := json.Marshal(profile.weights)
	if e != nil {
		return "", "", e
	}
	*args = append(*args, json.RawMessage(raw))
	n := len(*args)
	// The parameter is also present in productRows' count query; no user input is SQL.
	*where += fmt.Sprintf(" AND $%d::jsonb IS NOT NULL", n)
	order := fmt.Sprintf(`COALESCE(($%[1]d::jsonb->'itemcf'->>p.id)::float8,0) DESC,
 (6*COALESCE(($%[1]d::jsonb->'category'->>p.category_id)::float8,0)
 +2*COALESCE(($%[1]d::jsonb->'trade'->>p.trade_type)::float8,0)
 +3*COALESCE(($%[1]d::jsonb->'server'->>(p.attributes->>'server'))::float8,0)
 +COALESCE(($%[1]d::jsonb->'price'->>(CASE WHEN p.price_amount>0 AND p.price_unit<>'unknown' THEN p.price_unit||':'||floor(log(2,p.price_amount+1))::text ELSE '' END))::float8,0)) DESC,`, n)
	return order + hot, "browsing", nil
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
	_, e = a.DB.Exec(r.Context(), `INSERT INTO recommendation_preferences(visitor_hash,enabled,reset_at) VALUES($1,COALESCE($2,true),CASE WHEN $3 THEN now() ELSE '1970-01-01 UTC'::timestamptz END)
 ON CONFLICT(visitor_hash) DO UPDATE SET enabled=COALESCE($2,recommendation_preferences.enabled),reset_at=CASE WHEN $3 THEN now() ELSE recommendation_preferences.reset_at END,updated_at=now()`, id, input.Enabled, input.Reset)
	if e != nil {
		a.dbError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": map[string]bool{"ok": true}})
}
