CREATE TABLE product_dictionary_groups (
 key text PRIMARY KEY CHECK(key IN ('server','gender','level','kind')),
 label text NOT NULL, version integer NOT NULL DEFAULT 1, sort_order integer NOT NULL
);
CREATE TABLE product_dictionary_options (
 dictionary_key text NOT NULL REFERENCES product_dictionary_groups(key),
 value text NOT NULL, label text NOT NULL, enabled boolean NOT NULL DEFAULT true,
 sort_order integer NOT NULL DEFAULT 0, PRIMARY KEY(dictionary_key,value)
);
INSERT INTO product_dictionary_groups(key,label,sort_order) VALUES
 ('server','游戏区服',1),('gender','角色性别',2),('level','VIP等级',3),('kind','账号结构',4);
-- Merge all templates and observed attributes; no single category is the full dictionary.
WITH choices AS (
 SELECT g->>'key' AS key,o->>'value' AS value,COALESCE(NULLIF(o->>'label',''),o->>'value') AS label,1 AS priority
 FROM categories c CROSS JOIN LATERAL jsonb_array_elements(c.filter_config) g
 CROSS JOIN LATERAL jsonb_array_elements(COALESCE(g->'options','[]')) o
 WHERE g->>'key' IN ('server','gender','level','kind')
 UNION ALL
 SELECT k.key, v.value #>> '{}',v.value #>> '{}',2
 FROM products p CROSS JOIN product_dictionary_groups k
 CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(p.attributes->k.key)='array' THEN p.attributes->k.key
 WHEN p.attributes ? k.key AND p.attributes->k.key<>'null'::jsonb THEN jsonb_build_array(p.attributes->k.key) ELSE '[]'::jsonb END) v(value)
 WHERE jsonb_typeof(v.value) IN ('string','number')
 UNION ALL SELECT * FROM (VALUES ('gender','female','女角色',0),('gender','male','男角色',0),('kind','single','独立账号',0),('kind','linked','双号连体',0)) s
), merged AS (SELECT DISTINCT ON(key,value) key,value,label FROM choices WHERE btrim(value)<>'' ORDER BY key,value,priority,label)
INSERT INTO product_dictionary_options(dictionary_key,value,label,sort_order)
SELECT key,value,label,row_number() OVER(PARTITION BY key ORDER BY value)::integer FROM merged;

ALTER TABLE products ADD COLUMN deleted_at timestamptz;
ALTER TABLE products ADD COLUMN draft_expires_at timestamptz;
ALTER TABLE products ADD COLUMN last_media_upload_at timestamptz;
UPDATE products SET draft_expires_at=GREATEST(created_at,COALESCE((SELECT max(m.created_at) FROM product_images pi JOIN media_files m ON m.id=pi.media_id WHERE pi.product_id=products.id),created_at))+interval '72 hours'
WHERE source='member' AND status='draft' AND published_at IS NULL AND legacy_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM product_reviews r WHERE r.product_id=products.id AND r.decision='published');
CREATE INDEX products_draft_expiry_idx ON products(draft_expires_at,id)
 WHERE deleted_at IS NULL AND status='draft' AND published_at IS NULL AND legacy_id IS NULL;
ALTER TABLE media_files ADD COLUMN managed_upload boolean NOT NULL DEFAULT false;
ALTER TABLE media_files ADD COLUMN ready_at timestamptz;
ALTER TABLE media_files ADD COLUMN deleted_at timestamptz;
CREATE INDEX media_member_quota_idx ON media_files(owner_id,created_at) WHERE managed_upload AND deleted_at IS NULL;
CREATE INDEX media_storage_key_idx ON media_files(storage_key) WHERE deleted_at IS NULL;
CREATE TABLE media_gc_queue (
 media_id text PRIMARY KEY REFERENCES media_files(id), storage_key text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), completed_at timestamptz, attempts integer NOT NULL DEFAULT 0
);

CREATE INDEX media_gc_pending_idx ON media_gc_queue(attempts,created_at,media_id) WHERE completed_at IS NULL;
CREATE INDEX product_reviews_published_history_idx ON product_reviews(product_id) WHERE decision='published';
