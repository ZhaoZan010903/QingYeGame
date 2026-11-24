-- Retire preview publishing during the upgrade. Re-enable the noon rule
-- explicitly after verifying the new catalog-difference rules.
UPDATE legacy_sync_control SET mode='paused',version=version+1,updated_at=clock_timestamp();
ALTER TABLE legacy_sync_control ALTER COLUMN mode SET DEFAULT 'paused';
UPDATE legacy_sync_batches SET status='superseded',reason='旧版预览已停用，请重新同步'
 WHERE status IN ('ready','review');
UPDATE legacy_sync_batches SET status='failed',finished_at=clock_timestamp(),reason='同步服务已升级，请重新同步'
 WHERE status='running';

ALTER TABLE legacy_sync_batches
 DROP CONSTRAINT legacy_sync_batches_status_check,
 ADD CONSTRAINT legacy_sync_batches_status_check
   CHECK(status IN ('running','ready','review','failed','blocked','applied','rolled_back','superseded')),
 ADD COLUMN heartbeat_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 ADD COLUMN progress_stage text NOT NULL DEFAULT 'read_source'
   CHECK(progress_stage IN ('read_source','verify_source','compare','prepare_media','commit','done')),
 ADD COLUMN progress_completed integer NOT NULL DEFAULT 0,
 ADD COLUMN progress_total integer NOT NULL DEFAULT 0,
 ADD CONSTRAINT legacy_sync_progress_bounds CHECK(progress_completed BETWEEN 0 AND progress_total);
UPDATE legacy_sync_batches SET progress_stage='done',progress_completed=1,progress_total=1
 WHERE status IN ('applied','rolled_back');
CREATE UNIQUE INDEX legacy_sync_one_running ON legacy_sync_batches(status) WHERE status='running';

ALTER TABLE legacy_sync_items
 DROP CONSTRAINT legacy_sync_items_action_check,
 ADD CONSTRAINT legacy_sync_items_action_check CHECK(action IN ('create','update','offline','restore','hold','missing')),
 ADD COLUMN blocking boolean NOT NULL DEFAULT false;

-- The marker remains valid only at the version written by sync. A later
-- operator edit, manual offline or recycle action invalidates it.
ALTER TABLE products ADD COLUMN legacy_sync_hidden_version integer;
UPDATE products p SET legacy_sync_hidden_version=p.version
 FROM legacy_sync_items i JOIN legacy_sync_batches b ON b.id=i.batch_id AND b.status='applied'
 WHERE p.source='legacy' AND p.deleted_at IS NULL AND p.status IN ('offline','sold','rented')
   AND p.legacy_id=i.legacy_id AND i.after_state->>'version'=p.version::text
   AND i.after_state->>'status'=p.status AND i.ownership->>'status'='true';
