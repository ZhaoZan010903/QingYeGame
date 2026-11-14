-- Operational recommendation boosts do not invalidate approved product content.
ALTER TABLE products ADD COLUMN recommendation_boost integer NOT NULL DEFAULT 0
 CHECK(recommendation_boost BETWEEN 0 AND 100);
ALTER TABLE products ADD COLUMN recommendation_version bigint NOT NULL DEFAULT 1
 CHECK(recommendation_version > 0);

CREATE OR REPLACE FUNCTION r20_product_media_state() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 -- Independent recommendation writes must not change publication or media.
 -- Keep all prior checks for every other update, including existing no-ops.
 IF NEW.recommendation_version IS DISTINCT FROM OLD.recommendation_version AND
  (to_jsonb(NEW)-ARRAY['recommendation_boost','recommendation_version']) IS NOT DISTINCT FROM
  (to_jsonb(OLD)-ARRAY['recommendation_boost','recommendation_version']) THEN
  RETURN NEW;
 END IF;
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
