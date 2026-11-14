package postgres

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/media"
)

// ReadLegacyReplica runs after media authorization. A registered copy never
// silently falls back to local bytes when it is retired or inconsistent.
func (s *MediaLifecycleStore) ReadLegacyReplica(ctx context.Context, id, key string) (media.LegacyReplica, bool, error) {
	var o media.LegacyReplica
	var purpose, actualKey string
	var alive, registered, valid, published, contactOnly bool
	err := s.DB.QueryRow(ctx, `SELECT m.purpose,m.storage_key,m.status='ready' AND m.deleted_at IS NULL,
 r.media_id IS NOT NULL,coalesce(r.media_id,''),coalesce(r.storage_key,''),coalesce(r.bucket,''),coalesce(r.object_key,''),
 m.mime_type,coalesce(r.sha256,''),coalesce(r.crc64,''),coalesce(r.byte_size,0),m.created_at,
 coalesce(r.deleted_at IS NULL AND r.storage_key=m.storage_key AND r.byte_size=m.byte_size
 AND (coalesce(m.sha256,'')='' OR m.sha256=r.sha256)
 AND NOT EXISTS(SELECT 1 FROM media_retired_keys k WHERE k.storage_key=m.storage_key),false),
 EXISTS(SELECT 1 FROM product_images pi JOIN products p ON p.id=pi.product_id
 WHERE pi.media_id=m.id AND p.status IN ('published','publishing') AND p.deleted_at IS NULL),
 EXISTS(SELECT 1 FROM contacts c WHERE c.qr_media_id=m.id)
 AND NOT EXISTS(SELECT 1 FROM product_images pi WHERE pi.media_id=m.id)
 FROM media_files m LEFT JOIN media_legacy_replicas r ON r.media_id=m.id WHERE m.id=$1`, id).Scan(
		&purpose, &actualKey, &alive, &registered, &o.MediaID, &o.StorageKey, &o.Bucket, &o.ObjectKey,
		&o.MIME, &o.SHA256, &o.CRC64, &o.Size, &o.Modified, &valid, &published, &contactOnly)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return o, true, fault.New(fault.NotFound, "MEDIA_FILE_MISSING", "图片暂不可用")
		}
		return o, true, mediaDBError(err)
	}
	if !alive || actualKey != key {
		return o, true, fault.New(fault.NotFound, "MEDIA_FILE_MISSING", "图片暂不可用")
	}
	// Older contact images were labelled product before the contact purpose was
	// introduced. They remain local even if an old test backup was registered.
	if purpose != "product" || contactOnly {
		return o, false, nil
	}
	if !registered {
		// New unpublished local uploads still use the local adapter. Historical
		// originals and published local products require an explicit verified copy.
		if strings.HasPrefix(key, "legacy/") || published {
			return o, true, fault.New(fault.Unavailable, "MEDIA_REPLICA_UNAVAILABLE", "图片暂不可用")
		}
		return o, false, nil
	}
	if !valid {
		return o, true, fault.New(fault.Unavailable, "MEDIA_REPLICA_UNAVAILABLE", "图片暂不可用")
	}
	return o, true, nil
}
