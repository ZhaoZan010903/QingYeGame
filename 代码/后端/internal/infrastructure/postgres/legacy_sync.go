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

const legacyLeaseEnd = `least(heartbeat_at+CASE WHEN progress_stage='commit' THEN interval '2 minutes'
 ELSE interval '90 seconds' END,started_at+interval '21 minutes')`
const freshLegacyBatch = legacyLeaseEnd + ">=now()"
const staleLegacyBatch = "status='running' AND NOT (" + freshLegacyBatch + ")"
const legacyBatchColumns = `id,planned_at,started_at,source_updated_at,
 CASE WHEN ` + staleLegacyBatch + ` THEN ` + legacyLeaseEnd + ` ELSE finished_at END,
 applied_at,rolled_back_at,rollback_until,CASE WHEN ` + staleLegacyBatch + ` THEN 'failed' ELSE status END,source_count,source_digest,summary,
 CASE WHEN ` + staleLegacyBatch + ` THEN '任务中断或超时，请重新发起' ELSE reason END,
 progress_stage,progress_completed,progress_total`

func scanLegacyBatch(row pgx.Row, batch *syncapp.Batch) error {
	return row.Scan(&batch.ID, &batch.PlannedAt, &batch.StartedAt, &batch.SourceUpdated, &batch.FinishedAt,
		&batch.AppliedAt, &batch.RolledBackAt, &batch.RollbackUntil, &batch.Status, &batch.SourceCount,
		&batch.SourceDigest, &batch.Summary, &batch.Reason, &batch.Progress.Stage, &batch.Progress.Completed, &batch.Progress.Total)
}

func legacyConflict(message string) error {
	return fault.New(fault.Conflict, "LEGACY_SYNC_CONFLICT", message)
}

func lockLegacyControls(ctx context.Context, tx pgx.Tx) (syncapp.Controls, error) {
	var c syncapp.Controls
	err := tx.QueryRow(ctx, `SELECT mode='automatic',version,updated_at FROM legacy_sync_control FOR UPDATE`).
		Scan(&c.ScheduleEnabled, &c.Version, &c.UpdatedAt)
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

func (s LegacySyncStore) BeginScheduledBatch(ctx context.Context, id string, planned time.Time) (syncapp.Batch, bool, error) {
	return s.beginBatch(ctx, id, &planned, "")
}

func (s LegacySyncStore) BeginManualBatch(ctx context.Context, id, actor string) (syncapp.Batch, bool, error) {
	if actor == "" {
		return syncapp.Batch{}, false, fault.New(fault.Forbidden, "FORBIDDEN", "需要启用的管理员账号")
	}
	return s.beginBatch(ctx, id, nil, actor)
}

func (s LegacySyncStore) beginBatch(ctx context.Context, id string, planned *time.Time, actor string) (syncapp.Batch, bool, error) {
	var batch syncapp.Batch
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return batch, false, err
	}
	defer tx.Rollback(ctx)
	settings, err := lockLegacyControls(ctx, tx)
	if err != nil || planned != nil && !settings.ScheduleEnabled {
		return batch, false, err
	}
	if actor != "" {
		if err = legacyActor(ctx, tx, actor); err != nil {
			return batch, false, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE legacy_sync_batches
 SET status='failed',finished_at=clock_timestamp(),reason='任务中断或超时，请重新发起'
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
	}

	var busy bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM legacy_sync_batches WHERE status='running')`).Scan(&busy); err != nil {
		return batch, false, err
	}
	if busy {
		if planned != nil {
			return batch, false, tx.Commit(ctx)
		}
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
		if err = legacyAudit(ctx, tx, actor, "legacy.sync.start", id, map[string]string{"trigger": "manual"}); err != nil {
			return batch, false, err
		}
	}
	return batch, true, tx.Commit(ctx)
}

func (s LegacySyncStore) ReportProgress(ctx context.Context, id string, p syncapp.Progress) error {
	tag, err := s.DB.Exec(ctx, `UPDATE legacy_sync_batches SET progress_stage=$2,progress_completed=$3,
 progress_total=$4,heartbeat_at=clock_timestamp() WHERE id=$1 AND status='running' AND `+freshLegacyBatch,
		id, p.Stage, p.Completed, p.Total)
	if err == nil && tag.RowsAffected() != 1 {
		return syncapp.ErrLeaseLost
	}
	return err
}

func (s LegacySyncStore) Heartbeat(ctx context.Context, id string) error {
	tag, err := s.DB.Exec(ctx, `UPDATE legacy_sync_batches SET heartbeat_at=clock_timestamp()
 WHERE id=$1 AND status='running' AND `+freshLegacyBatch, id)
	if err == nil && tag.RowsAffected() != 1 {
		return syncapp.ErrLeaseLost
	}
	return err
}

func saveLegacyItems(ctx context.Context, tx pgx.Tx, id string, items []syncapp.Item) error {
	if len(items) == 0 {
		return nil
	}
	_, err := tx.CopyFrom(ctx, pgx.Identifier{"legacy_sync_items"}, []string{
		"batch_id", "legacy_id", "product_code", "action", "source_price", "source_updated_at",
		"notices", "before_state", "after_state", "ownership", "blocking",
	}, pgx.CopyFromSlice(len(items), func(index int) ([]any, error) {
		i := items[index]
		return []any{id, i.LegacyID, i.Code, i.Action, i.SourcePrice, i.SourceUpdatedAt,
			i.Notices, i.Before, i.After, i.Ownership, i.Blocking}, nil
	}))
	return err
}

func (s LegacySyncStore) CloseBatch(ctx context.Context, batch syncapp.Batch, items []syncapp.Item) (syncapp.Batch, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return batch, err
	}
	defer tx.Rollback(ctx)
	var saved syncapp.Batch
	err = scanLegacyBatch(tx.QueryRow(ctx, `UPDATE legacy_sync_batches SET status=$2,reason=$3,
 finished_at=clock_timestamp(),source_updated_at=$4,source_count=$5,source_digest=$6,summary=$7
 WHERE id=$1 AND status='running' RETURNING `+legacyBatchColumns,
		batch.ID, batch.Status, batch.Reason, batch.SourceUpdated, batch.SourceCount, batch.SourceDigest, batch.Summary), &saved)
	if errors.Is(err, pgx.ErrNoRows) {
		// COMMIT may have succeeded even if the worker lost the database response.
		err = scanLegacyBatch(tx.QueryRow(ctx, `SELECT `+legacyBatchColumns+` FROM legacy_sync_batches WHERE id=$1`, batch.ID), &saved)
		return saved, err
	}
	if err != nil {
		return batch, err
	}
	if err = saveLegacyItems(ctx, tx, batch.ID, items); err != nil {
		return batch, err
	}
	return saved, tx.Commit(ctx)
}

type LegacySyncPage struct {
	Batches  []syncapp.Batch
	Controls syncapp.Controls
	Latest   *syncapp.Batch
	Total    int
}

func (s LegacySyncStore) Batches(ctx context.Context, limit, offset int) (LegacySyncPage, error) {
	page := LegacySyncPage{Batches: []syncapp.Batch{}}
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return page, err
	}
	defer tx.Rollback(ctx)
	if err = tx.QueryRow(ctx, `SELECT mode='automatic',version,updated_at,(SELECT count(*) FROM legacy_sync_batches)
 FROM legacy_sync_control`).Scan(&page.Controls.ScheduleEnabled, &page.Controls.Version, &page.Controls.UpdatedAt, &page.Total); err != nil {
		return page, err
	}
	var latest syncapp.Batch
	err = scanLegacyBatch(tx.QueryRow(ctx, `SELECT `+legacyBatchColumns+` FROM legacy_sync_batches
 WHERE id=coalesce(
   (SELECT id FROM legacy_sync_batches WHERE status='running' AND `+freshLegacyBatch+` LIMIT 1),
   (SELECT id FROM legacy_sync_batches ORDER BY started_at DESC,id DESC LIMIT 1))`), &latest)
	if err == nil {
		page.Latest = &latest
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return page, err
	}
	rows, err := tx.Query(ctx, `SELECT `+legacyBatchColumns+`
 FROM legacy_sync_batches ORDER BY started_at DESC,id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var batch syncapp.Batch
		if err = scanLegacyBatch(rows, &batch); err != nil {
			return page, err
		}
		page.Batches = append(page.Batches, batch)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	return page, tx.Commit(ctx)
}

func scanLegacyItem(row pgx.Row, item *syncapp.Item) error {
	var before, after []byte
	if err := row.Scan(&item.LegacyID, &item.Code, &item.Action, &item.SourcePrice, &item.SourceUpdatedAt,
		&item.Notices, &before, &after, &item.Ownership, &item.Blocking); err != nil {
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
 after_state,ownership,blocking`

func (s LegacySyncStore) Details(ctx context.Context, id, filter string, limit, offset int) (syncapp.Batch, []syncapp.Item, int, error) {
	var batch syncapp.Batch
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return batch, nil, 0, err
	}
	defer tx.Rollback(ctx)
	err = scanLegacyBatch(tx.QueryRow(ctx, `SELECT `+legacyBatchColumns+` FROM legacy_sync_batches WHERE id=$1`, id), &batch)
	if errors.Is(err, pgx.ErrNoRows) {
		return batch, nil, 0, fault.New(fault.NotFound, "NOT_FOUND", "同步批次不存在")
	}
	if err != nil {
		return batch, nil, 0, err
	}
	const filterSQL = ` WHERE batch_id=$1 AND (
 $2='' OR action=$2 OR ($2='issues' AND (action IN ('hold','missing') OR jsonb_array_length(notices)>0))
 OR ($2='offline' AND action='update' AND before_state->>'status'='published' AND after_state->>'status'<>'published'))`
	var total int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM legacy_sync_items`+filterSQL, id, filter).Scan(&total); err != nil {
		return batch, nil, 0, err
	}
	rows, err := tx.Query(ctx, `SELECT `+legacyItemColumns+` FROM legacy_sync_items`+filterSQL+`
 ORDER BY blocking DESC,legacy_id LIMIT $3 OFFSET $4`, id, filter, limit, offset)
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
	if err = rows.Err(); err != nil {
		return batch, nil, 0, err
	}
	return batch, items, total, tx.Commit(ctx)
}

func (s LegacySyncStore) SetSchedule(ctx context.Context, actor string, enabled bool, version int64) (syncapp.Controls, error) {
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
		return settings, legacyConflict("定时设置已变化，请刷新后重试")
	}
	if enabled == settings.ScheduleEnabled {
		return settings, tx.Commit(ctx)
	}
	err = tx.QueryRow(ctx, `UPDATE legacy_sync_control SET mode=CASE WHEN $1 THEN 'automatic' ELSE 'paused' END,
 version=version+1,updated_at=clock_timestamp() RETURNING mode='automatic',version,updated_at`, enabled).
		Scan(&settings.ScheduleEnabled, &settings.Version, &settings.UpdatedAt)
	if err == nil {
		err = legacyAudit(ctx, tx, actor, "legacy.sync.schedule", "", map[string]bool{"enabled": enabled})
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return settings, err
}
