-- New identifiers follow legacy maxima; existing duplicates remain untouched.
CREATE SEQUENCE IF NOT EXISTS product_sale_code_seq;
CREATE SEQUENCE IF NOT EXISTS product_rent_code_seq;
SELECT setval('product_sale_code_seq', GREATEST(COALESCE((SELECT max(substring(product_code from '^QYCH([0-9]+)$')::bigint) FROM products WHERE product_code ~ '^QYCH[0-9]{1,15}$'),0),1), EXISTS(SELECT 1 FROM products WHERE product_code ~ '^QYCH[0-9]{1,15}$'));
SELECT setval('product_rent_code_seq', GREATEST(COALESCE((SELECT max(substring(product_code from '^QYZH([0-9]+)$')::bigint) FROM products WHERE product_code ~ '^QYZH[0-9]{1,15}$'),0),1), EXISTS(SELECT 1 FROM products WHERE product_code ~ '^QYZH[0-9]{1,15}$'));
ALTER TABLE products ADD COLUMN IF NOT EXISTS rental_quotes jsonb NOT NULL DEFAULT '[]';
ALTER TABLE products DROP CONSTRAINT IF EXISTS products_status_check;
ALTER TABLE products ADD CONSTRAINT products_status_check CHECK(status IN ('draft','pending','published','rejected','offline','sold','rented'));
CREATE TABLE IF NOT EXISTS product_private_credentials (
 product_id text PRIMARY KEY REFERENCES products(id), ciphertext bytea NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now()
);
-- Public projection is normalized without overwriting the historical source payload.
UPDATE products SET attributes=jsonb_set(attributes,'{game}','"QQ炫舞"'::jsonb,true);
UPDATE categories SET price_units='["hour","day","week","month"]'::jsonb WHERE trade_type='rent';

-- Preserve only existing manual selections once; runtime never auto-generates slides.
UPDATE site_settings SET value=jsonb_set(value,'{slides}',COALESCE((SELECT jsonb_agg(slide ORDER BY manual_rank,id) FROM (
 SELECT p.id,p.manual_rank,jsonb_build_object('productId',p.id,
 'title',(CASE WHEN p.trade_type='sale' THEN '账号出售 · ' ELSE '账号租赁 · ' END)||p.product_code,
 'description','查看完整账号资料，咨询客服了解详情。',
 'tradeLabel',CASE WHEN p.trade_type='sale' THEN '账号出售' ELSE '账号租赁' END,
 'serverLabel',COALESCE(p.attributes->>'server',''),
 'priceLabel',CASE WHEN p.price_amount IS NULL THEN '咨询报价' ELSE p.price_amount::text||CASE p.price_unit WHEN 'account' THEN ' 元' WHEN 'day' THEN ' 元/日' WHEN 'month' THEN ' 元/月' WHEN 'hour' THEN ' 元/时' WHEN 'week' THEN ' 元/周' ELSE '（单位待核对）' END END,
 'imagePositionX',50,'imagePositionY',50,'imageFit','contain','layout','image-right','textAlign','left','verticalAlign','center') slide
 FROM products p JOIN categories c ON c.id=p.category_id WHERE p.manual_rank IS NOT NULL AND p.status='published' AND p.approved_version=p.version AND c.enabled ORDER BY p.manual_rank,p.id LIMIT 12
 ) selected),'[]'::jsonb),true) WHERE key='home_presentation' AND NOT(value ? 'slides');

CREATE INDEX IF NOT EXISTS products_numeric_code_idx ON products(left(product_code,4),(substring(product_code from '[0-9]+$')::bigint) DESC) WHERE product_code ~ '^QY(CH|ZH)[0-9]{1,15}$';
