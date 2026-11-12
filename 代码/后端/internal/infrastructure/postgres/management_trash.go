package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
	"strings"
)

func (s *ManagementStore) TrashProduct(ctx context.Context, actor management.Actor, id string, restore bool, in management.TransitionInput) (map[string]any, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, managementStoreError(err)
	}
	defer tx.Rollback(ctx)
	var version int
	var deleted, adminDeleted bool
	var status string
	err = tx.QueryRow(ctx, `SELECT version,deleted_at IS NOT NULL,deleted_by_admin,status FROM products WHERE id=$1 FOR UPDATE`, id).Scan(&version, &deleted, &adminDeleted, &status)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && deleted && !adminDeleted {
		return nil, fault.New(fault.NotFound, "NOT_FOUND", "商品不存在或已被永久清理")
	}
	if err != nil {
		return nil, managementStoreError(err)
	}
	if version != in.ExpectedVersion {
		return nil, fault.New(fault.Conflict, "VERSION_CONFLICT", "商品已更新，请刷新列表后重试")
	}
	if deleted != restore {
		return nil, fault.New(fault.Conflict, "INVALID_STATE", "商品状态已变化，请刷新列表")
	}
	action := "product.trash"
	if restore {
		// Restore to draft, never directly to a public state or a state that skips member review.
		_, err = tx.Exec(ctx, `UPDATE products SET deleted_at=NULL,deleted_by_admin=false,status='draft',approved_version=NULL,version=version+1,updated_at=now(),review_note='',draft_expires_at=CASE WHEN source='member' AND published_at IS NULL AND legacy_id IS NULL THEN now()+interval '72 hours' ELSE NULL END WHERE id=$1`, id)
		action = "product.restore"
	} else {
		// Retain image associations and encrypted credentials; the media GC cannot collect them.
		_, err = tx.Exec(ctx, `UPDATE products SET deleted_at=now(),deleted_by_admin=true,approved_version=NULL,version=version+1,updated_at=now() WHERE id=$1`, id)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO admin_logs(id,user_id,action,resource_id,detail) VALUES($1,$2,$3,$4,$5)`, s.NewID(), actor.ID, action, id, map[string]any{"previous_status": status, "reason": strings.TrimSpace(in.Reason)})
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return nil, managementStoreError(err)
	}
	return map[string]any{"data": map[string]any{"id": id, "version": version + 1, "deleted": !restore}}, nil
}
