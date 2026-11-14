package mediaobject

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"qingye/backend/internal/domain/media"
)

type LegacyRepository interface {
	ReadLegacyReplica(context.Context, string, string) (media.LegacyReplica, bool, error)
}

type LegacyObjectReader interface {
	OpenReplica(context.Context, media.LegacyReplica) (io.ReadCloser, error)
}

type LegacyFiles struct {
	Repository LegacyRepository
	Store      LegacyObjectReader
	TempDir    string
}

// LegacyReader deliberately has no write/delete methods. Historical access
// never changes the environment used by new R20 objects.
type LegacyReader struct {
	backend    *ossBackend
	prefix     string
	legacyOnly bool
}

func NewLegacyReader(c Config) (*LegacyReader, error) {
	prefix, legacyOnly, err := legacyScope(c)
	if err != nil {
		return nil, err
	}
	store, err := NewOSS(c)
	if err != nil {
		return nil, err
	}
	return &LegacyReader{backend: store.backend.(*ossBackend), prefix: prefix, legacyOnly: legacyOnly}, nil
}

// Open is called only after the same resource authorization and download-budget
// admission as local and R20 reads. Both original and preview use verified bytes;
// neither route needs an original on the application's persistent local disk.
func (s *LegacyFiles) Open(ctx context.Context, key, id string) (*media.File, bool, error) {
	o, found, err := s.Repository.ReadLegacyReplica(ctx, id, key)
	if err != nil || !found {
		return nil, found, err
	}
	if o.MediaID != id || o.StorageKey != key || !validLegacyReplica(o) {
		return nil, true, ErrIntegrity
	}
	f, err := privateRead(ctx, s.TempDir, o.Size, o.SHA256, o.MIME, func() (io.ReadCloser, error) {
		return s.Store.OpenReplica(ctx, o)
	})
	if err == nil {
		var sample [512]byte
		n, sampleErr := io.ReadFull(f.Reader, sample[:])
		if (sampleErr != nil && sampleErr != io.EOF && sampleErr != io.ErrUnexpectedEOF) || http.DetectContentType(sample[:n]) != o.MIME {
			_ = f.Reader.Close()
			return nil, true, ErrIntegrity
		}
		if _, err = f.Reader.Seek(0, io.SeekStart); err != nil {
			_ = f.Reader.Close()
			return nil, true, ErrStorage
		}
		f.Modified = o.Modified
	}
	return f, true, err
}

func validLegacyReplica(o media.LegacyReplica) bool {
	crc, err := strconv.ParseUint(o.CRC64, 10, 64)
	return legacyReplicaKey.MatchString(o.StorageKey) && o.MediaID != "" && validDigest(o.Size, o.SHA256) &&
		err == nil && strconv.FormatUint(crc, 10) == o.CRC64 &&
		(o.MIME == "image/png" || o.MIME == "image/jpeg" || o.MIME == "image/gif" || o.MIME == "image/webp")
}

// OpenReplica pins an explicit read scope, independently of permanent deletion.
// SDK range reads pin the HEAD ETag; privateRead also verifies the entire SHA256.
func (d *LegacyReader) OpenReplica(ctx context.Context, o media.LegacyReplica) (io.ReadCloser, error) {
	if !validLegacyReplica(o) || o.ObjectKey != d.prefix+o.StorageKey || o.Bucket != d.backend.bucket ||
		(d.legacyOnly && !strings.HasPrefix(o.StorageKey, "legacy/")) {
		return nil, ErrIntegrity
	}
	f, err := d.backend.client.OpenFile(ctx, o.Bucket, o.ObjectKey, func(options *oss.OpenOptions) { options.EnablePrefetch = false })
	if err != nil {
		return nil, safe(ctx, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, safe(ctx, err)
	}
	headers, ok := info.Sys().(http.Header)
	if !ok || info.Size() != o.Size || headers.Get("Content-Type") != o.MIME ||
		headers.Get("X-Oss-Meta-Source-Sha256") != o.SHA256 || headers.Get("X-Oss-Meta-Media-Id") != o.MediaID ||
		headers.Get("X-Oss-Hash-Crc64ecma") != o.CRC64 {
		_ = f.Close()
		return nil, ErrIntegrity
	}
	return &safeReader{ctx: ctx, r: f}, nil
}
