package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
)

func (s *ManagementStore) Recommendation(ctx context.Context, actor management.Actor, id string, in management.RecommendationInput) (map[string]any, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, managementStoreError(err)
	}
	defer tx.Rollback(ctx)
	// Hold the actor through commit. Account disable/reset takes an exclusive
	// user lock; existing product writes only acquire compatible FK key locks.
	var actorID string
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 AND enabled AND role='admin' FOR SHARE`, actor.ID).Scan(&actorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fault.New(fault.Forbidden, "FORBIDDEN", "当前管理员已停用或无管理权限")
	}
	if err != nil {
		return nil, managementStoreError(err)
	}
	var old int
	var version int64
	err = tx.QueryRow(ctx, `SELECT recommendation_boost,recommendation_version FROM products WHERE id=$1 AND deleted_at IS NULL AND purged_at IS NULL FOR UPDATE`, id).Scan(&old, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fault.New(fault.NotFound, "NOT_FOUND", "商品不存在或已删除")
	}
	if err != nil {
		return nil, managementStoreError(err)
	}
	if version != in.ExpectedVersion {
		return nil, fault.New(fault.Conflict, "RECOMMENDATION_VERSION_CONFLICT", "推荐配置已变化，请刷新后核对再保存")
	}
	// No content fields or content timestamps are updated. The product remains
	// approved/published exactly as before this independent operational write.
	_, err = tx.Exec(ctx, `UPDATE products SET recommendation_boost=$2,recommendation_version=recommendation_version+1 WHERE id=$1`, id, *in.Boost)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO admin_logs(id,user_id,action,resource_id,detail) VALUES($1,$2,'product.recommendation',$3,$4)`, s.NewID(), actor.ID, id, map[string]any{"previous_boost": old, "boost": *in.Boost, "previous_version": version, "version": version + 1})
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return nil, managementStoreError(err)
	}
	return map[string]any{"data": map[string]any{"id": id, "recommendation_boost": *in.Boost, "recommendation_version": version + 1}}, nil
}
