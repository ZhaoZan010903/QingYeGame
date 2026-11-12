package postgres

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"qingye/backend/internal/domain/fault"
	domain "qingye/backend/internal/domain/security"
)

type UploadSecurityStore struct {
	DB    *pgxpool.Pool
	NewID func() string
}

// The caller owns member-quota:<id>; both ticket creation and final install recheck.
func checkProductUploadAllowed(ctx context.Context, tx pgx.Tx, owner string) error {
	var blocked bool
	if err := tx.QueryRow(ctx, `SELECT coalesce((SELECT blocked FROM product_upload_restrictions WHERE user_id=$1),false)`, owner).Scan(&blocked); err != nil {
		return mediaDBError(err)
	}
	if blocked {
		return fault.New(fault.Forbidden, "UPLOAD_DISABLED", "账号上传权限已暂停，请联系管理员")
	}
	return nil
}

func (s *UploadSecurityStore) Restriction(ctx context.Context, owner string) (domain.UploadRestriction, error) {
	var out domain.UploadRestriction
	err := s.DB.QueryRow(ctx, `SELECT coalesce(r.blocked,false),coalesce(r.version,0),coalesce(r.reason,'') FROM users u LEFT JOIN product_upload_restrictions r ON r.user_id=u.id WHERE u.id=$1`, owner).Scan(&out.Blocked, &out.Version, &out.Reason)
	return out, mediaDBError(err)
}
func (s *UploadSecurityStore) Restrict(ctx context.Context, actor, owner string, in domain.RestrictInput) (domain.UploadRestriction, error) {
	var out domain.UploadRestriction
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return out, mediaDBError(err)
	}
	defer tx.Rollback(ctx)
	if err = lockMemberQuota(ctx, tx, owner); err != nil {
		return out, mediaDBError(err)
	}
	var member bool
	if err = tx.QueryRow(ctx, `SELECT role='member' FROM users WHERE id=$1`, owner).Scan(&member); err != nil {
		return out, mediaDBError(err)
	}
	if !member {
		return out, fault.New(fault.Invalid, "INVALID_RESTRICTION", "此操作仅适用于会员商品上传")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO product_upload_restrictions(user_id) VALUES($1) ON CONFLICT DO NOTHING`, owner); err != nil {
		return out, mediaDBError(err)
	}
	err = tx.QueryRow(ctx, `UPDATE product_upload_restrictions SET blocked=$2,reason=$3,version=version+1,updated_at=now() WHERE user_id=$1 AND version=$4 RETURNING blocked,version,reason`, owner, in.Blocked, in.Reason, in.ExpectedVersion).Scan(&out.Blocked, &out.Version, &out.Reason)
	if err == pgx.ErrNoRows {
		return out, fault.New(fault.Conflict, "VERSION_CONFLICT", "上传权限已更新，请刷新")
	}
	if err != nil {
		return out, mediaDBError(err)
	}
	if in.Blocked {
		// Revoked tickets stay revoked after unblocking; chat/avatar permissions are untouched.
		_, err = tx.Exec(ctx, `UPDATE media_files SET upload_token_hash=NULL,upload_expires_at=now() WHERE owner_id=$1 AND purpose='product' AND status IN ('pending','uploaded') AND managed_upload AND source_url=''`, owner)
	}
	if err == nil && in.PurgeStaging {
		_, err = tx.Exec(ctx, `UPDATE media_files SET upload_cleanup_requested=true WHERE owner_id=$1 AND purpose='product' AND lifecycle_managed AND managed_upload AND source_url='' AND status IN ('pending','uploaded','failed')`, owner)
		if err != nil {
			return out, mediaDBError(err)
		}
		// Bulk transitions and administrator cleanup take object rows in the same order.
		if _, err = tx.Exec(ctx, `SELECT o.media_id FROM media_objects o JOIN media_files m ON m.id=o.media_id WHERE m.owner_id=$1 AND m.purpose='product' AND m.lifecycle_managed AND o.store='staging' AND o.deleted_at IS NULL ORDER BY o.media_id,o.variant FOR UPDATE OF o`, owner); err != nil {
			return out, mediaDBError(err)
		}
		// Mark only R20 staging. Never recurse through paths, clear OSS, or touch old imports.
		_, err = tx.Exec(ctx, `UPDATE media_objects o SET force_delete=true,retain_until=now(),retry_at=now(),updated_at=now() FROM media_files m WHERE m.id=o.media_id AND m.owner_id=$1 AND m.purpose='product' AND m.managed_upload AND m.lifecycle_managed AND m.source_url='' AND o.store='staging' AND o.deleted_at IS NULL`, owner)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE media_stage_candidates c SET expires_at=now() FROM media_files m WHERE m.id=c.media_id AND m.owner_id=$1 AND m.purpose='product' AND m.managed_upload AND m.lifecycle_managed AND m.source_url=''`, owner)
		}
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO admin_logs(id,user_id,action,resource_id,detail) VALUES($1,$2,'security.product-upload',$3,$4)`, s.NewID(), actor, owner, map[string]any{"blocked": in.Blocked, "purge_staging": in.PurgeStaging, "reason": in.Reason, "version": out.Version})
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return out, mediaDBError(err)
}
