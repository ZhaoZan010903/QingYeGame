package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
	"strings"
	"time"
)

func (a *CatalogStore) JSONRows(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
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

func (a *CatalogStore) Categories(ctx context.Context) ([]map[string]any, error) {
	return a.JSONRows(ctx, "SELECT to_jsonb(c) FROM categories c WHERE enabled ORDER BY sort_order,id")
}
func (a *CatalogStore) Contacts(ctx context.Context, purpose string) ([]map[string]any, error) {
	return a.JSONRows(ctx, "SELECT to_jsonb(c)-'qr_media_id' || jsonb_build_object('qr_url',CASE WHEN qr_media_id IS NOT NULL THEN '/api/v1/media/'||qr_media_id||'/file' ELSE qr_url END) FROM contacts c WHERE enabled AND ($1='' OR purpose ? $1 OR purpose ? 'general') ORDER BY id", purpose)
}
func (a *CatalogStore) Page(ctx context.Context, slug string) (json.RawMessage, error) {
	var b json.RawMessage
	e := a.DB.QueryRow(ctx, "SELECT to_jsonb(p) FROM content_pages p WHERE slug=$1 AND published", slug).Scan(&b)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, fault.New(fault.NotFound, "NOT_FOUND", "说明页尚未发布")
	}
	return b, e
}
func (a *CatalogStore) VisibleOwner(ctx context.Context, id string) (*string, error) {
	var owner *string
	e := a.DB.QueryRow(ctx, "SELECT p.owner_id FROM products p JOIN categories c ON c.id=p.category_id WHERE p.id=$1 AND "+VisibleSQL, id).Scan(&owner)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, fault.New(fault.NotFound, "NOT_FOUND", "商品不存在")
	}
	return owner, e
}
func (a *CatalogStore) RecordView(ctx context.Context, id, hash string) (bool, error) {
	var recorded bool
	e := a.DB.QueryRow(ctx, `WITH inserted AS (
 INSERT INTO product_views_daily(product_id,visitor_hash,viewed_date,last_seen_at) VALUES($1,$2,(now() AT TIME ZONE 'Asia/Shanghai')::date,now()) ON CONFLICT DO NOTHING RETURNING 1
 ), touched AS (UPDATE product_views_daily SET last_seen_at=now() WHERE product_id=$1 AND visitor_hash=$2 AND viewed_date=(now() AT TIME ZONE 'Asia/Shanghai')::date)
 SELECT EXISTS(SELECT 1 FROM inserted)`, id, hash).Scan(&recorded)
	return recorded, e
}
func (a *CatalogStore) Featured(ctx context.Context) ([]map[string]any, error) {
	// Only an explicit administrator slide enters the carousel; no product-copy fallback.
	projection := strings.Replace(fmt.Sprintf(ProductJSON, 1), "ORDER BY pi.sort_order,pi.media_id LIMIT 1", "ORDER BY (pi.media_id=COALESCE(sl.value->>'imageId','')) DESC,pi.sort_order,pi.media_id LIMIT 1", 1)
	return a.JSONRows(ctx, "SELECT "+projection+` FROM site_settings ss CROSS JOIN LATERAL jsonb_array_elements(COALESCE(ss.value->'slides','[]')) WITH ORDINALITY sl(value,n) JOIN products p ON p.id=sl.value->>'productId' JOIN categories c ON c.id=p.category_id WHERE ss.key='home_presentation' AND `+VisibleSQL+" ORDER BY sl.n LIMIT 12")
}
func (a *CatalogStore) PublishForm(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	err := a.DB.QueryRow(ctx, "SELECT value FROM site_settings WHERE key='publish_form'").Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		raw, err = json.Marshal(management.DefaultPublishForm())
	}
	return raw, err
}
func (a *CatalogStore) HomePresentation(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	e := a.DB.QueryRow(ctx, "SELECT value FROM site_settings WHERE key='home_presentation'").Scan(&raw)
	if errors.Is(e, pgx.ErrNoRows) {
		e = nil
	}
	return raw, e
}
func (a *CatalogStore) RecommendationState(ctx context.Context) (map[string]any, time.Time, error) {
	var state map[string]any
	var now time.Time
	e := a.DB.QueryRow(ctx, "SELECT value,now() FROM site_settings WHERE key='recommendation_state'").Scan(&state, &now)
	return state, now, e
}
