-- Only newly enrolled objects participate; legacy originals and R19 media stay local.
CREATE TABLE product_upload_restrictions (
 user_id text PRIMARY KEY REFERENCES users(id), blocked boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 0, reason text NOT NULL DEFAULT '', updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE media_files ADD COLUMN resumable boolean NOT NULL DEFAULT false;
ALTER TABLE media_files ADD COLUMN lifecycle_managed boolean NOT NULL DEFAULT false;
ALTER TABLE media_files ADD COLUMN upload_cleanup_requested boolean NOT NULL DEFAULT false;
ALTER TABLE media_files ADD COLUMN upload_offset bigint NOT NULL DEFAULT 0 CHECK(upload_offset>=0);
ALTER TABLE media_files ADD COLUMN upload_security_version bigint NOT NULL DEFAULT 0;
ALTER TABLE media_files ADD COLUMN processing_token text;
ALTER TABLE media_files ADD COLUMN processing_until timestamptz;
ALTER TABLE media_files DROP CONSTRAINT media_files_purpose_check;
ALTER TABLE media_files ADD CONSTRAINT media_files_purpose_check CHECK(purpose IN ('product','chat','avatar','contact'));
CREATE TABLE media_stage_candidates (
 object_key text PRIMARY KEY, media_id text NOT NULL REFERENCES media_files(id),variant text NOT NULL CHECK(variant IN ('original','preview')),
 created_at timestamptz NOT NULL DEFAULT now(),expires_at timestamptz NOT NULL DEFAULT now()+interval '5 days'
);
CREATE INDEX media_stage_candidates_expiry_idx ON media_stage_candidates(expires_at);
CREATE TABLE media_objects (
 media_id text NOT NULL REFERENCES media_files(id), variant text NOT NULL CHECK(variant IN ('original','preview')),
 product_id text REFERENCES products(id), store text NOT NULL CHECK(store IN ('staging','oss')),
 object_key text NOT NULL UNIQUE, mime_type text NOT NULL, byte_size bigint NOT NULL CHECK(byte_size>0),
 sha256 text NOT NULL CHECK(sha256 ~ '^[0-9a-f]{64}$'), generation bigint NOT NULL DEFAULT 1,
 retain_until timestamptz DEFAULT now()+interval '5 days', deleted_at timestamptz,
 force_delete boolean NOT NULL DEFAULT false, retry_at timestamptz NOT NULL DEFAULT now(),
 attempts integer NOT NULL DEFAULT 0, last_error text NOT NULL DEFAULT '', updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(media_id,variant)
);
CREATE INDEX media_objects_due_idx ON media_objects(retry_at,retain_until) WHERE deleted_at IS NULL;
CREATE INDEX media_objects_product_idx ON media_objects(product_id) WHERE deleted_at IS NULL;
-- Persist target keys before network I/O, including targets of failed transfers.
CREATE TABLE media_object_moves (
 media_id text NOT NULL REFERENCES media_files(id), variant text NOT NULL,
 source_key text NOT NULL, destination text NOT NULL CHECK(destination IN ('staging','oss')),
 destination_key text NOT NULL UNIQUE, completed boolean NOT NULL DEFAULT false,
 lease_token text,lease_until timestamptz,
 ever_started boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(source_key,destination)
);
CREATE INDEX media_object_moves_pending_idx ON media_object_moves(media_id,variant) WHERE NOT completed;
CREATE TABLE media_object_cleanup (
 store text NOT NULL CHECK(store IN ('staging','oss')), object_key text NOT NULL,
 media_id text NOT NULL REFERENCES media_files(id), variant text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), retry_at timestamptz NOT NULL DEFAULT now(),
 attempts integer NOT NULL DEFAULT 0, completed_at timestamptz,
 lease_token text,lease_until timestamptz,
 reconcile boolean NOT NULL DEFAULT false,
 PRIMARY KEY(store,object_key)
);
CREATE INDEX media_object_cleanup_due_idx ON media_object_cleanup(retry_at,created_at) WHERE completed_at IS NULL OR reconcile;
ALTER TABLE products DROP CONSTRAINT products_status_check;
ALTER TABLE products ADD CONSTRAINT products_status_check CHECK(status IN ('draft','pending','publishing','published','rejected','rejected_final','offline','sold','rented'));

-- The row lock spans association and lifecycle changes, so deletion cannot race re-use.
CREATE FUNCTION r20_media_associate() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM media_objects WHERE media_id=NEW.media_id) THEN
  PERFORM 1 FROM media_objects WHERE media_id=NEW.media_id ORDER BY variant FOR UPDATE;
  IF EXISTS(SELECT 1 FROM media_objects WHERE media_id=NEW.media_id AND (deleted_at IS NOT NULL OR force_delete OR (product_id IS NOT NULL AND product_id<>NEW.product_id))) THEN
   RAISE EXCEPTION 'managed media unavailable' USING ERRCODE='23514';
  END IF;
  UPDATE media_objects SET product_id=NEW.product_id WHERE media_id=NEW.media_id;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER r20_media_associate BEFORE INSERT ON product_images FOR EACH ROW EXECUTE FUNCTION r20_media_associate();

CREATE FUNCTION r20_media_detach() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM media_objects WHERE media_id=OLD.media_id AND product_id=OLD.product_id ORDER BY variant FOR UPDATE;
 UPDATE media_objects SET product_id=NULL,
  retain_until=coalesce(retain_until,now()+interval '5 days'),retry_at=now(),updated_at=now()
 WHERE media_id=OLD.media_id AND product_id=OLD.product_id AND deleted_at IS NULL;
 RETURN OLD;
END $$;
CREATE TRIGGER r20_media_detach AFTER DELETE ON product_images FOR EACH ROW EXECUTE FUNCTION r20_media_detach();

CREATE FUNCTION r20_product_media_state() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.status='rejected_final' AND NEW.status<>OLD.status THEN
  RAISE EXCEPTION 'final rejection is terminal' USING ERRCODE='23514';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM media_objects WHERE product_id=NEW.id) THEN RETURN NEW; END IF;
 PERFORM 1 FROM media_objects WHERE product_id=NEW.id AND deleted_at IS NULL ORDER BY media_id,variant FOR UPDATE;
 IF NEW.status='published' THEN
  IF EXISTS(SELECT 1 FROM product_images pi JOIN media_objects o ON o.media_id=pi.media_id WHERE pi.product_id=NEW.id AND (o.deleted_at IS NOT NULL OR o.force_delete)) THEN
   RAISE EXCEPTION 'managed product images expired' USING ERRCODE='23514';
  END IF;
  IF EXISTS(SELECT 1 FROM product_images pi JOIN media_objects o ON o.media_id=pi.media_id WHERE pi.product_id=NEW.id AND o.store<>'oss') THEN
   NEW.status:='publishing'; NEW.approved_version:=NEW.version;
   -- First-publication time is assigned only after all images are verified in OSS.
   NEW.published_at:=OLD.published_at;
  END IF;
 END IF;
 IF NEW.status IS DISTINCT FROM OLD.status OR NEW.deleted_at IS DISTINCT FROM OLD.deleted_at THEN
  UPDATE media_objects SET retain_until=CASE
   WHEN NEW.deleted_at IS NOT NULL AND NEW.deleted_by_admin THEN NULL
   WHEN NEW.status IN ('publishing','published','sold','rented') THEN NULL
   WHEN NEW.status='rejected_final' OR NEW.deleted_at IS NOT NULL THEN now()
   WHEN NEW.status='rejected' AND OLD.status='pending' THEN now()+interval '5 days'
   ELSE coalesce(retain_until,now()+interval '5 days') END,
   force_delete=force_delete OR NEW.status='rejected_final' OR (NEW.deleted_at IS NOT NULL AND NOT NEW.deleted_by_admin),
   retry_at=now(),last_error='',updated_at=now()
   WHERE product_id=NEW.id AND deleted_at IS NULL;
  -- Keep the user-visible draft deadline aligned with an existing image grace;
  -- ordinary text edits do not create a new retention window.
  IF NEW.source='member' AND NEW.published_at IS NULL AND NEW.legacy_id IS NULL
   AND NEW.deleted_at IS NULL AND NEW.status IN ('draft','pending','rejected') THEN
   SELECT greatest(NEW.draft_expires_at,max(retain_until)) INTO NEW.draft_expires_at
    FROM media_objects WHERE product_id=NEW.id AND deleted_at IS NULL AND NOT force_delete;
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER r20_product_media_state BEFORE UPDATE ON products FOR EACH ROW EXECUTE FUNCTION r20_product_media_state();
