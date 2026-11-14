package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"qingye/backend/internal/domain/recommendation"
	"time"
)

type RecommendationStore struct{ DB *pgxpool.Pool }

func (a *RecommendationStore) Report(ctx context.Context) (map[string]any, error) {
	// The management report uses the same live candidate visibility and score
	// freshness as public hot ordering. Retain score as the original batch value
	// so an expired batch is distinguishable from its currently effective zero.
	natural := "CASE WHEN h.computed_at BETWEEN now()-interval '60 minutes' AND now() THEN COALESCE(h.score,0) ELSE 0 END"
	rows, e := (&CatalogStore{DB: a.DB}).JSONRows(ctx, "SELECT COALESCE(to_jsonb(h),'{}'::jsonb)||jsonb_build_object('product_id',p.id,'title',p.title,'score',COALESCE(h.score,0),'natural_score',"+natural+",'recommendation_boost',p.recommendation_boost,'hot_score',("+natural+"+p.recommendation_boost),'score_fresh',COALESCE(h.computed_at BETWEEN now()-interval '60 minutes' AND now(),false)) FROM products p JOIN categories c ON c.id=p.category_id LEFT JOIN product_hot_scores h ON h.product_id=p.id WHERE "+VisibleSQL+" ORDER BY ("+natural+"+p.recommendation_boost) DESC,p.published_at DESC NULLS LAST,p.id LIMIT 50")
	if e != nil {
		return nil, e
	}
	config, e := LoadRecommendations(ctx, a.DB)
	if e != nil {
		return nil, e
	}
	var state map[string]any
	_ = a.DB.QueryRow(ctx, "SELECT value FROM site_settings WHERE key='recommendation_state'").Scan(&state)
	return map[string]any{"data": rows, "rule_version": state["rule_version"], "computed_at": state["computed_at"], "applied_config": state["config"], "config": config, "rule": "当前热门按有效自然热度加单商品推荐加分排序；自然热度为窗口内有效浏览、首次有效收藏和首次咨询人数加权，快照超过60分钟或处于未来时按0计算。本榜不表示个人推荐得分"}, nil
}

func LoadRecommendations(ctx context.Context, q RowQuerier) (recommendation.Config, error) {
	var raw []byte
	err := q.QueryRow(ctx, "SELECT value FROM site_settings WHERE key='recommendation'").Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return recommendation.DefaultConfig(), nil
	}
	if err != nil {
		return recommendation.Config{}, err
	}
	return recommendation.ParseConfig(raw)
}
func (a *RecommendationStore) RefreshPopular(ctx context.Context, asOf time.Time, batch string) error {
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
	config, err := LoadRecommendations(ctx, tx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM product_hot_scores h WHERE NOT EXISTS(
SELECT 1 FROM products p JOIN categories c ON c.id=p.category_id WHERE p.id=h.product_id AND `+VisibleSQL+`)`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_hot_scores(product_id,score,views,favorites,consultations,new_bonus,batch_id,computed_at)
 SELECT p.id,v.n::bigint*$2::int+f.n::bigint*$3::int+q.n::bigint*$6::int,
 v.n,f.n,q.n,0,$1,$5
 FROM products p JOIN categories c ON c.id=p.category_id
 CROSS JOIN LATERAL(SELECT count(*)::int n FROM product_views_daily WHERE product_id=p.id
   AND viewed_date BETWEEN ($5::timestamptz AT TIME ZONE 'Asia/Shanghai')::date-($4::int-1) AND ($5::timestamptz AT TIME ZONE 'Asia/Shanghai')::date
   AND created_at<=$5)v
 CROSS JOIN LATERAL(SELECT count(*)::int n FROM favorites fav JOIN users u ON u.id=fav.user_id
   WHERE fav.product_id=p.id AND fav.active AND u.enabled AND u.role='member' AND p.owner_id IS DISTINCT FROM fav.user_id
   AND fav.created_at>=(date_trunc('day',$5::timestamptz AT TIME ZONE 'Asia/Shanghai')-make_interval(days=>$4::int-1)) AT TIME ZONE 'Asia/Shanghai'
   AND fav.created_at<=$5)f
 CROSS JOIN LATERAL(SELECT count(*)::int n FROM product_consultations pc JOIN users u ON u.id=pc.member_id
   WHERE pc.product_id=p.id AND u.enabled AND u.role='member' AND p.owner_id IS DISTINCT FROM pc.member_id
   AND pc.first_consulted_at>=(date_trunc('day',$5::timestamptz AT TIME ZONE 'Asia/Shanghai')-make_interval(days=>$4::int-1)) AT TIME ZONE 'Asia/Shanghai'
   AND pc.first_consulted_at<=$5)q
 WHERE `+VisibleSQL+` ON CONFLICT(product_id) DO UPDATE SET score=EXCLUDED.score,views=EXCLUDED.views,favorites=EXCLUDED.favorites,consultations=EXCLUDED.consultations,new_bonus=EXCLUDED.new_bonus,batch_id=EXCLUDED.batch_id,computed_at=EXCLUDED.computed_at`, batch, config.ViewWeight, config.FavoriteWeight, config.WindowDays, asOf, config.ConsultationWeight)
	if err != nil {
		return err
	}
	// Persist the exact applied rule alongside the batch, separately from future editable settings.
	var hasEngagement bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM product_hot_scores WHERE views>0 OR favorites>0 OR consultations>0)").Scan(&hasEngagement)
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
	return nil
}
func (a *RecommendationStore) RefreshItemCF(parent context.Context, asOf time.Time, batch string) error {
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
	if err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", recommendation.ItemCFLock).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	// View/reset facts and freshness checks use PostgreSQL time. An API host's
	// clock must not make a newly committed batch appear to be from the future.
	if asOf.IsZero() {
		if err = tx.QueryRow(ctx, "SELECT now()").Scan(&asOf); err != nil {
			return err
		}
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
 AND COALESCE(v.last_seen_at,v.created_at)<=$1 AND COALESCE(pref.enabled,true) AND `+VisibleSQL+`
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
	state := map[string]any{"version": recommendation.ItemCFVersion, "batch_id": batch, "computed_at": asOf.UTC(), "relations": result.RowsAffected(), "window_days": 30, "max_history": 50, "top_k": 30, "min_common_viewers": 2, "shrinkage": 3, "max_age_hours": 2}
	if _, err = tx.Exec(ctx, `INSERT INTO site_settings(key,value) VALUES('itemcf_state',$1) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`, state); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Fetch at most 50*30 edges using the primary-key prefix, with a short read
// budget. Raw viewer identities and the global graph never leave this service.
func (a *RecommendationStore) ItemScores(parent context.Context, seeds map[string]float64) (map[string]float64, error) {
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
 AND NOT ($1::jsonb ? s.target_id) AND `+VisibleSQL+`
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
func (a *RecommendationStore) Preferences(ctx context.Context, id string) (bool, time.Time, error) {
	enabled := true
	reset := time.Unix(0, 0)
	e := a.DB.QueryRow(ctx, "SELECT enabled,reset_at FROM recommendation_preferences WHERE visitor_hash=$1", id).Scan(&enabled, &reset)
	if errors.Is(e, pgx.ErrNoRows) {
		e = nil
	}
	return enabled, reset, e
}
func (a *RecommendationStore) History(ctx context.Context, id string, reset time.Time) ([]recommendation.View, error) {
	rows, e := a.DB.Query(ctx, `SELECT p.id,p.category_id,c.name,p.trade_type,COALESCE(p.attributes->>'server',''),
 CASE WHEN p.price_amount>0 AND p.price_unit<>'unknown' THEN p.price_unit||':'||floor(log(2,p.price_amount+1))::text ELSE '' END,
 sum(power(0.5,extract(epoch FROM (now()-COALESCE(v.last_seen_at,v.created_at)))/604800))::float8 AS weight
 FROM product_views_daily v JOIN products p ON p.id=v.product_id JOIN categories c ON c.id=p.category_id
 WHERE v.visitor_hash=$1 AND v.viewed_date>=(now() AT TIME ZONE 'Asia/Shanghai')::date-30
 AND COALESCE(v.last_seen_at,v.created_at)>GREATEST($2::timestamptz,now()-interval '30 days')
 AND COALESCE(v.last_seen_at,v.created_at)<=now() AND `+VisibleSQL+`
 GROUP BY p.id,c.name ORDER BY max(COALESCE(v.last_seen_at,v.created_at)) DESC,p.id LIMIT 50`, id, reset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []recommendation.View{}
	for rows.Next() {
		var v recommendation.View
		if e = rows.Scan(&v.ID, &v.Category, &v.Name, &v.Trade, &v.Server, &v.Price, &v.Weight); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (a *RecommendationStore) SavePreference(ctx context.Context, id string, enabled *bool, reset bool) error {
	_, e := a.DB.Exec(ctx, `INSERT INTO recommendation_preferences(visitor_hash,enabled,reset_at) VALUES($1,COALESCE($2,true),CASE WHEN $3 THEN now() ELSE '1970-01-01 UTC'::timestamptz END)
 ON CONFLICT(visitor_hash) DO UPDATE SET enabled=COALESCE($2,recommendation_preferences.enabled),reset_at=CASE WHEN $3 THEN now() ELSE recommendation_preferences.reset_at END,updated_at=now()`, id, enabled, reset)
	return e
}
