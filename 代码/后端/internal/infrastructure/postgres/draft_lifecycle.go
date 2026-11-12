package postgres

import (
	"context"
	"github.com/jackc/pgx/v5"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/member"
)

const DraftLimit = 20
const UploadByteLimit int64 = 2 << 30
const OrphanUploadLimit = 1000

// Ready files get an association grace period while ordered client uploads finish.
const DraftHasActiveUploadsSQL = `EXISTS(SELECT 1 FROM media_files m WHERE m.owner_id=p.owner_id AND m.purpose='product' AND m.deleted_at IS NULL AND (
 (m.status IN ('pending','uploaded') AND m.upload_expires_at>now()) OR
 (m.status='ready' AND m.ready_at>now()-interval '15 minutes' AND NOT EXISTS(SELECT 1 FROM product_images pi WHERE pi.media_id=m.id))))`

func lockMemberQuota(ctx context.Context, tx pgx.Tx, owner string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "member-quota:"+owner)
	return err
}
func checkDraftQuota(ctx context.Context, tx pgx.Tx, owner string) error {
	if err := lockMemberQuota(ctx, tx, owner); err != nil {
		return memberStoreError(err)
	}
	var total int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM products WHERE owner_id=$1 AND source='member' AND published_at IS NULL AND deleted_at IS NULL`, owner).Scan(&total); err != nil {
		return memberStoreError(err)
	}
	if total >= DraftLimit {
		return fault.New(fault.Conflict, "DRAFT_QUOTA_EXCEEDED", "最多保留20份未公开草稿，请先删除不用的草稿")
	}
	return nil
}
func checkUploadQuota(ctx context.Context, tx pgx.Tx, owner, exclude string, additional int64, newTicket bool) error {
	if err := lockMemberQuota(ctx, tx, owner); err != nil {
		return mediaDBError(err)
	}
	var used int64
	var orphan int
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum(m.byte_size),0),count(*) FILTER(WHERE m.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM product_images pi WHERE pi.media_id=m.id) AND NOT EXISTS(SELECT 1 FROM chat_message_images a WHERE a.media_id=m.id) AND NOT EXISTS(SELECT 1 FROM users u WHERE u.avatar_media_id=m.id)) FROM media_files m WHERE m.owner_id=$1 AND m.managed_upload AND m.status<>'failed' AND (m.deleted_at IS NULL OR EXISTS(SELECT 1 FROM media_gc_queue q WHERE q.media_id=m.id AND q.completed_at IS NULL)) AND m.id<>$2 AND NOT EXISTS(SELECT 1 FROM product_images pi JOIN products p ON p.id=pi.product_id WHERE pi.media_id=m.id AND p.published_at IS NOT NULL)`, owner, exclude).Scan(&used, &orphan)
	if err != nil {
		return mediaDBError(err)
	}
	if used+additional > UploadByteLimit || newTicket && orphan >= OrphanUploadLimit {
		return fault.New(fault.Conflict, "UPLOAD_QUOTA_EXCEEDED", "未公开资料、聊天图片与头像合计限2GB，待关联图片最多1000张，请联系管理员处理")
	}
	return nil
}
func (s *MemberStore) DeleteDraft(ctx context.Context, u member.Actor, id string, version int) (map[string]any, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, memberStoreError(err)
	}
	defer tx.Rollback(ctx)
	actual, _, err := lockedOwner(ctx, tx, id, u.ID)
	if err != nil {
		return nil, memberStoreError(err)
	}
	if err = member.CheckVersion(actual, version); err != nil {
		return nil, err
	}
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT status='draft' AND source='member' AND published_at IS NULL AND legacy_id IS NULL AND NOT EXISTS(SELECT 1 FROM product_reviews r WHERE r.product_id=p.id AND r.decision='published') FROM products p WHERE id=$1`, id).Scan(&eligible)
	if err != nil {
		return nil, memberStoreError(err)
	}
	if !eligible {
		return nil, fault.New(fault.Conflict, "DRAFT_NOT_DELETABLE", "仅可删除从未公开的新草稿；历史商品请联系管理员下架")
	}
	if err = DeleteNewDraft(ctx, tx, id); err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return nil, memberStoreError(err)
	}
	return map[string]any{"data": map[string]any{"ok": true}}, nil
}

// Keep a tombstone so codes and idempotency keys cannot silently create a replacement product.
func DeleteNewDraft(ctx context.Context, tx pgx.Tx, id string) error {
	var mediaIDs []string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(array_agg(media_id ORDER BY media_id),'{}') FROM product_images WHERE product_id=$1`, id).Scan(&mediaIDs); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM product_images WHERE product_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM product_private_credentials WHERE product_id=$1`, id); err != nil {
		return err
	}
	for _, mid := range mediaIDs {
		if err := QueueManagedMediaGC(ctx, tx, mid); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `UPDATE products SET deleted_at=now(),version=version+1,approved_version=NULL,title='',description='',attributes='{}',rental_quotes='[]' WHERE id=$1`, id)
	return err
}

func QueueManagedMediaGC(ctx context.Context, tx pgx.Tx, id string) error {
	var key string
	err := tx.QueryRow(ctx, `SELECT storage_key FROM media_files m WHERE id=$1 AND managed_upload AND source_url='' AND deleted_at IS NULL FOR UPDATE OF m`, id).Scan(&key)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	var referenced bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_images WHERE media_id=$1) OR EXISTS(SELECT 1 FROM chat_message_images WHERE media_id=$1) OR EXISTS(SELECT 1 FROM users WHERE avatar_media_id=$1) OR EXISTS(SELECT 1 FROM contacts WHERE qr_media_id=$1) OR EXISTS(SELECT 1 FROM product_reviews WHERE snapshot->'images' @> jsonb_build_array(jsonb_build_object('id',$1::text))) OR EXISTS(SELECT 1 FROM media_files WHERE storage_key=$2 AND id<>$1 AND deleted_at IS NULL)`, id, key).Scan(&referenced)
	if err != nil {
		return err
	}
	if referenced {
		return nil
	}
	if _, err = tx.Exec(ctx, `INSERT INTO media_gc_queue(media_id,storage_key) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, key); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE media_files SET deleted_at=now(),upload_token_hash=NULL WHERE id=$1`, id)
	return err
}

// Caller holds the product row. Serialize with ticket creation/completion, then
// recheck in a fresh statement so a newly committed upload protects its draft.
func DraftCanExpire(ctx context.Context, tx pgx.Tx, id string) (bool, error) {
	var owner string
	if err := tx.QueryRow(ctx, `SELECT coalesce(owner_id,'') FROM products WHERE id=$1`, id).Scan(&owner); err != nil {
		return false, err
	}
	if err := lockMemberQuota(ctx, tx, owner); err != nil {
		return false, err
	}
	var eligible bool
	err := tx.QueryRow(ctx, `SELECT p.deleted_at IS NULL AND p.status='draft' AND p.source='member' AND p.published_at IS NULL AND p.legacy_id IS NULL AND p.draft_expires_at<=now()
 AND NOT EXISTS(SELECT 1 FROM product_reviews r WHERE r.product_id=p.id AND r.decision='published')
 AND NOT `+DraftHasActiveUploadsSQL+`
 FROM products p WHERE p.id=$1`, id).Scan(&eligible)
	return eligible, err
}
