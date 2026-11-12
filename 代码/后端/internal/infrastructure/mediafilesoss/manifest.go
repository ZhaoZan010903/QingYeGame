//go:build oss_read

package mediafilesoss

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const objectPrefix = "qingye-test/media/"
const maxObjectBytes int64 = 64 << 20

const maxManifestEntries = 100000
const maxManifestLineBytes = 64 << 10

var (
	legacyKeyPattern = regexp.MustCompile(`^legacy/[0-9a-f]{64}$`)
	imageKeyPattern  = regexp.MustCompile(`^images/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.(jpg|jpeg|png|webp)$`)
	bucketPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
)

// Entry 只包含已验证的商品图片映射，不保存临时签名链接或账户凭据。
type Entry struct {
	MediaID, Purpose, StorageKey, ObjectKey, Bucket, MIME, SHA256, CRC64 string
	Size                                                                 int64
}

// Manifest 加载完成后不可变；按媒体 ID 与原存储键共同匹配，不能替代业务鉴权。
type Manifest struct {
	entries map[string]Entry
}

type manifestRecord struct {
	MediaID    string `json:"media_id"`
	Purpose    string `json:"purpose"`
	StorageKey string `json:"source_storage_key"`
	ObjectKey  string `json:"object_key"`
	Bucket     string `json:"bucket"`
	MIME       string `json:"content_type"`
	SHA256     string `json:"verified_source_sha256"`
	CRC64      string `json:"remote_crc64"`
	Size       int64  `json:"size_bytes"`
	Status     string `json:"status"`
	ACL        string `json:"object_acl"`
	VerifiedAt string `json:"verified_at"`
}

// LoadManifest 仅接受上传完成后的 JSONL 回执；计划清单不能使未迁移图片进入 OSS 读路径。
func LoadManifest(source io.Reader, bucket string) (*Manifest, error) {
	if source == nil || !bucketPattern.MatchString(bucket) {
		return nil, errors.New("invalid OSS manifest configuration")
	}
	manifest := &Manifest{entries: make(map[string]Entry)}
	keys := make(map[string]struct{})
	scanner := bufio.NewScanner(source)
	// Scanner 还需容纳换行符；另外检查有效行长度，避免上限被实现细节放宽。
	scanner.Buffer(make([]byte, 4096), maxManifestLineBytes+2)
	for scanner.Scan() {
		if len(manifest.entries) >= maxManifestEntries {
			return nil, errors.New("OSS manifest entry limit exceeded")
		}
		if len(scanner.Bytes()) > maxManifestLineBytes {
			return nil, errors.New("OSS manifest line limit exceeded")
		}
		var record manifestRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, errors.New("invalid OSS manifest record")
		}
		entry, ok := validatedManifestEntry(record, bucket)
		if !ok {
			return nil, errors.New("invalid OSS manifest record")
		}
		if _, exists := manifest.entries[entry.MediaID]; exists {
			return nil, errors.New("duplicate OSS manifest media ID")
		}
		if _, exists := keys[entry.StorageKey]; exists {
			return nil, errors.New("duplicate OSS manifest storage key")
		}
		manifest.entries[entry.MediaID] = entry
		keys[entry.StorageKey] = struct{}{}
	}
	if scanner.Err() != nil {
		// 输入可能来自私有清单；不把原始行或 Reader 错误写入可见日志。
		return nil, errors.New("cannot read OSS manifest within limits")
	}
	if len(manifest.entries) == 0 {
		return nil, errors.New("empty OSS manifest")
	}
	return manifest, nil
}

func validatedManifestEntry(record manifestRecord, bucket string) (Entry, bool) {
	if record.MediaID == "" || len(record.MediaID) > 100 || !utf8.ValidString(record.MediaID) ||
		strings.IndexFunc(record.MediaID, unicode.IsControl) >= 0 ||
		record.Purpose != "product" || record.Bucket != bucket || record.ACL != "private" ||
		(record.Status != "copied_verified" && record.Status != "existing_verified") ||
		record.Size < 1 || record.Size > maxObjectBytes {
		return Entry{}, false
	}
	if !legacyKeyPattern.MatchString(record.StorageKey) && !imageKeyPattern.MatchString(record.StorageKey) {
		return Entry{}, false
	}
	if record.ObjectKey != objectPrefix+record.StorageKey {
		return Entry{}, false
	}
	switch record.MIME {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return Entry{}, false
	}
	if _, err := time.Parse(time.RFC3339Nano, record.VerifiedAt); err != nil {
		return Entry{}, false
	}
	if len(record.SHA256) != 64 {
		return Entry{}, false
	}
	if _, err := hex.DecodeString(record.SHA256); err != nil {
		return Entry{}, false
	}
	if record.CRC64 == "" || strings.IndexFunc(record.CRC64, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return Entry{}, false
	}
	crc, err := strconv.ParseUint(record.CRC64, 10, 64)
	if err != nil {
		return Entry{}, false
	}
	return Entry{
		MediaID: record.MediaID, Purpose: record.Purpose, StorageKey: record.StorageKey,
		ObjectKey: record.ObjectKey, Bucket: record.Bucket, MIME: record.MIME,
		SHA256: strings.ToLower(record.SHA256), CRC64: strconv.FormatUint(crc, 10), Size: record.Size,
	}, true
}

func (m *Manifest) lookup(id, key string) (Entry, bool) {
	if m == nil {
		return Entry{}, false
	}
	entry, ok := m.entries[id]
	if !ok || entry.StorageKey != key {
		return Entry{}, false
	}
	return entry, true
}
