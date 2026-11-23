CREATE TABLE legacy_sync_control (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 mode text NOT NULL DEFAULT 'preview' CHECK(mode IN ('preview','automatic','paused')),
 version bigint NOT NULL DEFAULT 1,
 updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO legacy_sync_control DEFAULT VALUES;

CREATE TABLE legacy_sync_batches (
 id text PRIMARY KEY,
 planned_at timestamptz UNIQUE,
 started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 source_updated_at timestamptz,
 finished_at timestamptz,
 applied_at timestamptz,
 rolled_back_at timestamptz,
 rollback_until timestamptz,
 status text NOT NULL DEFAULT 'running'
   CHECK(status IN ('running','ready','review','failed','applied','rolled_back','superseded')),
 source_count integer NOT NULL DEFAULT 0,
 summary jsonb NOT NULL DEFAULT '{}',
 reason text NOT NULL DEFAULT '',
 applied_by text REFERENCES users(id),
 rolled_back_by text REFERENCES users(id)
);
CREATE INDEX legacy_sync_batches_latest ON legacy_sync_batches(started_at DESC,id DESC);

CREATE TABLE legacy_sync_items (
 batch_id text NOT NULL REFERENCES legacy_sync_batches(id),
 legacy_id text NOT NULL,
 product_code text NOT NULL,
 action text NOT NULL CHECK(action IN ('create','update','hold','missing')),
 source_price text NOT NULL,
 source_updated_at timestamptz,
 notices jsonb NOT NULL DEFAULT '[]',
 before_state jsonb,
 after_state jsonb,
 ownership jsonb NOT NULL DEFAULT '{}',
 PRIMARY KEY(batch_id,legacy_id)
);
CREATE INDEX legacy_sync_items_history ON legacy_sync_items(legacy_id,batch_id) WHERE after_state IS NOT NULL;

-- Source/sha identity is per product because each media record has one product owner.
-- Objects from failed attempts stay reachable by the standard media cleanup worker.
CREATE TABLE legacy_sync_assets (
 media_id text PRIMARY KEY REFERENCES media_files(id),
 legacy_id text NOT NULL,
 source_url text NOT NULL,
 raw_sha256 text NOT NULL CHECK(raw_sha256 ~ '^[0-9a-f]{64}$')
);
CREATE INDEX legacy_sync_assets_url ON legacy_sync_assets(legacy_id,source_url);
CREATE INDEX legacy_sync_assets_sha ON legacy_sync_assets(legacy_id,raw_sha256);

-- A reference to the original media row is enough; no rollback copies of the image.
CREATE TABLE legacy_sync_media_holds (
 batch_id text NOT NULL REFERENCES legacy_sync_batches(id),
 media_id text NOT NULL REFERENCES media_files(id),
 expires_at timestamptz NOT NULL,
 PRIMARY KEY(batch_id,media_id)
);
CREATE INDEX legacy_sync_media_holds_live ON legacy_sync_media_holds(media_id,expires_at);
