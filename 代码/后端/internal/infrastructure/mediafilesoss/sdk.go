//go:build oss_read

package mediafilesoss

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	application "qingye/backend/internal/application/media"
	domain "qingye/backend/internal/domain/media"
)

type Config struct{ Bucket, Region, Endpoint string }

var errObject = errors.New("OSS object unavailable or inconsistent with verified migration record")

// New 仅供未来显式装配使用；读取环境凭据不会触发请求，也不会自动启用该存储。
// 业务进程目前没有调用者。启用前需确认迁移完成、只读 RAM 凭据和权限回归。
func New(cfg Config, local application.Files, verifiedManifest io.Reader) (*Store, error) {
	if local == nil || verifiedManifest == nil {
		return nil, errors.New("OSS read preparation requires local fallback and verified manifest")
	}
	if !bucketPattern.MatchString(cfg.Bucket) || cfg.Region != "cn-beijing" || cfg.Endpoint != "https://oss-cn-beijing.aliyuncs.com" {
		return nil, errors.New("OSS read preparation requires a valid bucket and the Beijing HTTPS endpoint")
	}
	manifest, err := LoadManifest(verifiedManifest, cfg.Bucket)
	if err != nil {
		return nil, err
	}
	client := oss.NewClient(oss.LoadDefaultConfig().
		WithRegion(cfg.Region).WithEndpoint(cfg.Endpoint).
		WithCredentialsProvider(credentials.NewEnvironmentVariableCredentialsProvider()).
		WithRetryMaxAttempts(2).WithConnectTimeout(3 * time.Second).
		WithReadWriteTimeout(15 * time.Second).WithMaxConnections(8).
		WithEnabledRedirect(false))
	return newStore(local, &sdkReader{client: client}, manifest)
}

type sdkReader struct{ client *oss.Client }

func (r *sdkReader) Open(ctx context.Context, entry Entry) (*domain.File, error) {
	// SDK 按 Range 流式读并固定 ETag；关闭预取，避免多图并发时无界缓存整图。
	f, err := r.client.OpenFile(ctx, entry.Bucket, entry.ObjectKey, func(o *oss.OpenOptions) { o.EnablePrefetch = false })
	if err != nil {
		return nil, errObject
	}
	valid := false
	defer func() {
		if !valid {
			_ = f.Close()
		}
	}()
	info, err := f.Stat()
	if err != nil || info.Size() != entry.Size {
		return nil, errObject
	}
	headers, ok := info.Sys().(http.Header)
	if !ok || headers.Get("Content-Type") != entry.MIME ||
		headers.Get("X-Oss-Meta-Source-Sha256") != entry.SHA256 ||
		headers.Get("X-Oss-Meta-Media-Id") != entry.MediaID ||
		headers.Get("X-Oss-Hash-Crc64ecma") != entry.CRC64 {
		return nil, errObject
	}
	var sample [512]byte
	n, err := io.ReadFull(f, sample[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, errObject
	}
	if http.DetectContentType(sample[:n]) != entry.MIME {
		return nil, errObject
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, errObject
	}
	valid = true
	return &domain.File{Reader: f, MIME: entry.MIME, Modified: info.ModTime()}, nil
}
