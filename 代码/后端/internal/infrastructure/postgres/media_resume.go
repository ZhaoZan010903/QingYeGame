package postgres

import (
	"context"
	"qingye/backend/internal/domain/fault"
)

func (m mediaMutation) Progress(ctx context.Context, offset int64, complete bool) error {
	_, err := m.tx.Exec(ctx, `UPDATE media_files SET upload_offset=$2,status=CASE WHEN $3 THEN 'uploaded' ELSE 'pending' END WHERE id=$1`, m.id, offset, complete)
	return err
}
func (m mediaMutation) Renew(ctx context.Context, hash string) error {
	_, err := m.tx.Exec(ctx, `UPDATE media_files SET upload_token_hash=$2,upload_expires_at=now()+interval '15 minutes' WHERE id=$1`, m.id, hash)
	return err
}
func (m mediaMutation) Cancel(ctx context.Context) error {
	var referenced, published bool
	var state, key string
	// The caller holds owner -> media; AppendImages must acquire the same media row.
	if err := m.tx.QueryRow(ctx, `SELECT status,storage_key,
 EXISTS(SELECT 1 FROM product_images WHERE media_id=$1) OR EXISTS(SELECT 1 FROM contacts WHERE qr_media_id=$1)
 OR EXISTS(SELECT 1 FROM chat_message_images WHERE media_id=$1) OR EXISTS(SELECT 1 FROM users WHERE avatar_media_id=$1)
 OR EXISTS(SELECT 1 FROM product_reviews WHERE snapshot->'images' @> jsonb_build_array(jsonb_build_object('id',$1::text))),
 EXISTS(SELECT 1 FROM media_objects WHERE media_id=$1 AND store='oss' AND deleted_at IS NULL)
 FROM media_files WHERE id=$1`, m.id).Scan(&state, &key, &referenced, &published); err != nil {
		return err
	}
	if referenced || published {
		return fault.New(fault.Conflict, "UPLOAD_ALREADY_COMPLETED", "图片已用于商品，请在商品图集中移除")
	}
	if _, err := m.tx.Exec(ctx, `SELECT media_id FROM media_objects WHERE media_id=$1 ORDER BY variant FOR UPDATE`, m.id); err != nil {
		return err
	}
	if _, err := m.tx.Exec(ctx, `UPDATE media_objects SET force_delete=true,retain_until=now(),retry_at=now(),updated_at=now() WHERE media_id=$1 AND deleted_at IS NULL AND store='staging'`, m.id); err != nil {
		return err
	}
	if _, err := m.tx.Exec(ctx, `UPDATE media_stage_candidates SET expires_at=now() WHERE media_id=$1`, m.id); err != nil {
		return err
	}
	// A canceled request may still be unwinding external I/O; retain its lease until it exits.
	_, err := m.tx.Exec(ctx, `UPDATE media_files SET status='failed',upload_cleanup_requested=true,upload_token_hash=NULL,upload_expires_at=now() WHERE id=$1`, m.id)
	if err == nil && state == "ready" && !m.managed {
		_, err = m.tx.Exec(ctx, `INSERT INTO media_gc_queue(media_id,storage_key) VALUES($1,$2) ON CONFLICT DO NOTHING`, m.id, key)
	}
	return err
}
func (m mediaMutation) Claim(ctx context.Context, token string) error {
	_, err := m.tx.Exec(ctx, `UPDATE media_files SET processing_token=$2,processing_until=now()+interval '5 minutes' WHERE id=$1`, m.id, token)
	return err
}
func (m mediaMutation) Release(ctx context.Context) error {
	// Failed attempts are retired promptly; verified current objects remain protected.
	if _, err := m.tx.Exec(ctx, `UPDATE media_stage_candidates c SET expires_at=now() WHERE c.media_id=$1 AND NOT EXISTS(SELECT 1 FROM media_objects o WHERE o.object_key=c.object_key AND o.deleted_at IS NULL)`, m.id); err != nil {
		return err
	}
	_, err := m.tx.Exec(ctx, `UPDATE media_files SET processing_token=NULL,processing_until=NULL WHERE id=$1`, m.id)
	return err
}

// Cleanup is not a new upload: it must remain possible after revocation, while
// an old attempt must never release a newer processor's lease.
func (s *MediaStore) ReleaseProcessing(ctx context.Context, id, surface, owner, token string) error {
	if owner == "" || token == "" {
		return fault.New(fault.Invalid, "INVALID_PROCESSING_LEASE", "图片处理凭据无效")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return mediaDBError(err)
	}
	defer tx.Rollback(ctx)
	if err = lockMemberQuota(ctx, tx, owner); err != nil {
		return mediaDBError(err)
	}
	var current string
	err = tx.QueryRow(ctx, `SELECT coalesce(processing_token,'') FROM media_files WHERE id=$1 AND owner_id=$2 AND upload_audience=$3 AND purpose='product' AND (resumable OR lifecycle_managed) FOR UPDATE`, id, owner, surface).Scan(&current)
	if err != nil {
		return mediaDBError(err)
	}
	if current != token {
		return nil
	}
	if err = (mediaMutation{tx: tx, id: id}).Release(ctx); err != nil {
		return mediaDBError(err)
	}
	return mediaDBError(tx.Commit(ctx))
}
