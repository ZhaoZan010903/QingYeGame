package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/media"
)

type MediaStore struct {
	DB             *pgxpool.Pool
	ManagedEnabled bool
}

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
	if in.Purpose == "avatar" && (actor.Role != "admin" || actor.Surface != "admin") {
		return fault.New(fault.Forbidden, "AVATAR_LIBRARY_ONLY", "请从头像库选择头像")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return mediaDBError(err)
	}
	defer tx.Rollback(ctx)
	if in.Purpose == "" {
		in.Purpose = "product"
	}
	if in.Purpose == "chat" {
		column := "member_id"
		if actor.Surface == "admin" {
			column = "admin_id"
		}
		var canonical string
		err = tx.QueryRow(ctx, `SELECT c.id FROM chat_conversations old JOIN chat_conversations c ON c.id=coalesce(old.merged_into,old.id) WHERE old.id=$1 AND c.`+column+`=$2 AND c.status<>'closed' FOR SHARE OF c`, in.ConversationID, actor.ID).Scan(&canonical)
		if err != nil {
			return mediaDBError(err)
		}
		in.ConversationID = canonical
	}
	if in.Purpose == "product" {
		if err = lockMemberQuota(ctx, tx, actor.ID); err != nil {
			return mediaDBError(err)
		}
		if err = checkProductUploadAllowed(ctx, tx, actor.ID); err != nil {
			return err
		}
	}
	if actor.Surface == "public" || in.Purpose != "product" {
		if err = checkUploadQuota(ctx, tx, actor.ID, "", in.Size, true); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO media_files(id,owner_id,filename,mime_type,byte_size,storage_key,upload_token_hash,upload_expires_at,upload_audience,managed_upload,purpose,conversation_id,resumable,upload_security_version,lifecycle_managed) VALUES($1,$2,$3,$4,$5,$6,$7,now()+interval '15 minutes',$8,true,$9,NULLIF($10,''),$11,coalesce((SELECT version FROM product_upload_restrictions WHERE user_id=$2),0),$12)`, id, actor.ID, in.Filename, in.MIME, in.Size, key, hash, actor.Surface, in.Purpose, in.ConversationID, in.Resumable, s.ManagedEnabled && in.Purpose == "product")
	if err == nil {
		err = tx.Commit(ctx)
	}
	return mediaDBError(err)
}

const uploadColumns = `id,coalesce(owner_id,''),storage_key,mime_type,status,byte_size,coalesce(upload_token_hash,''),coalesce(upload_expires_at,created_at)>now(),purpose,resumable,upload_offset,upload_security_version,coalesce(processing_token,''),coalesce(processing_until>now(),false),(NOT (resumable OR lifecycle_managed) OR created_at>now()-interval '5 days'),lifecycle_managed`

func scanUpload(row pgx.Row) (media.Upload, error) {
	var u media.Upload
	err := row.Scan(&u.ID, &u.Owner, &u.Key, &u.MIME, &u.State, &u.Size, &u.TokenHash, &u.Valid, &u.Purpose, &u.Resumable, &u.Offset, &u.SecurityVersion, &u.ProcessingToken, &u.ProcessingValid, &u.ResumeValid, &u.Managed)
	return u, err
}
func (s *MediaStore) Ticket(ctx context.Context, id, surface string) (media.Upload, error) {
	u, err := scanUpload(s.DB.QueryRow(ctx, "SELECT "+uploadColumns+" FROM media_files WHERE id=$1 AND upload_audience=$2 AND deleted_at IS NULL", id, surface))
	u.Valid = u.Valid && u.ResumeValid
	return u, mediaDBError(err)
}

type mediaMutation struct {
	tx             pgx.Tx
	id             string
	owner, surface string
	purpose        string
	managed        bool
}

func (m mediaMutation) Uploaded(ctx context.Context) error {
	_, err := m.tx.Exec(ctx, "UPDATE media_files SET status='uploaded' WHERE id=$1", m.id)
	return err
}
func (m mediaMutation) Failed(ctx context.Context) error {
	_, err := m.tx.Exec(ctx, "UPDATE media_files SET status='failed',upload_token_hash=NULL,processing_token=NULL,processing_until=NULL WHERE id=$1", m.id)
	return err
}
func (m mediaMutation) Ready(ctx context.Context, p media.Processed) error {
	if !m.managed && len(p.Managed) != 0 {
		return fault.New(fault.Invalid, "INVALID_MANAGED_MEDIA", "图片用途不支持此存储方式")
	}
	if m.managed {
		variants := map[string]bool{}
		for _, object := range p.Managed {
			if (object.Variant != "original" && object.Variant != "preview") || variants[object.Variant] {
				return fault.New(fault.Invalid, "INVALID_MANAGED_MEDIA", "图片存储校验未完成")
			}
			variants[object.Variant] = true
			if object.Variant == "original" && (object.Key != p.Key || object.MIME != p.MIME || object.Size != p.Size) {
				return fault.New(fault.Invalid, "INVALID_MANAGED_MEDIA", "图片存储校验未完成")
			}
		}
		// New uploads use one display object; older verified original/preview pairs remain readable.
		if len(p.Managed) < 1 || len(p.Managed) > 2 || !variants["original"] {
			return fault.New(fault.Invalid, "INVALID_MANAGED_MEDIA", "图片存储校验未完成")
		}
	}

	if m.surface == "public" || m.purpose != "product" {
		if err := checkUploadQuota(ctx, m.tx, m.owner, m.id, p.Size, false); err != nil {
			return err
		}
	}
	_, err := m.tx.Exec(ctx, "UPDATE media_files SET storage_key=$1,mime_type=$2,byte_size=$3,status='ready',ready_at=now(),upload_token_hash=NULL,processing_token=NULL,processing_until=NULL WHERE id=$4", p.Key, p.MIME, p.Size, m.id)
	if err != nil {
		return err
	}
	for _, object := range p.Managed {
		if err = RegisterMediaObjectTx(ctx, m.tx, m.id, object.Variant, object.Key, object.MIME, object.SHA256, object.Size); err != nil {
			return err
		}
	}
	if m.managed {
		return QueueCompletedUploadRawTx(ctx, m.tx, m.id)
	}
	return nil
}
func (s *MediaStore) WithUpload(ctx context.Context, id, surface, owner string, fn func(media.Upload, media.Mutation) error) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return mediaDBError(err)
	}
	defer tx.Rollback(ctx)
	// Lifecycle cleanup and upload mutations use owner -> media lock order.
	// The network body has already been staged before this transaction starts.
	{
		var uploadOwner string
		if err = tx.QueryRow(ctx, `SELECT coalesce(owner_id,'') FROM media_files WHERE id=$1 AND upload_audience=$2 AND ($3='' OR owner_id=$3) AND deleted_at IS NULL`, id, surface, owner).Scan(&uploadOwner); err != nil {
			return mediaDBError(err)
		}
		if uploadOwner != "" {
			if err = lockMemberQuota(ctx, tx, uploadOwner); err != nil {
				return mediaDBError(err)
			}
		}
	}
	u, err := scanUpload(tx.QueryRow(ctx, "SELECT "+uploadColumns+" FROM media_files WHERE id=$1 AND upload_audience=$2 AND ($3='' OR owner_id=$3) AND deleted_at IS NULL FOR UPDATE", id, surface, owner))
	if err != nil {
		return mediaDBError(err)
	}
	if (u.Resumable || u.Managed) && !u.ResumeValid && u.State != "ready" && u.State != "failed" {
		return fault.New(fault.Conflict, "UPLOAD_EXPIRED", "上传已过期，请重新选择图片")
	}
	if u.Purpose == "product" && u.State != "ready" && u.State != "failed" {
		if err = checkProductUploadAllowed(ctx, tx, u.Owner); err != nil {
			return err
		}
		var currentVersion int64
		if err = tx.QueryRow(ctx, `SELECT coalesce((SELECT version FROM product_upload_restrictions WHERE user_id=$1),0)`, u.Owner).Scan(&currentVersion); err != nil {
			return mediaDBError(err)
		}
		if u.SecurityVersion != currentVersion {
			return fault.New(fault.Forbidden, "UPLOAD_REVOKED", "此上传已失效，请重新上传")
		}
	}
	if err = fn(u, mediaMutation{tx: tx, id: id, owner: u.Owner, surface: surface, purpose: u.Purpose, managed: u.Managed}); err != nil {
		return mediaDBError(err)
	}
	return mediaDBError(tx.Commit(ctx))
}
func (s *MediaStore) Lookup(ctx context.Context, id string) (media.Upload, error) {
	var u media.Upload
	u.ID = id
	err := s.DB.QueryRow(ctx, `SELECT COALESCE(m.owner_id,''),m.storage_key,m.mime_type,m.status,m.purpose,m.byte_size,
 (m.purpose='product' AND EXISTS(SELECT 1 FROM product_images pi JOIN products p ON p.id=pi.product_id JOIN categories c ON c.id=p.category_id WHERE pi.media_id=m.id AND p.status='published' AND p.approved_version=p.version AND c.enabled))
 OR (m.purpose IN ('product','contact') AND EXISTS(SELECT 1 FROM contacts c WHERE c.qr_media_id=m.id AND c.enabled))
	 FROM media_files m WHERE m.id=$1 AND m.deleted_at IS NULL`, id).Scan(&u.Owner, &u.Key, &u.MIME, &u.State, &u.Purpose, &u.Size, &u.Public)
	return u, mediaDBError(err)
}
func (s *MediaStore) ScopedAccess(ctx context.Context, id string, actor media.Actor) (bool, error) {
	column := "member_id"
	if actor.Surface == "admin" {
		column = "admin_id"
	}
	var ok bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_files m WHERE m.id=$1 AND m.deleted_at IS NULL AND m.status='ready' AND (
 (m.purpose='chat' AND EXISTS(SELECT 1 FROM chat_conversations c WHERE c.id=m.conversation_id AND c.`+column+`=$2 AND (m.owner_id=$2 OR EXISTS(SELECT 1 FROM chat_message_images a WHERE a.media_id=m.id))))
 OR (m.purpose='avatar' AND (m.owner_id=$2 OR EXISTS(SELECT 1 FROM users u WHERE u.avatar_media_id=m.id AND u.enabled) OR EXISTS(SELECT 1 FROM avatar_library a WHERE a.media_id=m.id AND (a.enabled OR $3='admin'))))))`, id, actor.ID, actor.Role).Scan(&ok)
	return ok, mediaDBError(err)
}
func (s *MediaStore) OwnedProduct(ctx context.Context, id, owner string) (bool, error) {
	var ok bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_images pi JOIN products p ON p.id=pi.product_id WHERE pi.media_id=$1 AND p.owner_id=$2)`, id, owner).Scan(&ok)
	return ok, mediaDBError(err)
}
