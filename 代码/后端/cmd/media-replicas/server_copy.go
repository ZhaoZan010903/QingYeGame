package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const copySourceManifestName = "source-media-object-map-verified.jsonl"

// Source bytes come from the completed, previously verified original manifest,
// not an invented re-upload receipt. Only the fixed same-directory file is read.
func loadCopySources(parent string, receipt completionReceipt, hashes map[string]string) (map[string]replicaRecord, error) {
	path := filepath.Join(parent, copySourceManifestName)
	real, err := filepath.EvalSymlinks(path)
	if err != nil || real != path || hashes[copySourceManifestName] != receipt.SourceManifestSHA {
		return nil, errors.New("source manifest is not bound to the copy bundle")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 256<<20 {
		return nil, errors.New("source manifest must be a bounded regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	hash := sha256.New()
	scanner := bufio.NewScanner(io.TeeReader(io.LimitReader(f, (256<<20)+1), hash))
	scanner.Buffer(make([]byte, 4096), (64<<10)+2)
	sources, keys := map[string]replicaRecord{}, map[string]bool{}
	scope := importOptions{Bucket: receipt.Bucket, Prefix: receipt.SourcePrefix}
	for scanner.Scan() {
		if len(sources) >= 100000 || len(scanner.Bytes()) > 64<<10 {
			return nil, errors.New("source manifest exceeds bounded scope")
		}
		var r replicaRecord
		if err = decodeObject(scanner.Bytes(), &r); err != nil {
			return nil, errors.New("invalid verified source record")
		}
		if err = validateRecord(r, scope, receipt.CompletedAt); err != nil {
			return nil, err
		}
		if _, ok := sources[r.MediaID]; ok || keys[r.StorageKey] {
			return nil, errors.New("duplicate verified source identity")
		}
		sources[r.MediaID], keys[r.StorageKey] = r, true
	}
	if scanner.Err() != nil || len(sources) == 0 || hex.EncodeToString(hash.Sum(nil)) != receipt.SourceManifestSHA {
		return nil, errors.New("source manifest checksum mismatch")
	}
	return sources, nil
}

func validCopyETag(value string) bool {
	// Preserve the exact opaque ETag including quotes; no wildcard/HTTP-list or
	// control character can stand in for the captured source identity.
	return len(value) >= 2 && len(value) <= 256 && strings.TrimSpace(value) == value &&
		strings.IndexFunc(value, func(r rune) bool { return r < 0x21 || r > 0x7e }) == -1 &&
		!strings.ContainsAny(value, ",*") && !strings.HasPrefix(value, "W/")
}

func validateCopiedRecord(r replicaRecord, sources map[string]replicaRecord) error {
	source, ok := sources[r.MediaID]
	if !ok || source.StorageKey != r.StorageKey || source.ObjectKey != r.SourceObjectKey ||
		source.Bucket != r.Bucket || source.MIME != r.MIME || source.Size != r.Size || source.SHA256 != r.SHA256 || source.CRC64 != r.CRC64 ||
		r.VerifiedAt.Before(source.VerifiedAt) || !validCopyETag(r.SourceETag) || !validCopyETag(r.TargetETag) {
		return errors.New("copied object differs from the verified source")
	}
	if r.Status == "copied_verified" && (r.CopySourceIfMatch != r.SourceETag || r.CopiedCRC64 != r.CRC64) {
		return errors.New("copy response is not bound to the source ETag and CRC64")
	}
	if r.Status == "existing_verified" && ((r.CopySourceIfMatch != "" && r.CopySourceIfMatch != r.SourceETag) || (r.CopiedCRC64 != "" && r.CopiedCRC64 != r.CRC64)) {
		return errors.New("existing copy contains conflicting transfer evidence")
	}
	return nil
}
