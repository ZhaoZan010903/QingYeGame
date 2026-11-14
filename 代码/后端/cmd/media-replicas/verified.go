package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type importOptions struct {
	Manifest, Hashes, Receipt, Success, Bucket, Prefix string
	Count                                              int
	Apply                                              bool
}
type replicaRecord struct {
	MediaID               string    `json:"media_id"`
	Purpose               string    `json:"purpose"`
	StorageKey            string    `json:"source_storage_key"`
	ObjectKey             string    `json:"object_key"`
	Bucket                string    `json:"bucket"`
	Region                string    `json:"region"`
	MIME                  string    `json:"content_type"`
	SHA256                string    `json:"verified_source_sha256"`
	CRC64                 string    `json:"remote_crc64"`
	Size                  int64     `json:"size_bytes"`
	Status                string    `json:"status"`
	ACL                   string    `json:"object_acl"`
	VerifiedAt            time.Time `json:"verified_at"`
	DatabaseSHA           string    `json:"database_sha256"`
	DatabaseHashAvailable bool      `json:"database_hash_available"`
}
type completionReceipt struct {
	CompletedAt      time.Time `json:"completed_at"`
	Status           string    `json:"status"`
	Bucket           string    `json:"bucket"`
	Prefix           string    `json:"prefix"`
	Region           string    `json:"region"`
	VerifiedFiles    int       `json:"verified_files"`
	VerifiedBytes    int64     `json:"verified_bytes"`
	ACL              string    `json:"all_object_acls"`
	SourcePreserved  bool      `json:"source_files_preserved"`
	SourceChecks     int       `json:"source_stat_checks"`
	CodeModified     bool      `json:"business_code_modified"`
	DatabaseModified bool      `json:"business_database_modified"`
	ThumbnailsCopied bool      `json:"derived_thumbnail_cache_copied"`
	Verification     []string  `json:"verification"`
}
type verifiedManifest struct {
	Records        []replicaRecord
	SHA256         string
	Bytes          int64
	Legacy, Images int
}

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var idPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)
var bucketNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
var legacyNamePattern = regexp.MustCompile(`^legacy/[0-9a-f]{64}$`)
var imageNamePattern = regexp.MustCompile(`^images/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.(jpg|jpeg|png|webp)$`)

func readBounded(path string, max int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > max {
		return nil, errors.New("private record must be a bounded regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errors.New("private record limit exceeded")
	}
	return data, nil
}
func sha(data []byte) string { value := sha256.Sum256(data); return hex.EncodeToString(value[:]) }
func decodeObject(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("record must be a JSON object")
	}
	fields := map[string]bool{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok || fields[strings.ToLower(key)] {
			return errors.New("duplicate JSON field")
		}
		fields[strings.ToLower(key)] = true
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return err
		}
	}
	if _, err = decoder.Token(); err != nil {
		return err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return errors.New("trailing JSON content")
	}
	return json.Unmarshal(data, target)
}

func loadVerified(options importOptions) (verifiedManifest, error) {
	out := verifiedManifest{}
	if !bucketNamePattern.MatchString(options.Bucket) || options.Count < 1 || options.Count > 100000 ||
		(options.Prefix != "qingye-test/media/" && options.Prefix != "qingye-acceptance/media/") {
		return out, errors.New("invalid explicit import scope")
	}
	if options.Prefix == "qingye-test/media/" && options.Count != 48345 {
		return out, errors.New("test copy requires the complete bound snapshot")
	}
	// All four files are a single completed private evidence bundle. No symlink,
	// arbitrary manifest basename or path from a manifest row is dereferenced.
	paths := []string{options.Manifest, options.Hashes, options.Receipt, options.Success}
	names := []string{"media-object-map-verified.jsonl", "final-record-hashes.json", "completion-receipt.json", "SUCCESS"}
	parent := ""
	for index, path := range paths {
		if filepath.Base(path) != names[index] {
			return out, errors.New("unexpected completion bundle filename")
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return out, err
		}
		real, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return out, err
		}
		if real != absolute {
			return out, errors.New("completion bundle must not contain symlinks")
		}
		if parent == "" {
			parent = filepath.Dir(absolute)
		} else if filepath.Dir(absolute) != parent {
			return out, errors.New("completion files must share one directory")
		}
	}
	rawHashes, err := readBounded(options.Hashes, 1<<20)
	if err != nil {
		return out, err
	}
	hashes := map[string]string{}
	if err = decodeObject(rawHashes, &hashes); err != nil {
		return out, errors.New("invalid hash record")
	}
	for _, name := range []string{names[0], names[2]} {
		if !digestPattern.MatchString(hashes[name]) {
			return out, errors.New("missing checksum binding")
		}
	}
	rawReceipt, err := readBounded(options.Receipt, 1<<20)
	if err != nil {
		return out, err
	}
	if sha(rawReceipt) != hashes[names[2]] {
		return out, errors.New("completion receipt checksum mismatch")
	}
	receiptFields := map[string]json.RawMessage{}
	if err = decodeObject(rawReceipt, &receiptFields); err != nil {
		return out, errors.New("invalid completion receipt")
	}
	for _, key := range []string{"completed_at", "status", "bucket", "prefix", "region", "verified_files", "verified_bytes", "all_object_acls", "source_files_preserved", "source_stat_checks", "business_code_modified", "business_database_modified", "derived_thumbnail_cache_copied", "verification"} {
		value, exists := receiptFields[key]
		if !exists || string(value) == "null" {
			return out, errors.New("incomplete completion receipt")
		}
	}
	var receipt completionReceipt
	if err = json.Unmarshal(rawReceipt, &receipt); err != nil {
		return out, errors.New("invalid completion receipt")
	}
	marker, err := readBounded(options.Success, 4096)
	if err != nil {
		return out, err
	}
	completed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(marker)))
	if err != nil || !completed.Equal(receipt.CompletedAt) || receipt.CompletedAt.IsZero() || receipt.CompletedAt.After(time.Now().Add(5*time.Minute)) {
		return out, errors.New("completion marker is not bound to the receipt")
	}
	required := map[string]bool{"source_sha256": false, "upload_content_md5": false, "put_crc64": false, "head_length_crc64_sha256_metadata": false, "get_object_acl": false, "source_preservation": false, "product_image_order_mapping": false}
	for _, value := range receipt.Verification {
		if _, ok := required[value]; ok {
			required[value] = true
		}
	}
	for _, present := range required {
		if !present {
			return out, errors.New("copy verification is incomplete")
		}
	}
	if receipt.Status != "snapshot_copy_complete" || receipt.Bucket != options.Bucket || receipt.Prefix != options.Prefix || receipt.Region != "cn-beijing" ||
		receipt.VerifiedFiles != options.Count || receipt.VerifiedBytes < 1 || receipt.SourceChecks != options.Count || receipt.ACL != "private" ||
		!receipt.SourcePreserved || receipt.CodeModified || receipt.DatabaseModified || receipt.ThumbnailsCopied {
		return out, errors.New("copy completion scope mismatch")
	}
	if options.Prefix == "qingye-test/media/" && receipt.VerifiedBytes != 26845180398 {
		return out, errors.New("test copy byte total mismatch")
	}
	info, err := os.Lstat(options.Manifest)
	if err != nil {
		return out, err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 256<<20 {
		return out, errors.New("manifest file is not bounded")
	}
	file, err := os.Open(options.Manifest)
	if err != nil {
		return out, err
	}
	defer file.Close()
	digest := sha256.New()
	scanner := bufio.NewScanner(io.TeeReader(io.LimitReader(file, (256<<20)+1), digest))
	scanner.Buffer(make([]byte, 4096), (64<<10)+2)
	ids, keys := map[string]bool{}, map[string]bool{}
	for scanner.Scan() {
		if len(out.Records) >= options.Count || len(scanner.Bytes()) > 64<<10 {
			return out, errors.New("manifest exceeds declared limits")
		}
		var record replicaRecord
		if err = decodeObject(scanner.Bytes(), &record); err != nil {
			return out, errors.New("invalid verified record")
		}
		if err = validateRecord(record, options, receipt.CompletedAt); err != nil {
			return out, err
		}
		if ids[record.MediaID] || keys[record.StorageKey] {
			return out, errors.New("duplicate verified identity")
		}
		ids[record.MediaID] = true
		keys[record.StorageKey] = true
		if strings.HasPrefix(record.StorageKey, "legacy/") {
			out.Legacy++
		} else {
			out.Images++
		}
		out.Records = append(out.Records, record)
		out.Bytes += record.Size
	}
	if scanner.Err() != nil {
		return out, errors.New("cannot read verified manifest within bounds")
	}
	out.SHA256 = hex.EncodeToString(digest.Sum(nil))
	if out.SHA256 != hashes[names[0]] || len(out.Records) != options.Count || out.Bytes != receipt.VerifiedBytes {
		return out, errors.New("manifest content does not match completed copy")
	}
	if options.Prefix == "qingye-test/media/" && (out.Legacy != 48332 || out.Images != 13) {
		return out, errors.New("test copy source counts mismatch")
	}
	return out, nil
}
func validateRecord(r replicaRecord, options importOptions, completed time.Time) error {
	crc, err := strconv.ParseUint(r.CRC64, 10, 64)
	if !idPattern.MatchString(r.MediaID) || r.Purpose != "product" || r.Bucket != options.Bucket || r.Region != "cn-beijing" || r.ACL != "private" ||
		(r.Status != "copied_verified" && r.Status != "existing_verified") || r.Size < 1 || r.Size > 64<<20 || !digestPattern.MatchString(r.SHA256) ||
		err != nil || strconv.FormatUint(crc, 10) != r.CRC64 || r.VerifiedAt.IsZero() || r.VerifiedAt.After(completed) ||
		(!legacyNamePattern.MatchString(r.StorageKey) && !imageNamePattern.MatchString(r.StorageKey)) || r.ObjectKey != options.Prefix+r.StorageKey {
		return errors.New("unverified or out-of-scope replica")
	}
	if r.MIME != "image/png" && r.MIME != "image/jpeg" && r.MIME != "image/gif" && r.MIME != "image/webp" {
		return errors.New("unsupported verified content type")
	}
	if (r.DatabaseHashAvailable || r.DatabaseSHA != "") && (!digestPattern.MatchString(r.DatabaseSHA) || r.DatabaseSHA != r.SHA256) {
		return errors.New("database source digest differs from verification")
	}
	return nil
}
