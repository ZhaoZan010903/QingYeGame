-- Expand quote units without inferring or replacing historical monetary values.
ALTER TABLE products DROP CONSTRAINT products_price_unit_check;
ALTER TABLE products ADD CONSTRAINT products_price_unit_check CHECK(price_unit IN ('unknown','account','hour','day','week','month','period'));
ALTER TABLE products ADD COLUMN rental_category text NOT NULL DEFAULT ''
 CHECK(rental_category IN ('','hour_day','week_month'));
ALTER TABLE products ADD CONSTRAINT products_rental_category_trade CHECK(trade_type='rent' OR rental_category='');
CREATE INDEX products_rental_category_public ON products(rental_category,published_at DESC,id)
 WHERE deleted_at IS NULL AND status='published';

-- Empty configuration already permits all rental units; do not turn it into period-only.
UPDATE categories SET price_units=price_units||'"period"'::jsonb
 WHERE trade_type IN ('rent','all') AND jsonb_array_length(price_units)>0
 AND NOT(price_units ? 'period')
 AND (trade_type='rent' OR price_units ?| ARRAY['hour','day','week','month']);

-- Only the exact original business category is evidence of rental placement.
-- Version changes keep previously approved current versions visible; review snapshots stay intact.
WITH changed AS (
 UPDATE products SET rental_category=CASE btrim(legacy_category)
   WHEN '时日租号' THEN 'hour_day' WHEN '周月租号' THEN 'week_month' END,
   version=version+1,
   approved_version=CASE WHEN approved_version=version THEN version+1 ELSE approved_version END
 WHERE source='legacy' AND trade_type='rent' AND deleted_at IS NULL
   AND btrim(legacy_category) IN ('时日租号','周月租号')
 RETURNING rental_category
)
INSERT INTO admin_logs(id,action,resource_id,detail)
SELECT 'migration:0017_rental_quote_periods','migration.rental-category','products',
 jsonb_build_object('updated_products',count(*),'hour_day',count(*) FILTER(WHERE rental_category='hour_day'),
 'week_month',count(*) FILTER(WHERE rental_category='week_month'),'quotes_unchanged',true)
FROM changed;
