// Package media 组织上传、校验和授权，流传输不依赖 HTTP。
package media

import (
	"context"
	"crypto/subtle"
	"io"
	"qingye/backend/internal/domain/fault"
	domain "qingye/backend/internal/domain/media"
	"strings"
	"time"
	"unicode/utf8"
)

type Repository interface {
	Create(context.Context, domain.Actor, domain.CreateInput, string, string, string) error
	Ticket(context.Context, string, string) (domain.Upload, error)
	WithUpload(context.Context, string, string, string, func(domain.Upload, domain.Mutation) error) error
	Lookup(context.Context, string) (domain.Upload, error)
	OwnedProduct(context.Context, string, string) (bool, error)
	ScopedAccess(context.Context, string, domain.Actor) (bool, error)
}
type Files interface {
	Receive(context.Context, string, int64, io.Reader) (domain.Staged, error)
	Sanitize(context.Context, domain.Upload) (domain.Processed, error)
	RemoveRaw(string)
	Open(context.Context, string, string, bool) (*domain.File, error)
}
type Limiter interface {
	Allow(context.Context, string, int, time.Duration) error
}
type Service struct {
	repo            Repository
	files           Files
	limiter         Limiter
	surface         string
	max             int64
	processors      chan struct{}
	newID, newToken func() string
	hash            func(string) string
}

func New(repo Repository, files Files, limiter Limiter, surface string, max int64, processors chan struct{}, newID, newToken func() string, hash func(string) string) *Service {
	if max <= 0 {
		max = 10 << 20
	}
	return &Service{repo: repo, files: files, limiter: limiter, surface: surface, max: max, processors: processors, newID: newID, newToken: newToken, hash: hash}
}
func (s *Service) validActor(a domain.Actor) bool {
	role := "member"
	if s.surface == "admin" {
		role = "admin"
	}
	return (s.surface == "public" || s.surface == "admin") && a.ID != "" && a.Surface == s.surface && a.Role == role
}
func validID(id string) error {
	if len(id) == 0 || len(id) > 100 || !utf8.ValidString(id) || strings.ContainsRune(id, 0) {
		return fault.New(fault.Invalid, "INVALID_MEDIA", "图片编号无效")
	}
	return nil
}
func unauthenticated() error {
	return fault.New(fault.Unauthenticated, "UNAUTHENTICATED", "请先登录")
}
func tokenInvalid() error {
	return fault.New(fault.Forbidden, "UPLOAD_TOKEN_INVALID", "上传凭据无效或过期")
}
func (s *Service) checkTicket(u domain.Upload, token string) bool {
	return len(token) == 64 && u.Valid && subtle.ConstantTimeCompare([]byte(u.TokenHash), []byte(s.hash(token))) == 1
}
func (s *Service) Create(ctx context.Context, actor domain.Actor, in domain.CreateInput) (map[string]any, error) {
	if !s.validActor(actor) {
		return nil, unauthenticated()
	}
	if in.Purpose == "" {
		in.Purpose = "product"
	}
	if (in.Purpose != "product" && in.Purpose != "chat" && in.Purpose != "avatar") || (in.Purpose == "chat" && validID(in.ConversationID) != nil) || (in.Purpose != "chat" && in.ConversationID != "") {
		return nil, fault.New(fault.Invalid, "INVALID_MEDIA_PURPOSE", "图片用途或咨询编号无效")
	}
	if in.Purpose == "avatar" && in.Size > 2<<20 {
		return nil, fault.New(fault.TooLarge, "IMAGE_TOO_LARGE", "头像不能超过2MB")
	}
	if in.Purpose == "chat" && in.Size > 10<<20 {
		return nil, fault.New(fault.TooLarge, "IMAGE_TOO_LARGE", "聊天图片不能超过10MB")
	}
	if in.Size <= 0 || in.Size > s.max {
		return nil, fault.New(fault.TooLarge, "IMAGE_TOO_LARGE", "单张图片大小超过限制")
	}
	if !domain.ValidUploadName(in.Filename, in.MIME) {
		return nil, fault.New(fault.Invalid, "INVALID_IMAGE_TYPE", "请选择扩展名与格式一致的 JPG、PNG 或 WebP 图片")
	}
	if err := s.limiter.Allow(ctx, "upload:"+actor.ID, 120, 10*time.Minute); err != nil {
		return nil, err
	}
	id, token := s.newID(), s.newToken()
	if err := s.repo.Create(ctx, actor, in, id, "uploads/"+id+".raw", s.hash(token)); err != nil {
		return nil, err
	}
	return map[string]any{"data": map[string]any{"media_id": id, "upload_url": "/api/v1/uploads/" + id + "/content?token=" + token, "method": "PUT", "headers": map[string]string{"Content-Type": in.MIME}, "expires_in": 900}}, nil
}
func (s *Service) Receive(ctx context.Context, id, token string, length int64, source io.Reader) error {
	if err := validID(id); err != nil {
		return err
	}
	if len(token) != 64 {
		return tokenInvalid()
	}
	upload, err := s.repo.Ticket(ctx, id, s.surface)
	if err != nil || !s.checkTicket(upload, token) {
		return tokenInvalid()
	}
	if upload.State == "uploaded" || upload.State == "ready" {
		return nil
	}
	if upload.State != "pending" {
		return fault.New(fault.Conflict, "UPLOAD_CLOSED", "此上传已关闭")
	}
	if upload.Size < 1 || upload.Size > s.max {
		return fault.New(fault.TooLarge, "IMAGE_TOO_LARGE", "单张图片大小超过限制")
	}
	if length >= 0 && length != upload.Size {
		return fault.New(fault.Invalid, "UPLOAD_SIZE_MISMATCH", "图片大小与上传申请不一致")
	}
	// 网络读取与写盘发生在事务之前，慢上传不能耗尽连接池。
	incoming, err := s.files.Receive(ctx, upload.Key, upload.Size, source)
	if err != nil {
		return err
	}
	defer incoming.Discard()
	return s.repo.WithUpload(ctx, id, s.surface, "", func(current domain.Upload, mutation domain.Mutation) error {
		if !s.checkTicket(current, token) {
			return tokenInvalid()
		}
		if current.State == "uploaded" {
			return nil
		}
		if current.State != "pending" || current.Key != upload.Key || current.Size != upload.Size {
			return fault.New(fault.Conflict, "UPLOAD_CLOSED", "此上传已关闭或发生变化")
		}
		if err := incoming.Install(); err != nil {
			return err
		}
		return mutation.Uploaded(ctx)
	})
}
func (s *Service) Complete(ctx context.Context, actor domain.Actor, id string) (map[string]any, error) {
	if !s.validActor(actor) {
		return nil, unauthenticated()
	}
	if err := validID(id); err != nil {
		return nil, err
	}
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case s.processors <- struct{}{}:
		defer func() { <-s.processors }()
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fault.New(fault.RateLimited, "IMAGE_PROCESSOR_BUSY", "图片处理繁忙，请稍后重试")
	}
	var invalid error
	rawKey := ""
	err := s.repo.WithUpload(ctx, id, s.surface, actor.ID, func(upload domain.Upload, mutation domain.Mutation) error {
		if upload.State == "ready" {
			return nil
		}
		if upload.State != "uploaded" {
			return fault.New(fault.Invalid, "MEDIA_NOT_UPLOADED", "图片尚未上传或未通过校验")
		}
		processed, err := s.files.Sanitize(ctx, upload)
		if err != nil {
			if f, ok := fault.As(err); ok && f.Code == "INVALID_IMAGE" {
				invalid = err
				rawKey = upload.Key
				return mutation.Failed(ctx)
			}
			return err
		}
		rawKey = upload.Key
		if err := mutation.Ready(ctx, processed); err != nil {
			s.files.RemoveRaw(processed.Key)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// 事务确认成功后才清理含元数据的原始上传，提交失败仍可重试。
	if rawKey != "" {
		s.files.RemoveRaw(rawKey)
	}
	if invalid != nil {
		return nil, invalid
	}
	return map[string]any{"data": map[string]any{"id": id, "status": "ready", "url": "/api/v1/media/" + id + "/file", "preview_url": "/api/v1/media/" + id + "/preview"}}, nil
}
func (s *Service) Open(ctx context.Context, id string, thumbnail bool, resolve func(context.Context) (domain.Actor, error)) (*domain.File, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	upload, err := s.repo.Lookup(ctx, id)
	if err != nil {
		return nil, err
	}
	if upload.State != "ready" {
		return nil, fault.New(fault.NotFound, "MEDIA_NOT_READY", "图片暂不可用")
	}
	if !upload.Public {
		hidden := func() (*domain.File, error) {
			return nil, fault.New(fault.NotFound, "NOT_FOUND", "图片不存在或尚未公开")
		}
		actor, err := resolve(ctx)
		if err != nil || !s.validActor(actor) {
			return hidden()
		}
		permitted := false
		if upload.Purpose == "chat" || upload.Purpose == "avatar" {
			permitted, err = s.repo.ScopedAccess(ctx, id, actor)
			if err != nil {
				return nil, err
			}
		} else {
			permitted = actor.Role == "admin" || actor.ID == upload.Owner
		}
		if !permitted && upload.Purpose != "chat" && upload.Purpose != "avatar" {
			permitted, err = s.repo.OwnedProduct(ctx, id, actor.ID)
			if err != nil {
				return nil, err
			}
		}
		if !permitted {
			return hidden()
		}
	}
	// 缩略图命中缓存也必须先通过相同权限检查。
	return s.files.Open(ctx, upload.Key, id, thumbnail)
}
