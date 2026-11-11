-- Derived, replaceable recommendation data; no browser identities are stored here.
CREATE TABLE product_item_similarities (
 source_id text NOT NULL REFERENCES products(id) ON DELETE CASCADE,
 target_id text NOT NULL REFERENCES products(id) ON DELETE CASCADE,
 score double precision NOT NULL CHECK (score > 0 AND score <= 1),
 common_viewers integer NOT NULL CHECK (common_viewers >= 2),
 batch_id text NOT NULL,
 computed_at timestamptz NOT NULL,
 PRIMARY KEY(source_id,target_id),
 CHECK (source_id <> target_id)
);
CREATE INDEX views_date_visitor_product_idx ON product_views_daily(viewed_date,visitor_hash,product_id);
