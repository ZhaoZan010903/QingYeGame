-- Owner-confirmed on 2026-11-11: account sale in CNY, short rent per day,
-- long rent per month. Never infer from titles, prices or trade_type alone.
WITH rules(category_id, legacy_category, trade_type, price_unit) AS (
 VALUES ('legacy-sale','QQ炫舞买号','sale','account'),
        ('legacy-short-rent','时日租号','rent','day'),
        ('legacy-long-rent','周月租号','rent','month')
), updated AS (
 UPDATE products p SET
  price_unit=r.price_unit,
  price_amount=p.legacy_price::numeric(12,2),
  version=p.version+1,
  approved_version=CASE WHEN p.approved_version=p.version THEN p.version+1 ELSE p.approved_version END,
  updated_at=now()
 FROM rules r, categories c
 WHERE p.source='legacy' AND p.price_unit='unknown' AND p.price_amount IS NULL
   AND p.category_id=r.category_id AND p.legacy_category=r.legacy_category AND p.trade_type=r.trade_type
   AND c.id=p.category_id AND (c.price_units='[]'::jsonb OR c.price_units ? r.price_unit)
   AND p.legacy_price ~ '^[0-9]{1,10}([.][0-9]{1,2})?$'
 RETURNING p.id
)
INSERT INTO admin_logs(id,action,detail)
SELECT 'migration:0001_confirm_legacy_price_units','maintenance.legacy_price_units',
 jsonb_build_object('rule','owner-confirmed-2026-11-11','currency','CNY','updated_products',count(*))
FROM updated;

-- Change only the original imported defaults, not an operator's custom config.
-- Preserve compatibility if an operator already priced a product in another unit.
WITH rules(id,name,trade_type,old_units,new_units,unit) AS (
 VALUES ('legacy-sale','QQ炫舞买号','sale','["account","unknown"]'::jsonb,'["account"]'::jsonb,'account'),
        ('legacy-short-rent','时日租号','rent','["hour","day","unknown"]'::jsonb,'["day"]'::jsonb,'day'),
        ('legacy-long-rent','周月租号','rent','["week","month","unknown"]'::jsonb,'["month"]'::jsonb,'month')
)
UPDATE categories c SET price_units=r.new_units FROM rules r
WHERE c.id=r.id AND c.name=r.name AND c.trade_type=r.trade_type AND c.price_units=r.old_units
 AND NOT EXISTS(SELECT 1 FROM products p WHERE p.category_id=c.id AND p.price_unit NOT IN ('unknown',r.unit));
