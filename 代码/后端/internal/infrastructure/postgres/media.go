package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/media"
)

type MediaStore struct{ DB *pgxpool.Pool }

func mediaDBError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := fault.As(err); ok {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.New(fault.NotFound, "NOT_FOUND", "未找到资料")
	}
	return fault.Wrap(fault.Unavailable, "DATABASE_UNAVAILABLE", "服务暂不可用，请稍后重试", err)
}
func (s *MediaStore) Create(ctx context.Context, actor media.Actor, in media.CreateInput, id, key, hash string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO media_files(id,owner_id,filename,mime_type,byte_size,storage_key,upload_token_hash,upload_expires_at,upload_audience) VALUES($1,$2,$3,$4,$5,$6,$7,now()+interval '15 minutes',$8)`, id, actor.ID, in.Filename, in.MIME, in.Size, key, hash, actor.Surface)
	return mediaDBError(err)
}

const uploadColumns = `id,coalesce(owner_id,''),storage_key,mime_type,status,byte_size,coalesce(upload_token_hash,''),coalesce(upload_expires_at,created_at)>now()`

func scanUpload(row pgx.Row) (media.Upload, error) {
	var u media.Upload
	err := row.Scan(&u.ID, &u.Owner, &u.Key, &u.MIME, &u.State, &u.Size, &u.TokenHash, &u.Valid)
	return u, err
}
func (s *MediaStore) Ticket(ctx context.Context, id, surface string) (media.Upload, error) {
	u, err := scanUpload(s.DB.QueryRow(ctx, "SELECT "+uploadColumns+" FROM media_files WHERE id=$1 AND upload_audience=$2", id, surface))
	return u, mediaDBError(err)
}

type mediaMutation struct {
	tx pgx.Tx
	id string
}

func (m mediaMutation) Uploaded(ctx context.Context) error {
	_, err := m.tx.Exec(ctx, "UPDATE media_files SET status='uploaded' WHERE id=$1", m.id)
	return err
}
func (m mediaMutation) Failed(ctx context.Context) error {
	_, err := m.tx.Exec(ctx, "UPDATE media_files SET status='failed',upload_token_hash=NULL WHERE id=$1", m.id)
	return err
}
func (m mediaMutation) Ready(ctx context.Context, p media.Processed) error {
	_, err := m.tx.Exec(ctx, "UPDATE media_files SET storage_key=$1,mime_type=$2,byte_size=$3,status='ready',upload_token_hash=NULL WHERE id=$4", p.Key, p.MIME, p.Size, m.id)
	return err
}
func (s *MediaStore) WithUpload(ctx context.Context, id, surface, owner string, fn func(media.Upload, media.Mutation) error) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return mediaDBError(err)
	}
	defer tx.Rollback(ctx)
	u, err := scanUpload(tx.QueryRow(ctx, "SELECT "+uploadColumns+" FROM media_files WHERE id=$1 AND upload_audience=$2 AND ($3='' OR owner_id=$3) FOR UPDATE", id, surface, owner))
	if err != nil {
		return mediaDBError(err)
	}
	if err = fn(u, mediaMutation{tx: tx, id: id}); err != nil {
		return mediaDBError(err)
	}
	return mediaDBError(tx.Commit(ctx))
}
func (s *MediaStore) Lookup(ctx context.Context, id string) (media.Upload, error) {
	var u media.Upload
	u.ID = id
	err := s.DB.QueryRow(ctx, `SELECT COALESCE(m.owner_id,''),m.storage_key,m.mime_type,m.status,
 EXISTS(SELECT 1 FROM product_images pi JOIN products p ON p.id=pi.product_id JOIN categories c ON c.id=p.category_id WHERE pi.media_id=m.id AND p.status='published' AND p.approved_version=p.version AND c.enabled)
 OR EXISTS(SELECT 1 FROM contacts c WHERE c.qr_media_id=m.id AND c.enabled)
 FROM media_files m WHERE m.id=$1`, id).Scan(&u.Owner, &u.Key, &u.MIME, &u.State, &u.Public)
	return u, mediaDBError(err)
}
func (s *MediaStore) OwnedProduct(ctx context.Context, id, owner string) (bool, error) {
	var ok bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_images pi JOIN products p ON p.id=pi.product_id WHERE pi.media_id=$1 AND p.owner_id=$2)`, id, owner).Scan(&ok)
	return ok, mediaDBError(err)
}
