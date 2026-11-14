package mediaobject

import (
	"context"
	"errors"
	"net/http"
	"qingye/backend/internal/application/mediapurge"
	"regexp"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
)

var legacyReplicaKey = regexp.MustCompile(`^(legacy/[0-9a-f]{64}|images/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.(jpg|jpeg|png|webp))$`)

type LegacyDeleter struct {
	backend *ossBackend
	prefix  string
}

func LegacyPrefix(environment string) string {
	switch environment {
	case "test":
		return "qingye-test/media/"
	case "acceptance":
		return "qingye-acceptance/media/"
	case "production":
		return "qingye-production/media/"
	}
	return ""
}
func NewLegacyOSS(c Config) (*LegacyDeleter, error) {
	prefix := LegacyPrefix(c.Environment)
	if prefix == "" {
		return nil, errors.New("invalid legacy cleanup environment")
	}
	store, err := NewOSS(c)
	if err != nil {
		return nil, err
	}
	return &LegacyDeleter{backend: store.backend.(*ossBackend), prefix: prefix}, nil
}
func (d *LegacyDeleter) DeleteReplica(ctx context.Context, o mediapurge.Object) error {
	if !legacyReplicaKey.MatchString(o.StorageKey) || o.ObjectKey != d.prefix+o.StorageKey || o.Bucket != d.backend.bucket || o.MediaID == "" || !validDigest(o.Size, o.SHA256) || o.CRC64 == "" {
		return ErrIntegrity
	}
	// The final verified manifest is authoritative. HEAD metadata is checked again
	// immediately before deletion; a mismatching replacement is never removed.
	file, err := d.backend.client.OpenFile(ctx, o.Bucket, o.ObjectKey, func(options *oss.OpenOptions) { options.EnablePrefetch = false })
	if err != nil {
		return legacyDeleteError(ctx, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return legacyDeleteError(ctx, err)
	}
	headers, ok := info.Sys().(http.Header)
	if !ok || info.Size() != o.Size || headers.Get("X-Oss-Meta-Source-Sha256") != o.SHA256 || headers.Get("X-Oss-Meta-Media-Id") != o.MediaID || headers.Get("X-Oss-Hash-Crc64ecma") != o.CRC64 {
		return ErrIntegrity
	}
	_, err = d.backend.client.DeleteObject(ctx, &oss.DeleteObjectRequest{Bucket: &o.Bucket, Key: &o.ObjectKey})
	return safe(ctx, err)
}
func legacyDeleteError(ctx context.Context, err error) error {
	var serviceErr *oss.ServiceError
	if errors.As(err, &serviceErr) && serviceErr.StatusCode == http.StatusNotFound {
		return nil
	}
	return safe(ctx, err)
}
