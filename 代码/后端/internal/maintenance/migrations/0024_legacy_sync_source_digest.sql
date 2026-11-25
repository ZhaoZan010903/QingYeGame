ALTER TABLE legacy_sync_batches
  ADD COLUMN source_digest text NOT NULL DEFAULT ''
  CHECK (source_digest = '' OR source_digest ~ '^[0-9a-f]{64}$');

ALTER TABLE legacy_sync_batches
  DROP CONSTRAINT legacy_sync_batches_status_check,
  ADD CONSTRAINT legacy_sync_batches_status_check
    CHECK(status IN ('running','ready','review','failed','blocked','applied','no_change','rolled_back','superseded'));

-- URL aliases are lookup hints; only the asset's original media row owns storage.
CREATE TABLE legacy_sync_asset_urls (
  media_id text NOT NULL REFERENCES legacy_sync_assets(media_id) ON DELETE CASCADE,
  source_url text NOT NULL,
  PRIMARY KEY (media_id,source_url)
);
