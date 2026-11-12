package media

import (
	"context"
	"io"
	"qingye/backend/internal/domain/fault"
	domain "qingye/backend/internal/domain/media"
	"regexp"
	"strconv"
	"time"
)

const ChunkSize int64 = 1 << 20

var rangePattern = regexp.MustCompile(`^bytes ([0-9]+)-([0-9]+)/([0-9]+)$`)

func ParseRange(value string) (start, end, total int64, err error) {
	parts := rangePattern.FindStringSubmatch(value)
	bad := func() (int64, int64, int64, error) {
		return 0, 0, 0, fault.New(fault.Invalid, "INVALID_CONTENT_RANGE", "上传分块范围无效")
	}
	if len(parts) != 4 {
		return bad()
	}
	values := []*int64{&start, &end, &total}
	for i, p := range parts[1:] {
		n, e := strconv.ParseInt(p, 10, 64)
		if e != nil {
			return bad()
		}
		*values[i] = n
	}
	if total < 1 || start < 0 || end < start || end >= total || end-start >= ChunkSize || start%ChunkSize != 0 || (end+1 != total && end-start+1 != ChunkSize) {
		return bad()
	}
	return
}

// Chunk files are received without a transaction, then compared/installed under the media lock.
type Chunk interface {
	Install(committed int64) error
	Discard()
}
type ChunkFiles interface {
	ReceiveChunk(context.Context, string, int64, int64, io.Reader) (Chunk, error)
}
type ChunkMutation interface {
	domain.Mutation
	Progress(context.Context, int64, bool) error
	Renew(context.Context, string) error
	Cancel(context.Context) error
	Claim(context.Context, string) error
	Release(context.Context) error
}

// Compensation may retire an owned processing lease after upload permission expires.
type ProcessingReleaser interface {
	ReleaseProcessing(context.Context, string, string, string, string) error
}

func (s *Service) ReceiveRange(ctx context.Context, id, token, header string, length int64, body io.Reader) error {
	if err := validID(id); err != nil {
		return err
	}
	start, end, total, err := ParseRange(header)
	if err != nil {
		return err
	}
	u, err := s.repo.Ticket(ctx, id, s.surface)
	if err != nil || !s.checkTicket(u, token) {
		return tokenInvalid()
	}
	if !u.Resumable || u.Purpose != "product" || u.Size != total || total > s.max || (length >= 0 && length != end-start+1) {
		return fault.New(fault.Invalid, "UPLOAD_SIZE_MISMATCH", "分块与上传申请不一致")
	}
	if err = s.limiter.Allow(ctx, "upload-chunk:"+s.surface+":"+u.Owner, productChunkLimit(s.max), 10*time.Minute); err != nil {
		return err
	}
	if u.State != "pending" && u.State != "uploaded" {
		return fault.New(fault.Conflict, "UPLOAD_CLOSED", "此上传已关闭")
	}
	if start > u.Offset {
		return fault.New(fault.Conflict, "UPLOAD_OFFSET_MISMATCH", "上传位置已改变，请刷新上传进度")
	}
	files, ok := s.files.(ChunkFiles)
	if !ok {
		return fault.New(fault.Unavailable, "RESUME_UNAVAILABLE", "上传服务暂不可用")
	}
	incoming, err := files.ReceiveChunk(ctx, u.Key, start, end-start+1, body)
	if err != nil {
		return err
	}
	defer incoming.Discard()
	return s.repo.WithUpload(ctx, id, s.surface, "", func(current domain.Upload, m domain.Mutation) error {
		if !s.checkTicket(current, token) {
			return tokenInvalid()
		}
		if current.Key != u.Key || current.Size != total || !current.Resumable || (current.State != "pending" && current.State != "uploaded") {
			return fault.New(fault.Conflict, "UPLOAD_CLOSED", "此上传已关闭")
		}
		if start > current.Offset || (start < current.Offset && end >= current.Offset) {
			return fault.New(fault.Conflict, "UPLOAD_OFFSET_MISMATCH", "上传位置已改变，请刷新上传进度")
		}
		cm, ok := m.(ChunkMutation)
		if !ok {
			return fault.New(fault.Unavailable, "RESUME_UNAVAILABLE", "上传服务暂不可用")
		}
		if err := incoming.Install(current.Offset); err != nil {
			return err
		}
		if end < current.Offset {
			return nil
		}
		return cm.Progress(ctx, end+1, end+1 == total)
	})
}

func (s *Service) Status(ctx context.Context, actor domain.Actor, id string) (map[string]any, error) {
	if !s.validActor(actor) {
		return nil, unauthenticated()
	}
	if err := validID(id); err != nil {
		return nil, err
	}
	if err := s.limiter.Allow(ctx, "upload-status:"+actor.ID, productUploadLimit*2, 10*time.Minute); err != nil {
		return nil, err
	}
	token := s.newToken()
	var upload domain.Upload
	err := s.repo.WithUpload(ctx, id, s.surface, actor.ID, func(u domain.Upload, m domain.Mutation) error {
		upload = u
		if !u.Resumable || u.Purpose != "product" || (u.State != "pending" && u.State != "uploaded" && u.State != "ready") {
			return fault.New(fault.Conflict, "UPLOAD_CLOSED", "此上传已关闭")
		}
		if u.State == "ready" {
			return nil
		}
		cm, ok := m.(ChunkMutation)
		if !ok {
			return fault.New(fault.Unavailable, "RESUME_UNAVAILABLE", "上传服务暂不可用")
		}
		return cm.Renew(ctx, s.hash(token))
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"data": map[string]any{"media_id": id, "status": upload.State, "offset": upload.Offset, "chunk_size": ChunkSize, "upload_url": "/api/v1/uploads/" + id + "/content?token=" + token}}, nil
}
func (s *Service) Cancel(ctx context.Context, actor domain.Actor, id string) error {
	if !s.validActor(actor) {
		return unauthenticated()
	}
	if err := validID(id); err != nil {
		return err
	}
	var raw string
	err := s.repo.WithUpload(ctx, id, s.surface, actor.ID, func(u domain.Upload, m domain.Mutation) error {
		if !u.Resumable || u.Purpose != "product" {
			return fault.New(fault.Invalid, "INVALID_MEDIA_PURPOSE", "此上传不支持取消")
		}
		cm, ok := m.(ChunkMutation)
		if !ok {
			return fault.New(fault.Unavailable, "RESUME_UNAVAILABLE", "上传服务暂不可用")
		}
		if u.State != "ready" {
			raw = u.Key
		}
		return cm.Cancel(ctx)
	})
	if err == nil && raw != "" {
		s.files.RemoveRaw(raw)
	}
	return err
}
func (s *Service) releaseProcessing(ctx context.Context, actor domain.Actor, id, token string) {
	release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if repository, ok := s.repo.(ProcessingReleaser); ok {
		_ = repository.ReleaseProcessing(release, id, s.surface, actor.ID, token)
		return
	}
	_ = s.repo.WithUpload(release, id, s.surface, actor.ID, func(u domain.Upload, m domain.Mutation) error {
		if u.ProcessingToken != token {
			return nil
		}
		if cm, ok := m.(ChunkMutation); ok {
			return cm.Release(release)
		}
		return nil
	})
}
