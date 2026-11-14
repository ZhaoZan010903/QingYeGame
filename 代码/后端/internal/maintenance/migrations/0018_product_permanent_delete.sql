ALTER TABLE products ADD COLUMN purged_at timestamptz;
ALTER TABLE products ADD CONSTRAINT products_purge_tombstone_check
 CHECK(purged_at IS NULL OR (deleted_at IS NOT NULL AND NOT deleted_by_admin));
ALTER TABLE media_objects ADD COLUMN permanent_purge_guard boolean NOT NULL DEFAULT false;

-- Registration is an explicit, checksum-bound import of the completed legacy copy.
-- It does not enable OSS reads or perform any storage mutation.
CREATE TABLE media_legacy_replicas (
 media_id text PRIMARY KEY REFERENCES media_files(id),
 storage_key text NOT NULL, bucket text NOT NULL, object_key text NOT NULL,
 byte_size bigint NOT NULL CHECK(byte_size>0), sha256 text NOT NULL CHECK(sha256 ~ '^[0-9a-f]{64}$'),
 crc64 text NOT NULL CHECK(crc64 ~ '^[0-9]+$'), verified_at timestamptz NOT NULL,
 manifest_sha256 text NOT NULL CHECK(manifest_sha256 ~ '^[0-9a-f]{64}$'),
 registered_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz,
 UNIQUE(bucket,object_key)
);
CREATE TABLE media_retired_keys (
 storage_key text PRIMARY KEY, media_id text NOT NULL REFERENCES media_files(id),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE media_permanent_cleanup (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 media_id text NOT NULL REFERENCES media_files(id),
 store text NOT NULL CHECK(store IN ('local','legacy_oss')),
 storage_key text NOT NULL, derived_key text NOT NULL DEFAULT '', local_planned boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now(),
 retry_at timestamptz NOT NULL DEFAULT now(), attempts integer NOT NULL DEFAULT 0,
 lease_token text, lease_until timestamptz, completed_at timestamptz,
 last_error text NOT NULL DEFAULT '', UNIQUE(store,media_id,storage_key)
);
CREATE INDEX media_permanent_cleanup_due_idx ON media_permanent_cleanup(retry_at,id) WHERE completed_at IS NULL;

-- References serialize with media tombstoning. This includes JSON review images,
-- which have no foreign key, and disabled avatar-library entries.
CREATE FUNCTION permanent_media_reference_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE mid text; ids text[];
BEGIN
 IF TG_TABLE_NAME='product_reviews' THEN
  SELECT coalesce(array_agg(DISTINCT image->>'id' ORDER BY image->>'id'),'{}') INTO ids
   FROM jsonb_array_elements(CASE WHEN jsonb_typeof(NEW.snapshot->'images')='array' THEN NEW.snapshot->'images' ELSE '[]'::jsonb END) image
   WHERE coalesce(image->>'id','')<>'';
 ELSIF TG_TABLE_NAME='users' THEN ids:=ARRAY[NEW.avatar_media_id];
 ELSIF TG_TABLE_NAME='contacts' THEN ids:=ARRAY[NEW.qr_media_id];
 ELSE ids:=ARRAY[NEW.media_id];
 END IF;
 FOREACH mid IN ARRAY ids LOOP
  IF mid IS NULL OR mid='' THEN CONTINUE; END IF;
  PERFORM 1 FROM media_files WHERE id=mid FOR UPDATE;
  IF EXISTS(SELECT 1 FROM media_files m WHERE m.id=mid AND
    EXISTS(SELECT 1 FROM media_retired_keys k WHERE k.storage_key=m.storage_key)) THEN
   RAISE EXCEPTION 'permanently retired media cannot be referenced' USING ERRCODE='23514';
  END IF;
 END LOOP;
 RETURN NEW;
END $$;
CREATE TRIGGER a18_media_reference_guard BEFORE INSERT OR UPDATE OF media_id ON product_images FOR EACH ROW EXECUTE FUNCTION permanent_media_reference_guard();
CREATE TRIGGER a18_media_reference_guard BEFORE INSERT OR UPDATE OF media_id ON chat_message_images FOR EACH ROW EXECUTE FUNCTION permanent_media_reference_guard();
CREATE TRIGGER a18_media_reference_guard BEFORE INSERT OR UPDATE OF media_id ON avatar_library FOR EACH ROW EXECUTE FUNCTION permanent_media_reference_guard();
CREATE TRIGGER a18_media_reference_guard BEFORE INSERT OR UPDATE OF avatar_media_id ON users FOR EACH ROW EXECUTE FUNCTION permanent_media_reference_guard();
CREATE TRIGGER a18_media_reference_guard BEFORE INSERT OR UPDATE OF qr_media_id ON contacts FOR EACH ROW EXECUTE FUNCTION permanent_media_reference_guard();
CREATE TRIGGER a18_media_reference_guard BEFORE INSERT OR UPDATE OF snapshot ON product_reviews FOR EACH ROW EXECUTE FUNCTION permanent_media_reference_guard();

CREATE FUNCTION permanent_media_key_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.storage_key<>'' THEN
  PERFORM pg_advisory_xact_lock(hashtextextended('media-storage-key:'||NEW.storage_key,0));
  IF EXISTS(SELECT 1 FROM media_retired_keys WHERE storage_key=NEW.storage_key) THEN
   RAISE EXCEPTION 'permanently retired media key cannot be reused' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER a18_media_key_guard BEFORE INSERT OR UPDATE OF storage_key ON media_files FOR EACH ROW EXECUTE FUNCTION permanent_media_key_guard();
