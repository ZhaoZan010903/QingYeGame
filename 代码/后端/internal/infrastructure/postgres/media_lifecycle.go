package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"qingye/backend/internal/application/medialifecycle"
	"qingye/backend/internal/domain/fault"
)

type MediaLifecycleStore struct {
	DB    *pgxpool.Pool
	NewID func() string
}

// Queue local raw deletion in the same commit as Ready. A process crash after
// that commit must not leave a second untracked copy in uploads/ forever.
func QueueCompletedUploadRawTx(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `INSERT INTO media_gc_queue(media_id,storage_key) SELECT id,'uploads/'||id||'.raw' FROM media_files WHERE id=$1 AND lifecycle_managed AND status='ready' AND deleted_at IS NULL ON CONFLICT DO NOTHING`, id)
	return err
}

func RegisterMediaObjectTx(ctx context.Context, tx pgx.Tx, id, variant, key, mime, sha string, size int64) error {
	if variant != "original" && variant != "preview" {
		return fmt.Errorf("invalid media variant")
	}
	if _, err := medialifecycle.NextKey(key, "check"); err != nil {
		return err
	}
	parts := strings.Split(key, "/")
	if parts[3] != id || parts[5] != variant {
		return fmt.Errorf("object identity does not match media")
	}
	var eligible bool
	if err := tx.QueryRow(ctx, `SELECT lifecycle_managed AND managed_upload AND purpose='product' AND source_url='' AND deleted_at IS NULL FROM media_files WHERE id=$1`, id).Scan(&eligible); err != nil {
		return err
	}
	if !eligible {
		return fault.New(fault.Conflict, "MEDIA_NOT_MANAGED", "图片不属于新商品上传")
	}
	var retired bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_object_cleanup WHERE object_key=$1)`, key).Scan(&retired); err != nil {
		return err
	}
	if retired {
		return fault.New(fault.Conflict, "MEDIA_GENERATION_RETIRED", "图片存储版本已关闭")
	}
	_, err := tx.Exec(ctx, `INSERT INTO media_objects(media_id,variant,store,object_key,mime_type,byte_size,sha256) VALUES($1,$2,'staging',$3,$4,$5,$6)`, id, variant, key, mime, size, sha)
	if err == nil {
		_, err = tx.Exec(ctx, `DELETE FROM media_stage_candidates WHERE object_key=$1 AND media_id=$2 AND variant=$3`, key, id, variant)
	}
	return err
}

func (s *MediaLifecycleStore) StageCandidate(ctx context.Context, id, token, variant, key, mime, sha string, size int64) error {
	if _, err := medialifecycle.NextKey(key, "check"); err != nil {
		return err
	}
	parts := strings.Split(key, "/")
	if token == "" || parts[3] != id || parts[5] != variant {
		return fault.New(fault.Conflict, "UPLOAD_CLOSED", "上传状态已变化")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var owner string
	if err = tx.QueryRow(ctx, `SELECT coalesce(owner_id,'') FROM media_files WHERE id=$1`, id).Scan(&owner); err != nil {
		return mediaDBError(err)
	}
	// Purge and candidate registration serialize before any external PUT.
	if err = lockMemberQuota(ctx, tx, owner); err != nil {
		return mediaDBError(err)
	}
	var securityVersion int64
	err = tx.QueryRow(ctx, `SELECT upload_security_version FROM media_files WHERE id=$1 AND purpose='product' AND lifecycle_managed AND managed_upload AND source_url=''
 AND deleted_at IS NULL AND status='uploaded' AND NOT upload_cleanup_requested AND created_at>now()-interval '5 days'
 AND processing_token=$2 AND processing_until>now() FOR UPDATE`, id, token).Scan(&securityVersion)
	if err == pgx.ErrNoRows {
		return fault.New(fault.Conflict, "UPLOAD_CLOSED", "上传状态已变化")
	}
	if err != nil {
		return mediaDBError(err)
	}
	if err = checkProductUploadAllowed(ctx, tx, owner); err != nil {
		return err
	}
	var currentVersion int64
	if err = tx.QueryRow(ctx, `SELECT coalesce((SELECT version FROM product_upload_restrictions WHERE user_id=$1),0)`, owner).Scan(&currentVersion); err != nil {
		return mediaDBError(err)
	}
	if securityVersion != currentVersion {
		return fault.New(fault.Forbidden, "UPLOAD_REVOKED", "此上传已失效，请重新上传")
	}
	tag, err := tx.Exec(ctx, `INSERT INTO media_stage_candidates(object_key,media_id,variant) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, key, id, variant)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.New(fault.Conflict, "UPLOAD_CLOSED", "上传状态已变化")
	}
	return mediaDBError(tx.Commit(ctx))
}

func (s *MediaLifecycleStore) ReadObject(ctx context.Context, id, variant string) (medialifecycle.Object, error) {
	var o medialifecycle.Object
	err := s.DB.QueryRow(ctx, `SELECT media_id,store,object_key,mime_type,sha256,byte_size FROM media_objects WHERE media_id=$1 AND variant=$2 AND deleted_at IS NULL AND NOT force_delete`, id, variant).Scan(&o.MediaID, &o.Store, &o.Key, &o.MIME, &o.SHA256, &o.Size)
	return o, err
}

type MediaSyncStatus struct {
	Interrupted bool       `json:"interrupted"`
	Version     int        `json:"version"`
	Status      string     `json:"status"`
	Total       int        `json:"total"`
	Ready       int        `json:"ready"`
	Failed      int        `json:"failed"`
	Attempts    int        `json:"attempts"`
	Error       string     `json:"error,omitempty"`
	NextRetry   *time.Time `json:"next_retry,omitempty"`
}

func (s *MediaLifecycleStore) Status(ctx context.Context, product string) (MediaSyncStatus, error) {
	var out MediaSyncStatus
	err := s.DB.QueryRow(ctx, `SELECT p.version,p.status,coalesce(bool_or(o.force_delete OR o.deleted_at IS NOT NULL) FILTER(WHERE o.media_id IS NOT NULL),false),count(o.media_id),count(o.media_id) FILTER(WHERE o.store='oss' AND o.deleted_at IS NULL),count(o.media_id) FILTER(WHERE o.last_error<>''),coalesce(max(o.attempts),0),coalesce(max(o.last_error),''),min(o.retry_at) FILTER(WHERE o.last_error<>'' AND isfinite(o.retry_at)) FROM products p LEFT JOIN product_images pi ON pi.product_id=p.id LEFT JOIN media_objects o ON o.media_id=pi.media_id WHERE p.id=$1 AND p.deleted_at IS NULL GROUP BY p.id`, product).Scan(&out.Version, &out.Status, &out.Interrupted, &out.Total, &out.Ready, &out.Failed, &out.Attempts, &out.Error, &out.NextRetry)
	return out, mediaDBError(err)
}
func (s *MediaLifecycleStore) Retry(ctx context.Context, actor, product string, expectedVersion int) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return mediaDBError(err)
	}
	defer tx.Rollback(ctx)
	var version int
	var status string
	if err = tx.QueryRow(ctx, `SELECT version,status FROM products WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, product).Scan(&version, &status); err != nil {
		return mediaDBError(err)
	}
	if version != expectedVersion {
		return fault.New(fault.Conflict, "VERSION_CONFLICT", "商品已更新，请刷新后重试")
	}
	if status != "publishing" {
		return fault.New(fault.Conflict, "INVALID_STATE", "仅上架中的商品可以重试图片同步")
	}
	var interrupted bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_images pi JOIN media_objects o ON o.media_id=pi.media_id WHERE pi.product_id=$1 AND (o.force_delete OR o.deleted_at IS NOT NULL))`, product).Scan(&interrupted); err != nil {
		return mediaDBError(err)
	}
	if interrupted {
		return fault.New(fault.Conflict, "PUBLICATION_INTERRUPTED", "上架已中止，请先整理有效图片再重新提交审核")
	}
	_, err = tx.Exec(ctx, `UPDATE media_objects SET retry_at=now(),last_error='' WHERE product_id=$1 AND deleted_at IS NULL AND NOT force_delete`, product)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO admin_logs(id,user_id,action,resource_id,detail) VALUES($1,$2,'media.sync.retry',$3,$4)`, s.NewID(), actor, product, map[string]any{"version": version})
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return mediaDBError(err)
}
