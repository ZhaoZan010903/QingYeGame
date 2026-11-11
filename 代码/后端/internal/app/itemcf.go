package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

const itemCFVersion = "itemcf-v1"
const itemCFLock = 1834182

// RunItemCF belongs to the public process. PostgreSQL serializes overlapping
// workers, including manual maintenance, without a new Redis responsibility.
func (a *App) RunItemCF(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := a.RefreshItemCF(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("ItemCF refresh failed; attribute recommendations remain available", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) RefreshItemCF(ctx context.Context) error {
	return a.refreshItemCFAt(ctx, time.Now().UTC())
}

// Each run has one consistent cutoff and atomically replaces the previous
// snapshot. A failure or cancellation leaves the old complete batch intact.
func (a *App) refreshItemCFAt(parent context.Context, asOf time.Time) error {
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	tx, err := a.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(parent), 3*time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	var locked bool
	if err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", itemCFLock).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	// Bound database sort/hash memory and statement duration. PostgreSQL may
	// spill to temporary disk rather than accumulating the graph in Go or Redis.
	if _, err = tx.Exec(ctx, "SET LOCAL work_mem='4MB'; SET LOCAL statement_timeout='30s'; SET LOCAL lock_timeout='2s'; SET LOCAL max_parallel_workers_per_gather=0"); err != nil {
		return err
	}
	// Materialize bounded histories with an indexed, analyzed temporary table.
	// A self-join over a multiply referenced CTE otherwise has no column stats
	// and can choose a quadratic nested loop even for modest browsing volumes.
	_, err = tx.Exec(ctx, `CREATE TEMP TABLE itemcf_training ON COMMIT DROP AS WITH recent AS (
 SELECT v.visitor_hash,v.product_id,max(COALESCE(v.last_seen_at,v.created_at)) AS last_view
 FROM product_views_daily v JOIN products p ON p.id=v.product_id JOIN categories c ON c.id=p.category_id
 LEFT JOIN recommendation_preferences pref ON pref.visitor_hash=v.visitor_hash
 WHERE v.viewed_date BETWEEN ($1::timestamptz AT TIME ZONE 'Asia/Shanghai')::date-30 AND ($1::timestamptz AT TIME ZONE 'Asia/Shanghai')::date
 AND COALESCE(v.last_seen_at,v.created_at)>GREATEST($1::timestamptz-interval '30 days',COALESCE(pref.reset_at,'1970-01-01 UTC'::timestamptz))
 AND COALESCE(v.last_seen_at,v.created_at)<=$1 AND COALESCE(pref.enabled,true) AND `+visibleSQL+`
 GROUP BY v.visitor_hash,v.product_id
 ), ranked AS (
 SELECT *,row_number() OVER(PARTITION BY visitor_hash ORDER BY last_view DESC,product_id) AS position FROM recent
 ) SELECT visitor_hash,product_id FROM ranked WHERE position<=50`, asOf)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "CREATE INDEX ON itemcf_training(visitor_hash,product_id); ANALYZE itemcf_training"); err != nil {
		return err
	}
	batch := newID()
	if _, err = tx.Exec(ctx, "DELETE FROM product_item_similarities"); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `WITH counts AS (
 SELECT product_id,count(*)::float8 AS viewers FROM itemcf_training GROUP BY product_id
 ), pairs AS (
 SELECT a.product_id AS source_id,b.product_id AS target_id,count(*)::int AS common_viewers
 FROM itemcf_training a JOIN itemcf_training b ON a.visitor_hash=b.visitor_hash AND a.product_id<b.product_id
 GROUP BY a.product_id,b.product_id HAVING count(*)>=2
 ), scored AS (
 SELECT source_id,target_id,common_viewers,
 common_viewers::float8/sqrt(a.viewers*b.viewers)*common_viewers::float8/(common_viewers+3) AS score
 FROM pairs JOIN counts a ON a.product_id=source_id JOIN counts b ON b.product_id=target_id
 ), directed AS (
 SELECT * FROM scored UNION ALL SELECT target_id,source_id,common_viewers,score FROM scored
 ), neighbors AS (
 SELECT *,row_number() OVER(PARTITION BY source_id ORDER BY score DESC,common_viewers DESC,target_id) AS position FROM directed
 )
 INSERT INTO product_item_similarities(source_id,target_id,score,common_viewers,batch_id,computed_at)
 SELECT source_id,target_id,score,common_viewers,$2,$1::timestamptz FROM neighbors WHERE position<=30`, asOf, batch)
	if err != nil {
		return err
	}
	state := map[string]any{"version": itemCFVersion, "batch_id": batch, "computed_at": asOf.UTC(), "relations": result.RowsAffected(), "window_days": 30, "max_history": 50, "top_k": 30, "min_common_viewers": 2, "shrinkage": 3, "max_age_hours": 2}
	if _, err = tx.Exec(ctx, `INSERT INTO site_settings(key,value) VALUES('itemcf_state',$1) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`, state); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Fetch at most 50*30 edges using the primary-key prefix, with a short read
// budget. Raw viewer identities and the global graph never leave this service.
func (a *App) itemCFScores(parent context.Context, seeds map[string]float64) (map[string]float64, error) {
	scores := map[string]float64{}
	if len(seeds) == 0 {
		return scores, nil
	}
	raw, err := json.Marshal(seeds)
	if err != nil {
		return scores, err
	}
	ctx, cancel := context.WithTimeout(parent, 500*time.Millisecond)
	defer cancel()
	rows, err := a.DB.Query(ctx, `SELECT s.target_id,sum(s.score*seed.value::float8)
 FROM jsonb_each_text($1::jsonb) seed JOIN product_item_similarities s ON s.source_id=seed.key
 JOIN products p ON p.id=s.target_id JOIN categories c ON c.id=p.category_id
 WHERE s.computed_at BETWEEN now()-interval '2 hours' AND now()
 AND NOT ($1::jsonb ? s.target_id) AND `+visibleSQL+`
 GROUP BY s.target_id`, json.RawMessage(raw))
	if err != nil {
		return scores, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var score float64
		if err = rows.Scan(&id, &score); err != nil {
			return nil, err
		}
		scores[id] = score
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return scores, nil
}

func (a *App) applyItemCF(ctx context.Context, p *interestProfile) error {
	scores, err := a.itemCFScores(ctx, p.products)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A missing, stale or temporarily unavailable derived index must not turn
		// the catalog into an error page. The attribute path stays authoritative.
		if !errors.Is(err, context.DeadlineExceeded) {
			slog.Warn("ItemCF read unavailable; using attributes", "error", err)
		}
		scores = map[string]float64{}
	}
	p.weights["itemcf"] = scores
	return nil
}
