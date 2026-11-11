CREATE TABLE IF NOT EXISTS users (
 id text PRIMARY KEY, phone text UNIQUE NOT NULL, nickname text NOT NULL, password_hash text NOT NULL,
 role text NOT NULL DEFAULT 'member' CHECK(role IN ('member','admin')), enabled boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS sessions (
 token_hash text PRIMARY KEY, user_id text NOT NULL REFERENCES users(id), expires_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 audience text NOT NULL DEFAULT 'public' CHECK(audience IN ('public','admin'))
);
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS audience text NOT NULL DEFAULT 'public' CHECK(audience IN ('public','admin'));
CREATE INDEX IF NOT EXISTS sessions_user_idx ON sessions(user_id);
CREATE TABLE IF NOT EXISTS categories (
 id text PRIMARY KEY, name text NOT NULL, trade_type text NOT NULL DEFAULT 'all', price_units jsonb NOT NULL DEFAULT '[]',
 filter_config jsonb NOT NULL DEFAULT '[]', sort_order integer NOT NULL DEFAULT 0, enabled boolean NOT NULL DEFAULT true
);
CREATE TABLE IF NOT EXISTS products (
 id text PRIMARY KEY, legacy_id text UNIQUE, product_code text NOT NULL, owner_id text REFERENCES users(id),
 category_id text NOT NULL REFERENCES categories(id), title text NOT NULL DEFAULT '', trade_type text NOT NULL CHECK(trade_type IN ('sale','rent')),
 price_amount numeric(12,2), price_unit text NOT NULL CHECK(price_unit IN ('unknown','account','hour','day','week','month')),
 attributes jsonb NOT NULL DEFAULT '{}', description text NOT NULL DEFAULT '', status text NOT NULL DEFAULT 'draft' CHECK(status IN ('draft','pending','published','rejected','offline')),
 source text NOT NULL DEFAULT 'member' CHECK(source IN ('member','platform','legacy')), version integer NOT NULL DEFAULT 1,
 approved_version integer, manual_rank integer, review_note text NOT NULL DEFAULT '',
 legacy_price text, legacy_category text, legacy_payload jsonb, verification_status text NOT NULL DEFAULT 'unprovided',
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), published_at timestamptz
);
CREATE INDEX IF NOT EXISTS products_public_idx ON products(status,published_at DESC,id);
CREATE INDEX IF NOT EXISTS products_owner_idx ON products(owner_id,updated_at DESC,id);
CREATE INDEX IF NOT EXISTS products_category_idx ON products(category_id,status);
CREATE TABLE IF NOT EXISTS media_files (
 id text PRIMARY KEY, owner_id text REFERENCES users(id), filename text NOT NULL DEFAULT '', mime_type text NOT NULL DEFAULT '',
 byte_size bigint NOT NULL DEFAULT 0, storage_key text NOT NULL DEFAULT '', source_url text NOT NULL DEFAULT '',
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','uploaded','ready','failed')),
 upload_token_hash text, upload_expires_at timestamptz, sha256 text, created_at timestamptz NOT NULL DEFAULT now(),
 upload_audience text NOT NULL DEFAULT 'public' CHECK(upload_audience IN ('public','admin'))
);
ALTER TABLE media_files ADD COLUMN IF NOT EXISTS upload_audience text NOT NULL DEFAULT 'public' CHECK(upload_audience IN ('public','admin'));
CREATE TABLE IF NOT EXISTS product_images (
 product_id text NOT NULL REFERENCES products(id), media_id text UNIQUE NOT NULL REFERENCES media_files(id), sort_order integer NOT NULL,
 PRIMARY KEY(product_id,media_id)
);
CREATE INDEX IF NOT EXISTS product_images_order ON product_images(product_id,sort_order,media_id);
CREATE TABLE IF NOT EXISTS product_reviews (
 id text PRIMARY KEY, product_id text NOT NULL REFERENCES products(id), reviewer_id text REFERENCES users(id),
 decision text NOT NULL, reason text NOT NULL DEFAULT '', version integer NOT NULL, snapshot jsonb NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS favorites (
 user_id text NOT NULL REFERENCES users(id), product_id text NOT NULL REFERENCES products(id), active boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(user_id,product_id)
);
CREATE TABLE IF NOT EXISTS product_views_daily (
 product_id text NOT NULL REFERENCES products(id), visitor_hash text NOT NULL, viewed_date date NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(product_id,visitor_hash,viewed_date)
);
ALTER TABLE product_views_daily ADD COLUMN IF NOT EXISTS created_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX IF NOT EXISTS favorites_product_active_idx ON favorites(product_id,created_at) WHERE active;
CREATE INDEX IF NOT EXISTS views_product_date_idx ON product_views_daily(product_id,viewed_date);
CREATE TABLE IF NOT EXISTS contacts (
 id text PRIMARY KEY, name text NOT NULL, purpose jsonb NOT NULL DEFAULT '[]', channel text NOT NULL DEFAULT 'wechat',
 contact_value text NOT NULL DEFAULT '', qr_media_id text REFERENCES media_files(id), qr_url text NOT NULL DEFAULT '', hours text NOT NULL DEFAULT '', enabled boolean NOT NULL DEFAULT true
);
CREATE TABLE IF NOT EXISTS content_pages (
 slug text PRIMARY KEY, title text NOT NULL, body text NOT NULL DEFAULT '', published boolean NOT NULL DEFAULT false, version integer NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS site_settings (key text PRIMARY KEY, value jsonb NOT NULL, updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS admin_logs (id text PRIMARY KEY, user_id text REFERENCES users(id), action text NOT NULL, resource_id text NOT NULL DEFAULT '', detail jsonb NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS idempotency (
 user_id text NOT NULL REFERENCES users(id), request_id text NOT NULL, scope text NOT NULL, body_hash text NOT NULL, response jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(user_id,scope,request_id)
);
CREATE TABLE IF NOT EXISTS sms_challenges (
 id text PRIMARY KEY, phone text NOT NULL, purpose text NOT NULL, code_hash text NOT NULL, attempts integer NOT NULL DEFAULT 0,
 expires_at timestamptz NOT NULL, used_at timestamptz, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS product_hot_scores (
 product_id text PRIMARY KEY REFERENCES products(id), score bigint NOT NULL DEFAULT 0, views integer NOT NULL DEFAULT 0,
 favorites integer NOT NULL DEFAULT 0, new_bonus integer NOT NULL DEFAULT 0, batch_id text NOT NULL, computed_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS schema_migrations (
 version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now()
);
