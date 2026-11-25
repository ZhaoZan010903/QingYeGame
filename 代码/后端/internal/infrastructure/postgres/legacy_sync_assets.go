package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	syncapp "qingye/backend/internal/application/legacysync"
)

func (s LegacySyncStore) FindAsset(ctx context.Context, legacyID, source, rawSHA string) (syncapp.Asset, bool, error) {
	var out syncapp.Asset
	err := s.DB.QueryRow(ctx, `SELECT m.id,o.object_key,o.mime_type,o.sha256,o.byte_size,m.status='ready'
 FROM legacy_sync_assets a JOIN media_files m ON m.id=a.media_id
 JOIN media_objects o ON o.media_id=m.id AND o.variant='original'
 WHERE a.legacy_id=$1 AND (($3='' AND (a.source_url=$2 OR EXISTS(
     SELECT 1 FROM legacy_sync_asset_urls u WHERE u.media_id=a.media_id AND u.source_url=$2)))
 OR ($3<>'' AND a.raw_sha256=$3))
 AND m.deleted_at IS NULL AND m.status IN ('pending','ready')
 AND o.store='oss' AND o.deleted_at IS NULL AND NOT o.force_delete
 AND NOT EXISTS(SELECT 1 FROM media_object_moves mv WHERE mv.media_id=m.id AND NOT mv.completed)
 AND (o.product_id IS NOT NULL OR o.retain_until>now() OR EXISTS(
     SELECT 1 FROM legacy_sync_media_holds h WHERE h.media_id=m.id AND h.expires_at>now()))
 ORDER BY (m.status='ready') DESC,m.created_at DESC LIMIT 1`,
		legacyID, source, rawSHA).Scan(&out.ID, &out.Key, &out.MIME, &out.SHA256, &out.Size, &out.Ready)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	return out, err == nil, err
}

func (s LegacySyncStore) RememberAssetURL(ctx context.Context, mediaID, source string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO legacy_sync_asset_urls(media_id,source_url)
 SELECT media_id,$2 FROM legacy_sync_assets WHERE media_id=$1 AND source_url<>$2
 ON CONFLICT DO NOTHING`, mediaID, source)
	return err
}

func (s LegacySyncStore) ReserveAsset(ctx context.Context, legacyID, source, rawSHA string, asset syncapp.Asset) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Reserve the immutable key before touching OSS. The existing media worker
	// eventually retires unattached reservations, even if a worker crashed mid-PUT.
	if _, err = tx.Exec(ctx, `INSERT INTO media_files(id,filename,mime_type,byte_size,storage_key,sha256,
 status,purpose,managed_upload,lifecycle_managed,upload_audience)
 VALUES($1,'旧站商品图片',$2,$3,$4,$5,'pending','product',true,true,'admin')`,
		asset.ID, asset.MIME, asset.Size, asset.Key, asset.SHA256); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO media_objects(media_id,variant,store,object_key,mime_type,byte_size,sha256,retain_until)
 VALUES($1,'original','oss',$2,$3,$4,$5,now()+interval '7 days')`,
		asset.ID, asset.Key, asset.MIME, asset.Size, asset.SHA256); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO legacy_sync_assets(media_id,legacy_id,source_url,raw_sha256)
 VALUES($1,$2,$3,$4)`, asset.ID, legacyID, source, rawSHA); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s LegacySyncStore) ReadyAsset(ctx context.Context, id string) error {
	tag, err := s.DB.Exec(ctx, `UPDATE media_files m SET status='ready'
 WHERE m.id=$1 AND m.status IN ('pending','ready') AND m.deleted_at IS NULL
 AND EXISTS(SELECT 1 FROM legacy_sync_assets WHERE media_id=m.id)
 AND EXISTS(SELECT 1 FROM media_objects o WHERE o.media_id=m.id AND o.store='oss'
 AND o.deleted_at IS NULL AND NOT o.force_delete AND o.retain_until>now())`, id)
	if err == nil && tag.RowsAffected() != 1 {
		return syncapp.ErrImage
	}
	return err
}
