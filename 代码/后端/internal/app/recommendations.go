package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// These are editable integration defaults, not a confirmed business ranking policy.
type recommendationConfig struct {
	Version        string `json:"version"`
	WindowDays     int    `json:"window_days"`
	ViewWeight     int    `json:"view_weight"`
	FavoriteWeight int    `json:"favorite_weight"`
	NewBonus       int    `json:"new_bonus"`
	NewHours       int    `json:"new_hours"`
}

func defaultRecommendations() recommendationConfig {
	return recommendationConfig{"engagement-v2", 7, 1, 5, 0, 72}
}
func parseRecommendations(raw []byte) (recommendationConfig, error) {
	var c recommendationConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, errors.New("推荐配置结构无效")
	}
	if strings.TrimSpace(c.Version) == "" || len(c.Version) > 40 || c.WindowDays < 1 || c.WindowDays > 30 || c.NewHours < 1 || c.NewHours > 720 || c.ViewWeight < 0 || c.ViewWeight > 100 || c.FavoriteWeight < 0 || c.FavoriteWeight > 100 || c.NewBonus < 0 || c.NewBonus > 100 {
		return c, errors.New("推荐参数超出允许范围")
	}
	// Legacy keys remain readable; publication time never earns popularity points.
	c.NewBonus = 0
	return c, nil
}
func loadRecommendations(ctx context.Context, q rowQuerier) (recommendationConfig, error) {
	var raw []byte
	err := q.QueryRow(ctx, "SELECT value FROM site_settings WHERE key='recommendation'").Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultRecommendations(), nil
	}
	if err != nil {
		return recommendationConfig{}, err
	}
	return parseRecommendations(raw)
}

func (a *App) RefreshRecommendations(ctx context.Context) error {
	return a.refreshRecommendationsAt(ctx, time.Time{})
}

// One as-of instant defines the whole batch, including its persisted rule metadata.
// The window covers Beijing calendar days, including today up to asOf. Future
// timestamps never earn engagement. Personal recommendations are independent.
func (a *App) refreshRecommendationsAt(ctx context.Context, asOf time.Time) error {
	tx, err := a.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(1834180)").Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return errors.New("recommendation refresh already running")
	}
	// The database also timestamps engagement and evaluates snapshot freshness.
	// Use that same clock when the caller did not provide a fixed test cutoff.
	if asOf.IsZero() {
		if err = tx.QueryRow(ctx, "SELECT now()").Scan(&asOf); err != nil {
			return err
		}
	}
	config, err := loadRecommendations(ctx, tx)
	if err != nil {
		return err
	}
	batch := newID()
	_, err = tx.Exec(ctx, `DELETE FROM product_hot_scores h WHERE NOT EXISTS(
SELECT 1 FROM products p JOIN categories c ON c.id=p.category_id WHERE p.id=h.product_id AND `+visibleSQL+`)`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_hot_scores(product_id,score,views,favorites,new_bonus,batch_id,computed_at)
 SELECT p.id,v.n::bigint*$2::int+f.n::bigint*$3::int,
 v.n,f.n,0,$1,$5
 FROM products p JOIN categories c ON c.id=p.category_id
 CROSS JOIN LATERAL(SELECT count(*)::int n FROM product_views_daily WHERE product_id=p.id
   AND viewed_date BETWEEN ($5::timestamptz AT TIME ZONE 'Asia/Shanghai')::date-($4::int-1) AND ($5::timestamptz AT TIME ZONE 'Asia/Shanghai')::date
   AND created_at<=$5)v
 CROSS JOIN LATERAL(SELECT count(*)::int n FROM favorites WHERE product_id=p.id AND active
   AND created_at>=(date_trunc('day',$5::timestamptz AT TIME ZONE 'Asia/Shanghai')-make_interval(days=>$4::int-1)) AT TIME ZONE 'Asia/Shanghai'
   AND created_at<=$5)f
 WHERE `+visibleSQL+` ON CONFLICT(product_id) DO UPDATE SET score=EXCLUDED.score,views=EXCLUDED.views,favorites=EXCLUDED.favorites,new_bonus=EXCLUDED.new_bonus,batch_id=EXCLUDED.batch_id,computed_at=EXCLUDED.computed_at`, batch, config.ViewWeight, config.FavoriteWeight, config.WindowDays, asOf)
	if err != nil {
		return err
	}
	// Persist the exact applied rule alongside the batch, separately from future editable settings.
	var hasEngagement bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM product_hot_scores WHERE views>0 OR favorites>0)").Scan(&hasEngagement)
	if err != nil {
		return err
	}
	state := map[string]any{"batch_id": batch, "computed_at": asOf.UTC(), "rule_version": config.Version, "config": config, "has_engagement": hasEngagement}
	_, err = tx.Exec(ctx, `INSERT INTO site_settings(key,value) VALUES('recommendation_state',$1) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`, state)
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if a.Redis != nil {
		_ = a.Redis.Set(ctx, "qingye:recommendations:batch", batch, time.Hour).Err()
	}
	return nil
}
