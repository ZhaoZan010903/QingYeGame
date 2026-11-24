package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	syncapp "qingye/backend/internal/application/legacysync"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/infrastructure/security"
)

type LegacySyncStore struct{ DB *pgxpool.Pool }

const freshLegacyBatch = `started_at>=now()-interval '30 minutes'`
const staleLegacyBatch = `status='running' AND NOT (` + freshLegacyBatch + `)`
const legacyBatchColumns = `id,planned_at,started_at,source_updated_at,finished_at,applied_at,rolled_back_at,
 rollback_until,CASE WHEN ` + staleLegacyBatch + ` THEN 'failed' ELSE status END,source_count,summary,
 CASE WHEN ` + staleLegacyBatch + ` THEN '任务中断或超时' ELSE reason END`

func scanLegacyBatch(row pgx.Row, batch *syncapp.Batch) error {
	return row.Scan(&batch.ID, &batch.PlannedAt, &batch.StartedAt, &batch.SourceUpdated, &batch.FinishedAt,
		&batch.AppliedAt, &batch.RolledBackAt, &batch.RollbackUntil, &batch.Status, &batch.SourceCount,
		&batch.Summary, &batch.Reason)
}

func legacyConflict(message string) error {
	return fault.New(fault.Conflict, "LEGACY_SYNC_CONFLICT", message)
}

func lockLegacyControls(ctx context.Context, tx pgx.Tx) (syncapp.Controls, error) {
	var c syncapp.Controls
	err := tx.QueryRow(ctx, `SELECT mode,version,updated_at FROM legacy_sync_control FOR UPDATE`).Scan(&c.Mode, &c.Version, &c.UpdatedAt)
	return c, err
}

func legacyActor(ctx context.Context, tx pgx.Tx, id string) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT enabled AND role='admin' FROM users WHERE id=$1 FOR SHARE`, id).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !valid {
		return fault.New(fault.Forbidden, "FORBIDDEN", "需要启用的管理员账号")
	}
	return err
}

func legacyAudit(ctx context.Context, tx pgx.Tx, actor, action, target string, detail any) error {
	_, err := tx.Exec(ctx, `INSERT INTO admin_logs(id,user_id,action,resource_id,detail)
 VALUES($1,nullif($2,''),$3,$4,$5)`, security.NewID(), actor, action, target, detail)
	return err
}

func (s LegacySyncStore) BeginBatch(ctx context.Context, id string, planned *time.Time) (syncapp.Batch, bool, error) {
	return s.beginBatch(ctx, id, planned, "", 0)
}

func (s LegacySyncStore) BeginManualBatch(ctx context.Context, id, actor string, version int64) (syncapp.Batch, bool, error) {
	if actor == "" {
		return syncapp.Batch{}, false, fault.New(fault.Forbidden, "FORBIDDEN", "需要启用的管理员账号")
	}
	return s.beginBatch(ctx, id, nil, actor, version)
}

func (s LegacySyncStore) beginBatch(ctx context.Context, id string, planned *time.Time, actor string, version int64) (syncapp.Batch, bool, error) {
	var batch syncapp.Batch
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return batch, false, err
	}
	defer tx.Rollback(ctx)
	settings, err := lockLegacyControls(ctx, tx)
	if err != nil {
		return batch, false, err
	}
	if planned != nil && settings.Mode == syncapp.Paused {
		return batch, false, nil
	}
	if actor != "" {
		if err = legacyActor(ctx, tx, actor); err != nil {
			return batch, false, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE legacy_sync_batches
 SET status='failed',finished_at=clock_timestamp(),reason='任务中断或超时'
 WHERE `+staleLegacyBatch); err != nil {
		return batch, false, err
	}
	if actor != "" {
		err = scanLegacyBatch(tx.QueryRow(ctx, `SELECT `+legacyBatchColumns+`
 FROM legacy_sync_batches b WHERE b.id=$1 AND b.planned_at IS NULL AND EXISTS(
 SELECT 1 FROM admin_logs a WHERE a.resource_id=b.id AND a.action='legacy.sync.start' AND a.user_id=$2)`,
			id, actor), &batch)
		if err == nil {
			return batch, false, tx.Commit(ctx)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return batch, false, err
		}
		if settings.Version != version {
			return batch, false, legacyConflict("同步方式已变化，请刷新后重新确认")
		}
		if settings.Mode == syncapp.Paused {
			return batch, false, legacyConflict("同步已暂停，请先恢复同步")
		}
	}
	var busy bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM legacy_sync_batches WHERE status='running')`).Scan(&busy); err != nil {
		return batch, false, err
	}
	if busy {
		return batch, false, syncapp.ErrBusy
	}
	err = scanLegacyBatch(tx.QueryRow(ctx, `INSERT INTO legacy_sync_batches(id,planned_at) VALUES($1,$2)
 ON CONFLICT DO NOTHING RETURNING `+legacyBatchColumns, id, planned), &batch)
	if errors.Is(err, pgx.ErrNoRows) {
		if actor != "" {
			return batch, false, legacyConflict("该同步请求标识已被使用，请刷新记录")
		}
		return batch, false, tx.Commit(ctx)
	}
	if err != nil {
		return batch, false, err
	}
	if actor != "" {
		if err = legacyAudit(ctx, tx, actor, "legacy.sync.start", id, map[string]string{"mode": settings.Mode}); err != nil {
			return batch, false, err
		}
	}
	batch.Mode = settings.Mode
	return batch, true, tx.Commit(ctx)
}

func (s LegacySyncStore) FailBatch(ctx context.Context, id, reason string) error {
	_, err := s.DB.Exec(ctx, `UPDATE legacy_sync_batches SET status='failed',reason=$2,finished_at=clock_timestamp()
 WHERE id=$1 AND status='running'`, id, reason)
	return err
}

func (s LegacySyncStore) SavePreview(ctx context.Context, batch syncapp.Batch, items []syncapp.Item) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = lockLegacyControls(ctx, tx); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE legacy_sync_batches SET status=$2,finished_at=clock_timestamp(),
 source_updated_at=$3,source_count=$4,summary=$5,reason=$6 WHERE id=$1 AND status='running'`,
		batch.ID, batch.Status, batch.SourceUpdated, batch.SourceCount, batch.Summary, batch.Reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return legacyConflict("此批次已关闭")
	}
	if _, err = tx.Exec(ctx, `UPDATE legacy_sync_batches SET status='superseded',reason='已生成较新的对照批次'
 WHERE id<>$1 AND status IN ('ready','review')`, batch.ID); err != nil {
		return err
	}
	if len(items) > 0 {
		_, err = tx.CopyFrom(ctx, pgx.Identifier{"legacy_sync_items"}, []string{
			"batch_id", "legacy_id", "product_code", "action", "source_price", "source_updated_at",
			"notices", "before_state", "after_state", "ownership",
		}, pgx.CopyFromSlice(len(items), func(index int) ([]any, error) {
			i := items[index]
			return []any{batch.ID, i.LegacyID, i.Code, i.Action, i.SourcePrice, i.SourceUpdatedAt,
				i.Notices, i.Before, i.After, i.Ownership}, nil
		}))
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s LegacySyncStore) Batches(ctx context.Context, limit, offset int) ([]syncapp.Batch, syncapp.Controls, int, error) {
	var settings syncapp.Controls
	var total int
	if err := s.DB.QueryRow(ctx, `SELECT mode,version,updated_at,(SELECT count(*) FROM legacy_sync_batches),
 coalesce((SELECT id FROM legacy_sync_batches WHERE status='running' AND `+freshLegacyBatch+`
 ORDER BY started_at DESC LIMIT 1),'') FROM legacy_sync_control`).
		Scan(&settings.Mode, &settings.Version, &settings.UpdatedAt, &total, &settings.ActiveBatchID); err != nil {
		return nil, settings, 0, err
	}
	rows, err := s.DB.Query(ctx, `SELECT `+legacyBatchColumns+`
 FROM legacy_sync_batches ORDER BY started_at DESC,id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, settings, 0, err
	}
	defer rows.Close()
	out := []syncapp.Batch{}
	for rows.Next() {
		var batch syncapp.Batch
		if err = scanLegacyBatch(rows, &batch); err != nil {
			return nil, settings, 0, err
		}
		out = append(out, batch)
	}
	return out, settings, total, rows.Err()
}

func scanLegacyItem(row pgx.Row, item *syncapp.Item) error {
	var before, after []byte
	if err := row.Scan(&item.LegacyID, &item.Code, &item.Action, &item.SourcePrice, &item.SourceUpdatedAt,
		&item.Notices, &before, &after, &item.Ownership); err != nil {
		return err
	}
	if len(before) > 0 {
		if err := json.Unmarshal(before, &item.Before); err != nil {
			return err
		}
	}
	if len(after) > 0 {
		return json.Unmarshal(after, &item.After)
	}
	return nil
}

const legacyItemColumns = `legacy_id,product_code,action,source_price,source_updated_at,notices,before_state,
 after_state,ownership`

func (s LegacySyncStore) Details(ctx context.Context, id string, limit, offset int) (syncapp.Batch, []syncapp.Item, int, error) {
	var batch syncapp.Batch
	err := scanLegacyBatch(s.DB.QueryRow(ctx, `SELECT `+legacyBatchColumns+` FROM legacy_sync_batches WHERE id=$1`, id), &batch)
	if errors.Is(err, pgx.ErrNoRows) {
		return batch, nil, 0, fault.New(fault.NotFound, "NOT_FOUND", "同步批次不存在")
	}
	if err != nil {
		return batch, nil, 0, err
	}
	var total int
	if err = s.DB.QueryRow(ctx, `SELECT count(*) FROM legacy_sync_items WHERE batch_id=$1`, id).Scan(&total); err != nil {
		return batch, nil, 0, err
	}
	rows, err := s.DB.Query(ctx, `SELECT `+legacyItemColumns+`
 FROM legacy_sync_items WHERE batch_id=$1 ORDER BY
 CASE WHEN action IN ('hold','missing') OR jsonb_array_length(notices)>0 THEN 0 ELSE 1 END,legacy_id
 LIMIT $2 OFFSET $3`, id, limit, offset)
	if err != nil {
		return batch, nil, 0, err
	}
	defer rows.Close()
	items := []syncapp.Item{}
	for rows.Next() {
		var item syncapp.Item
		if err := scanLegacyItem(rows, &item); err != nil {
			return batch, nil, 0, err
		}
		items = append(items, item)
	}
	return batch, items, total, rows.Err()
}

func (s LegacySyncStore) SetMode(ctx context.Context, actor, mode string, version int64) (syncapp.Controls, error) {
	if mode != syncapp.Preview && mode != syncapp.Automatic && mode != syncapp.Paused {
		return syncapp.Controls{}, fault.New(fault.Invalid, "INVALID_MODE", "同步方式无效")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return syncapp.Controls{}, err
	}
	defer tx.Rollback(ctx)
	settings, err := lockLegacyControls(ctx, tx)
	if err != nil {
		return settings, err
	}
	if err = legacyActor(ctx, tx, actor); err != nil {
		return settings, err
	}
	if version != settings.Version {
		return settings, legacyConflict("同步设置已变化，请刷新后重试")
	}
	if mode == syncapp.Automatic {
		var firstConfirmed bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM legacy_sync_batches
 WHERE status='applied' AND applied_by IS NOT NULL)`).Scan(&firstConfirmed); err != nil {
			return settings, err
		}
		if !firstConfirmed {
			return settings, legacyConflict("先人工核对并发布一次同步批次")
		}
	}
	err = tx.QueryRow(ctx, `UPDATE legacy_sync_control SET mode=$1,version=version+1,updated_at=clock_timestamp()
 RETURNING mode,version,updated_at`, mode).Scan(&settings.Mode, &settings.Version, &settings.UpdatedAt)
	if err == nil {
		err = legacyAudit(ctx, tx, actor, "legacy.sync.mode", "", map[string]string{"mode": mode})
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return settings, err
}
