package postgres

import (
	"context"
	"qingye/backend/internal/domain/management"
	"time"
)

// Every count, day and ranking is derived by one read-only SQL statement. Its
// MVCC snapshot and database clock cannot drift between independent requests.
func (s *ManagementStore) Dashboard(parent context.Context, in management.DashboardQuery) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	var data map[string]any
	err := s.DB.QueryRow(ctx, dashboardSQL, in.Days, in.RankBy).Scan(&data)
	if err != nil {
		return nil, managementStoreError(err)
	}
	return map[string]any{"data": data}, nil
}

// Do not join the raw interaction tables together: that multiplies independent
// facts. Aggregate each source first, then reuse the same rows for all outputs.
const dashboardSQL = `WITH clock AS MATERIALIZED (
 SELECT now() AS as_of,(now() AT TIME ZONE 'Asia/Shanghai')::date AS today
), bounds AS MATERIALIZED (
 SELECT as_of,today,today-($1::int-1) AS first_day,
 ((today-($1::int-1))::timestamp AT TIME ZONE 'Asia/Shanghai') AS first_at FROM clock
), eligible_products AS MATERIALIZED (
 SELECT id,product_code,title,trade_type,rental_category,status,owner_id FROM products WHERE deleted_at IS NULL
), product_counts AS (
 SELECT count(*) FILTER(WHERE p.deleted_at IS NULL) AS total,
 count(*) FILTER(WHERE ` + VisibleSQL + `) AS visible,
 count(*) FILTER(WHERE p.deleted_at IS NULL AND p.trade_type='sale') AS sale,
 count(*) FILTER(WHERE p.deleted_at IS NULL AND p.trade_type='rent') AS rent,
 count(*) FILTER(WHERE p.deleted_at IS NOT NULL AND p.deleted_by_admin) AS trash,
 count(*) FILTER(WHERE p.deleted_at IS NULL AND p.status='draft') AS draft,
 count(*) FILTER(WHERE p.deleted_at IS NULL AND p.status='pending') AS pending,
 count(*) FILTER(WHERE p.deleted_at IS NULL AND p.status='publishing') AS publishing,
 count(*) FILTER(WHERE p.deleted_at IS NULL AND p.status='published') AS published,
 count(*) FILTER(WHERE p.deleted_at IS NULL AND p.status='rejected') AS rejected,
 count(*) FILTER(WHERE p.deleted_at IS NULL AND p.status='rejected_final') AS rejected_final,
 count(*) FILTER(WHERE p.deleted_at IS NULL AND p.status='offline') AS offline,
 count(*) FILTER(WHERE p.deleted_at IS NULL AND p.status='sold') AS sold,
 count(*) FILTER(WHERE p.deleted_at IS NULL AND p.status='rented') AS rented
 FROM products p JOIN categories c ON c.id=p.category_id
), member_counts AS (
 SELECT count(*) AS total,count(*) FILTER(WHERE enabled) AS enabled,count(*) FILTER(WHERE NOT enabled) AS disabled,
 count(*) FILTER(WHERE created_at>=b.first_at AND created_at<=b.as_of) AS new_in_period
 FROM users CROSS JOIN bounds b WHERE role='member'
), view_days AS (
 SELECT v.product_id,v.viewed_date AS day,count(*) AS views,0::bigint AS favorites,0::bigint AS consultations
 FROM product_views_daily v JOIN eligible_products p ON p.id=v.product_id CROSS JOIN bounds b
 WHERE v.viewed_date BETWEEN b.first_day AND b.today AND v.created_at<=b.as_of
 GROUP BY v.product_id,v.viewed_date
), favorite_days AS (
 SELECT f.product_id,(f.created_at AT TIME ZONE 'Asia/Shanghai')::date AS day,
 0::bigint AS views,count(*) AS favorites,0::bigint AS consultations
 FROM favorites f JOIN eligible_products p ON p.id=f.product_id JOIN users u ON u.id=f.user_id CROSS JOIN bounds b
 WHERE f.active AND u.enabled AND u.role='member' AND p.owner_id IS DISTINCT FROM f.user_id
 AND f.created_at>=b.first_at AND f.created_at<=b.as_of
 GROUP BY f.product_id,(f.created_at AT TIME ZONE 'Asia/Shanghai')::date
), consultation_days AS (
 SELECT q.product_id,(q.first_consulted_at AT TIME ZONE 'Asia/Shanghai')::date AS day,
 0::bigint AS views,0::bigint AS favorites,count(*) AS consultations
 FROM product_consultations q JOIN eligible_products p ON p.id=q.product_id JOIN users u ON u.id=q.member_id CROSS JOIN bounds b
 WHERE u.enabled AND u.role='member' AND p.owner_id IS DISTINCT FROM q.member_id
 AND q.first_consulted_at>=b.first_at AND q.first_consulted_at<=b.as_of
 GROUP BY q.product_id,(q.first_consulted_at AT TIME ZONE 'Asia/Shanghai')::date
), events AS MATERIALIZED (
 SELECT * FROM view_days UNION ALL SELECT * FROM favorite_days UNION ALL SELECT * FROM consultation_days
), per_day AS (
 SELECT day,sum(views) AS views,sum(favorites) AS favorites,sum(consultations) AS consultations FROM events GROUP BY day
), per_product AS (
 SELECT product_id,sum(views) AS views,sum(favorites) AS favorites,sum(consultations) AS consultations FROM events GROUP BY product_id
), member_days AS (
 SELECT (u.created_at AT TIME ZONE 'Asia/Shanghai')::date AS day,count(*) AS new_members
 FROM users u CROSS JOIN bounds b WHERE u.role='member' AND u.created_at>=b.first_at AND u.created_at<=b.as_of
 GROUP BY (u.created_at AT TIME ZONE 'Asia/Shanghai')::date
), dates AS (
 SELECT b.first_day+series.day_offset AS day FROM bounds b CROSS JOIN generate_series(0,$1::int-1) AS series(day_offset)
), trend AS (
 SELECT d.day AS date,coalesce(e.views,0) AS views,coalesce(e.favorites,0) AS favorites,
 coalesce(e.consultations,0) AS consultations,coalesce(m.new_members,0) AS new_members
 FROM dates d LEFT JOIN per_day e ON e.day=d.day LEFT JOIN member_days m ON m.day=d.day
), ranked AS MATERIALIZED (
 SELECT p.id,p.product_code,p.title,p.trade_type,p.rental_category,p.status,e.views,e.favorites,e.consultations,
 CASE $2::text WHEN 'views' THEN e.views WHEN 'favorites' THEN e.favorites WHEN 'consultations' THEN e.consultations END AS metric
 FROM per_product e JOIN eligible_products p ON p.id=e.product_id
 WHERE CASE $2::text WHEN 'views' THEN e.views WHEN 'favorites' THEN e.favorites WHEN 'consultations' THEN e.consultations END>0
 ORDER BY metric DESC,p.product_code,p.id LIMIT 10
)
SELECT jsonb_build_object(
 'generated_at',b.as_of,'timezone','Asia/Shanghai',
 'range',jsonb_build_object('days',$1::int,'start_date',b.first_day,'end_date',b.today,'start_at',b.first_at,'end_at',b.as_of),
 'products',jsonb_build_object('total',p.total,'visible',p.visible,'sale',p.sale,'rent',p.rent,'trash',p.trash,
 'statuses',jsonb_build_object('draft',p.draft,'pending',p.pending,'publishing',p.publishing,'published',p.published,
 'rejected',p.rejected,'rejected_final',p.rejected_final,'offline',p.offline,'sold',p.sold,'rented',p.rented)),
 'members',to_jsonb(m),
 'todos',jsonb_build_object('pending_products',p.pending,'publishing_products',p.publishing,
 'pending_nicknames',(SELECT count(*) FROM nickname_requests WHERE status='pending'),
 'waiting_conversations',(SELECT count(*) FROM chat_conversations WHERE status='waiting' AND merged_into IS NULL)),
 'engagement',(SELECT jsonb_build_object('views',coalesce(sum(views),0),'favorites',coalesce(sum(favorites),0),'consultations',coalesce(sum(consultations),0)) FROM events),
 'trend',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY t.date),'[]'::jsonb) FROM trend t),
 'ranking',jsonb_build_object('by',$2::text,'limit',10,'items',(SELECT coalesce(jsonb_agg(to_jsonb(r)-'metric' ORDER BY r.metric DESC,r.product_code,r.id),'[]'::jsonb) FROM ranked r))
 ) FROM bounds b CROSS JOIN product_counts p CROSS JOIN member_counts m`
