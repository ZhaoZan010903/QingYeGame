package postgres

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"qingye/backend/internal/application/mediapurge"
	"qingye/backend/internal/domain/fault"
)

const permanentMediaReferencedSQL = `EXISTS(SELECT 1 FROM product_images WHERE media_id=$1)
 OR EXISTS(SELECT 1 FROM chat_message_images WHERE media_id=$1)
 OR EXISTS(SELECT 1 FROM users WHERE avatar_media_id=$1)
 OR EXISTS(SELECT 1 FROM avatar_library WHERE media_id=$1)
 OR EXISTS(SELECT 1 FROM contacts WHERE qr_media_id=$1)
 OR EXISTS(SELECT 1 FROM product_reviews WHERE snapshot->'images' @> jsonb_build_array(jsonb_build_object('id',$1::text)))
 OR EXISTS(SELECT 1 FROM media_files WHERE storage_key=$2 AND id<>$1 AND deleted_at IS NULL)`

// The caller holds all target product rows, has removed every selected product's
// image/review references, and retains a permanent product tombstone. Shared media
// is left readable and is never enrolled in physical cleanup.
func QueueProductPurgeMediaTx(ctx context.Context, tx pgx.Tx, productID string, mediaIDs []string) error {
	var purged bool
	if err := tx.QueryRow(ctx, `SELECT purged_at IS NOT NULL FROM products WHERE id=$1`, productID).Scan(&purged); err != nil {
		return err
	}
	if !purged {
		return errors.New("permanent media cleanup requires a product tombstone")
	}
	ids := append([]string(nil), mediaIDs...)
	sort.Strings(ids)
	previous := ""
	for _, id := range ids {
		if id == "" || id == previous {
			continue
		}
		previous = id
		var key, owner string
		var deleted, managed, r20 bool
		err := tx.QueryRow(ctx, `SELECT storage_key,coalesce(owner_id,''),deleted_at IS NOT NULL,managed_upload,lifecycle_managed FROM media_files WHERE id=$1`, id).Scan(&key, &owner, &deleted, &managed, &r20)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if owner != "" {
			if err = lockMemberQuota(ctx, tx, owner); err != nil {
				return err
			}
		}
		if err = tx.QueryRow(ctx, `SELECT storage_key,deleted_at IS NOT NULL FROM media_files WHERE id=$1 FOR UPDATE`, id).Scan(&key, &deleted); err != nil {
			return err
		}
		if deleted {
			continue
		}
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('media-storage-key:'||$1,0))`, key); err != nil {
			return err
		}
		var referenced bool
		if err = tx.QueryRow(ctx, `SELECT `+permanentMediaReferencedSQL, id, key).Scan(&referenced); err != nil {
			return err
		}
		if referenced {
			// The detach trigger removes product ownership. Preserve cross-resource
			// references when the ordinary R20 retention deadline is subsequently due.
			if _, err = tx.Exec(ctx, `UPDATE media_objects SET permanent_purge_guard=true,retry_at=now() WHERE media_id=$1 AND deleted_at IS NULL`, id); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(key, "legacy/") {
			var registered bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_legacy_replicas WHERE media_id=$1 AND storage_key=$2)`, id, key).Scan(&registered); err != nil {
				return err
			}
			if !registered {
				return fault.New(fault.Conflict, "MEDIA_REPLICA_NOT_REGISTERED", "历史图片副本登记未完成，请联系管理员核对后重试")
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO media_retired_keys(storage_key,media_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, key, id); err != nil {
			return err
		}
		// Revoke processing before a late upload/move can publish any new object.
		if _, err = tx.Exec(ctx, `UPDATE media_files SET deleted_at=now(),upload_token_hash=NULL,upload_expires_at=now(),upload_cleanup_requested=true,processing_token=NULL,processing_until=now() WHERE id=$1`, id); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `SELECT media_id FROM media_objects WHERE media_id=$1 ORDER BY variant FOR UPDATE`, id); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE media_objects SET product_id=NULL,permanent_purge_guard=true,force_delete=true,retain_until=now(),retry_at=now(),last_error='' WHERE media_id=$1 AND deleted_at IS NULL`, id); err != nil {
			return err
		}
		if key != "" && !strings.HasPrefix(key, "r20/") {
			if _, err = tx.Exec(ctx, `INSERT INTO media_permanent_cleanup(media_id,store,storage_key) VALUES($1,'local',$2) ON CONFLICT DO NOTHING`, id, key); err != nil {
				return err
			}
		}
		if managed || r20 {
			if _, err = tx.Exec(ctx, `INSERT INTO media_permanent_cleanup(media_id,store,storage_key) VALUES($1,'local','uploads/'||$1||'.raw') ON CONFLICT DO NOTHING`, id); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO media_permanent_cleanup(media_id,store,storage_key) SELECT media_id,'legacy_oss',storage_key FROM media_legacy_replicas WHERE media_id=$1 AND deleted_at IS NULL ON CONFLICT DO NOTHING`, id); err != nil {
			return err
		}
	}
	return nil
}

type PermanentCleanupObject = mediapurge.Object
type MediaPermanentStore struct {
	DB    *pgxpool.Pool
	NewID func() string
}
type PermanentLocalPlan = mediapurge.Plan
type PermanentRemove = mediapurge.Remove

// A thumbnail that was authorized before purge must finish its final rename
// before the purge transaction can tombstone the media row. No late derivative
// can be created after the permanent worker has removed the original.
func (s *MediaPermanentStore) CommitThumbnail(ctx context.Context, id string, commit func() error) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var alive bool
	if err = tx.QueryRow(ctx, `SELECT deleted_at IS NULL FROM media_files WHERE id=$1 FOR SHARE`, id).Scan(&alive); err != nil {
		return err
	}
	if !alive {
		return errors.New("media retired before thumbnail commit")
	}
	if err = commit(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *MediaPermanentStore) StepPermanent(ctx context.Context, plan PermanentLocalPlan, remove PermanentRemove) (bool, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var o PermanentCleanupObject
	err = tx.QueryRow(ctx, `SELECT q.id,q.media_id,q.store,q.storage_key,q.derived_key,q.local_planned,
 coalesce(r.bucket,''),coalesce(r.object_key,''),coalesce(r.byte_size,0),coalesce(r.sha256,''),coalesce(r.crc64,'')
 FROM media_permanent_cleanup q LEFT JOIN media_legacy_replicas r ON r.media_id=q.media_id
 WHERE q.completed_at IS NULL AND q.retry_at<=now() AND (q.lease_until IS NULL OR q.lease_until<=now())
 ORDER BY q.retry_at,q.id LIMIT 1 FOR UPDATE OF q SKIP LOCKED`).Scan(&o.ID, &o.MediaID, &o.Store, &o.StorageKey, &o.DerivedKey, &o.Planned, &o.Bucket, &o.ObjectKey, &o.Size, &o.SHA256, &o.CRC64)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var safe bool
	err = tx.QueryRow(ctx, `SELECT m.deleted_at IS NOT NULL AND NOT (`+permanentMediaReferencedSQL+`) FROM media_files m WHERE m.id=$1`, o.MediaID, o.StorageKey).Scan(&safe)
	if err != nil {
		return false, err
	}
	if !safe {
		return false, errors.New("permanent cleanup refused referenced media")
	}
	token := s.NewID()
	if _, err = tx.Exec(ctx, `UPDATE media_permanent_cleanup SET lease_token=$2,lease_until=now()+interval '15 minutes' WHERE id=$1`, o.ID, token); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	var operationErr error
	if o.Store == "local" && !o.Planned {
		o.DerivedKey, operationErr = plan(ctx, o.MediaID, o.StorageKey)
		if operationErr == nil {
			var tag int64
			command, e := s.DB.Exec(ctx, `UPDATE media_permanent_cleanup SET derived_key=$3,local_planned=true WHERE id=$1 AND lease_token=$2`, o.ID, token, o.DerivedKey)
			if e != nil {
				operationErr = e
			} else {
				tag = command.RowsAffected()
			}
			if operationErr == nil && tag != 1 {
				operationErr = errors.New("permanent cleanup lease lost")
			}
		}
	}
	if operationErr == nil {
		operationErr = remove(ctx, o)
	}
	final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if operationErr != nil {
		_, err = s.DB.Exec(final, `UPDATE media_permanent_cleanup SET attempts=attempts+1,last_error='PERMANENT_CLEANUP_FAILED',retry_at=now()+interval '1 minute',lease_token=NULL,lease_until=NULL WHERE id=$1 AND lease_token=$2`, o.ID, token)
		if err != nil {
			return true, err
		}
		return true, errors.New("permanent media cleanup failed; retained for retry")
	}
	finalTx, err := s.DB.Begin(final)
	if err != nil {
		return true, err
	}
	defer finalTx.Rollback(final)
	tag, err := finalTx.Exec(final, `UPDATE media_permanent_cleanup SET attempts=attempts+1,completed_at=now(),last_error='',lease_token=NULL,lease_until=NULL WHERE id=$1 AND lease_token=$2`, o.ID, token)
	if err == nil && tag.RowsAffected() == 1 && o.Store == "legacy_oss" {
		_, err = finalTx.Exec(final, `UPDATE media_legacy_replicas SET deleted_at=now() WHERE media_id=$1`, o.MediaID)
	}
	if err != nil {
		return true, err
	}
	return true, finalTx.Commit(final)
}
