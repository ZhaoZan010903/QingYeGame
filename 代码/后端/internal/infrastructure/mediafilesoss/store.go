//go:build oss_read

// Package mediafilesoss 预留私有 OSS 原图读取。迁移未完成，业务装配仍为注释，禁止启用。
package mediafilesoss

import (
	"context"
	"errors"
	"io"
	"sync"

	application "qingye/backend/internal/application/media"
	domain "qingye/backend/internal/domain/media"
)

type remoteReader interface {
	Open(context.Context, Entry) (*domain.File, error)
}

// Store 只替换经过业务授权的原图读取；上传、处理、删除和缩略图仍委托本地存储。
// 必须在启动时构造并复用，不能为每个 HTTP 请求重新创建连接池或并发限制器。
type Store struct {
	application.Files
	remote   remoteReader
	manifest *Manifest
	slots    chan struct{}
}

func newStore(local application.Files, remote remoteReader, manifest *Manifest) (*Store, error) {
	if local == nil || remote == nil || manifest == nil {
		return nil, errors.New("OSS read preparation requires local fallback, reader and verified manifest")
	}
	return &Store{Files: local, remote: remote, manifest: manifest, slots: make(chan struct{}, 8)}, nil
}

func (s *Store) Open(ctx context.Context, key, id string, thumbnail bool) (*domain.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry, found := s.manifest.lookup(id, key)
	// 不能将聊天附件、头像、未验证对象或未迁移的 derived 缓存送入 OSS。
	if thumbnail || !found {
		return s.Files.Open(ctx, key, id, thumbnail)
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return s.Files.Open(ctx, key, id, false)
	}
	readCtx, cancel := context.WithCancel(ctx)
	file, err := s.remote.Open(readCtx, entry)
	if err != nil {
		cancel()
		<-s.slots
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// 仅在响应尚未开始时回退，避免把两份文件拼接进同一个响应。
		return s.Files.Open(ctx, key, id, false)
	}
	file.Reader = &leasedReader{ReadSeekCloser: file.Reader, ctx: readCtx, cancel: cancel, release: func() { <-s.slots }}
	return file, nil
}

type leasedReader struct {
	io.ReadSeekCloser
	ctx      context.Context
	cancel   context.CancelFunc
	release  func()
	once     sync.Once
	closeErr error
}

func (r *leasedReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.ReadSeekCloser.Read(p)
	return n, r.safeError(err)
}
func (r *leasedReader) Seek(offset int64, whence int) (int64, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.ReadSeekCloser.Seek(offset, whence)
	return n, r.safeError(err)
}
func (r *leasedReader) safeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, io.EOF) {
		return io.EOF
	}
	if canceled := r.ctx.Err(); canceled != nil {
		return canceled
	}
	// SDK 错误可能携带请求信息；不能传播到媒体响应或通用访问日志。
	return errObject
}
func (r *leasedReader) Close() error {
	r.once.Do(func() {
		r.cancel()
		if err := r.ReadSeekCloser.Close(); err != nil {
			r.closeErr = errObject
		}
		r.release()
	})
	return r.closeErr
}

var _ application.Files = (*Store)(nil)
