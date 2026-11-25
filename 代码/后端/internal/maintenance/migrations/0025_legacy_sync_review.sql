ALTER TABLE legacy_sync_batches
  ADD COLUMN review_kind text NOT NULL DEFAULT '' CHECK (review_kind IN ('', 'bulk_offline')),
  ADD COLUMN review_of text UNIQUE REFERENCES legacy_sync_batches(id),
  ADD CONSTRAINT legacy_sync_review_origin CHECK (review_of IS NULL OR (review_of <> id AND planned_at IS NULL)),
  DROP CONSTRAINT legacy_sync_batches_status_check,
  ADD CONSTRAINT legacy_sync_batches_status_check
    CHECK (status IN ('running','ready','review','failed','blocked','applied','no_change','rolled_back','superseded','cancelled'));

-- Only 0024 batches with a verified directory digest and the exact, complete
-- guard item set can be continued. An unrelated validation error stays final.
UPDATE legacy_sync_batches b SET review_kind='bulk_offline'
WHERE b.status='blocked' AND b.progress_stage='verify_source' AND b.source_digest<>''
  AND EXISTS (
    SELECT 1 FROM legacy_sync_items i WHERE i.batch_id=b.id
    HAVING count(*)>0 AND b.summary->>'review'=count(*)::text
      AND b.reason=format('本次有 %s 件在售商品拟下架，触发批量下架保护，请核对旧站目录范围',count(*))
      AND bool_and(i.action='hold' AND i.blocking AND i.notices='["旧站目录未显示在售，已触发整批下架保护"]'::jsonb)
  );
