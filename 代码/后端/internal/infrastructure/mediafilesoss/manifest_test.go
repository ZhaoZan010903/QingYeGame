//go:build oss_read

package mediafilesoss

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

const manifestTestBucket = "qingye-images-test"

func validManifestRecord() manifestRecord {
	key := "legacy/" + strings.Repeat("a", 64)
	return manifestRecord{
		MediaID: "000180e4-e91a-5715-81d0-e6dffd935fae", Purpose: "product",
		StorageKey: key, ObjectKey: objectPrefix + key, Bucket: manifestTestBucket,
		MIME: "image/png", SHA256: strings.Repeat("b", 64), CRC64: "18446744073709551615",
		Size: 506773, Status: "copied_verified", ACL: "private", VerifiedAt: "2026-11-12T15:00:00.123456+00:00",
	}
}

func encodeManifestRecord(t *testing.T, record manifestRecord) string {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestManifestLoadsVerifiedExtensionlessImages(t *testing.T) {
	for _, status := range []string{"copied_verified", "existing_verified"} {
		for _, mime := range []string{"image/png", "image/jpeg", "image/gif", "image/webp"} {
			t.Run(status+"/"+mime, func(t *testing.T) {
				record := validManifestRecord()
				record.Status, record.MIME = status, mime
				manifest, err := LoadManifest(strings.NewReader(encodeManifestRecord(t, record)+"\r\n"), manifestTestBucket)
				if err != nil {
					t.Fatal(err)
				}
				entry, ok := manifest.lookup(record.MediaID, record.StorageKey)
				if !ok || entry.ObjectKey != objectPrefix+record.StorageKey || entry.MIME != mime || entry.Size != record.Size {
					t.Fatal("verified extensionless image mapping lost")
				}
			})
		}
	}
}

func TestManifestAcceptsKnownImagesKeysAndIgnoresReceiptExtras(t *testing.T) {
	for _, extension := range []string{"jpg", "jpeg", "png", "webp"} {
		record := validManifestRecord()
		record.StorageKey = "images/" + record.MediaID + "." + extension
		record.ObjectKey = objectPrefix + record.StorageKey
		record.SHA256 = strings.Repeat("AB", 32)
		record.CRC64 = "00042"
		encoded := strings.TrimSuffix(encodeManifestRecord(t, record), "}") + `,"remote_etag":"receipt-extra","source_absolute_path":"/private/example"}`
		manifest, err := LoadManifest(strings.NewReader(encoded), manifestTestBucket)
		if err != nil {
			t.Fatal(err)
		}
		entry, ok := manifest.lookup(record.MediaID, record.StorageKey)
		if !ok || entry.SHA256 != strings.Repeat("ab", 32) || entry.CRC64 != "42" {
			t.Fatal("known images key or canonical checksums rejected")
		}
	}
}

func TestManifestRejectsUnsafeOrUnverifiedRecords(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*manifestRecord)
	}{
		{"planned", func(r *manifestRecord) { r.Status = "planned_not_uploaded" }},
		{"source_only", func(r *manifestRecord) { r.Status = "source_verified_not_uploaded" }},
		{"unknown_status", func(r *manifestRecord) { r.Status = "uploaded" }},
		{"chat", func(r *manifestRecord) { r.Purpose = "chat" }},
		{"avatar", func(r *manifestRecord) { r.Purpose = "avatar" }},
		{"missing_purpose", func(r *manifestRecord) { r.Purpose = "" }},
		{"wrong_bucket", func(r *manifestRecord) { r.Bucket = "other-bucket" }},
		{"public_acl", func(r *manifestRecord) { r.ACL = "public-read" }},
		{"inherited_acl", func(r *manifestRecord) { r.ACL = "default" }},
		{"missing_timestamp", func(r *manifestRecord) { r.VerifiedAt = "" }},
		{"invalid_timestamp", func(r *manifestRecord) { r.VerifiedAt = "yesterday" }},
		{"missing_media_id", func(r *manifestRecord) { r.MediaID = "" }},
		{"control_media_id", func(r *manifestRecord) { r.MediaID = "secret\nvalue" }},
		{"zero_size", func(r *manifestRecord) { r.Size = 0 }},
		{"negative_size", func(r *manifestRecord) { r.Size = -1 }},
		{"oversize", func(r *manifestRecord) { r.Size = maxObjectBytes + 1 }},
		{"active_mime", func(r *manifestRecord) { r.MIME = "image/svg+xml" }},
		{"html_mime", func(r *manifestRecord) { r.MIME = "text/html" }},
		{"missing_mime", func(r *manifestRecord) { r.MIME = "" }},
		{"short_sha", func(r *manifestRecord) { r.SHA256 = "abcd" }},
		{"nonhex_sha", func(r *manifestRecord) { r.SHA256 = strings.Repeat("g", 64) }},
		{"missing_crc", func(r *manifestRecord) { r.CRC64 = "" }},
		{"negative_crc", func(r *manifestRecord) { r.CRC64 = "-1" }},
		{"signed_crc", func(r *manifestRecord) { r.CRC64 = "+1" }},
		{"overflow_crc", func(r *manifestRecord) { r.CRC64 = "18446744073709551616" }},
		{"object_prefix", func(r *manifestRecord) { r.ObjectKey = "another/" + r.StorageKey }},
		{"object_mismatch", func(r *manifestRecord) { r.ObjectKey = objectPrefix + "legacy/" + strings.Repeat("c", 64) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := validManifestRecord()
			test.mutate(&record)
			manifest, err := LoadManifest(strings.NewReader(encodeManifestRecord(t, record)), manifestTestBucket)
			if err == nil || manifest != nil {
				t.Fatal("unsafe or unverified manifest accepted")
			}
			if strings.Contains(err.Error(), record.MediaID) && record.MediaID != "" {
				t.Fatal("error discloses original record")
			}
		})
	}
}

func TestManifestRejectsUnsafeKeys(t *testing.T) {
	for _, key := range []string{
		"", "../secret", "legacy/../secret", "/legacy/" + strings.Repeat("a", 64),
		"legacy/" + strings.Repeat("a", 64) + "?secret=value", "legacy/" + strings.Repeat("a", 64) + "#fragment",
		"legacy\\" + strings.Repeat("a", 64), "legacy/%2e%2e/secret", "legacy/" + strings.Repeat("a", 64) + "\x00",
		"https://other.invalid/image", "uploads/id.raw", "derived/id.jpg", "images/not-a-uuid.jpg",
		"images/000180e4-e91a-5715-81d0-e6dffd935fae.svg", "images/000180e4-e91a-5715-81d0-e6dffd935fae.png/other",
	} {
		record := validManifestRecord()
		record.StorageKey, record.ObjectKey = key, objectPrefix+key
		if manifest, err := LoadManifest(strings.NewReader(encodeManifestRecord(t, record)), manifestTestBucket); err == nil || manifest != nil {
			t.Fatal("unsafe storage key accepted")
		}
	}
}

func TestManifestRejectsDuplicateIdentityAndStorageKey(t *testing.T) {
	first := validManifestRecord()
	for _, kind := range []string{"id", "key"} {
		second := first
		if kind == "id" {
			second.StorageKey = "legacy/" + strings.Repeat("c", 64)
			second.ObjectKey = objectPrefix + second.StorageKey
		} else {
			second.MediaID = "another-media-id"
		}
		input := encodeManifestRecord(t, first) + "\n" + encodeManifestRecord(t, second)
		if manifest, err := LoadManifest(strings.NewReader(input), manifestTestBucket); err == nil || manifest != nil {
			t.Fatal("duplicate manifest identity accepted")
		}
	}
}

func TestManifestLookupIsBoundToIDAndKeyAndReturnsCopy(t *testing.T) {
	record := validManifestRecord()
	manifest, err := LoadManifest(strings.NewReader(encodeManifestRecord(t, record)), manifestTestBucket)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.lookup("other-id", record.StorageKey); ok {
		t.Fatal("foreign ID matched")
	}
	if _, ok := manifest.lookup(record.MediaID, "images/other-key.png"); ok {
		t.Fatal("changed storage key matched")
	}
	entry, ok := manifest.lookup(record.MediaID, record.StorageKey)
	if !ok {
		t.Fatal("verified entry missing")
	}
	entry.ObjectKey = "modified"
	entry.Size = 0
	unchanged, ok := manifest.lookup(record.MediaID, record.StorageKey)
	if !ok || unchanged.ObjectKey != record.ObjectKey || unchanged.Size != record.Size {
		t.Fatal("caller mutated manifest entry")
	}
	var absent *Manifest
	if _, ok := absent.lookup(record.MediaID, record.StorageKey); ok {
		t.Fatal("nil manifest returned entry")
	}
}

type manifestErrorReader struct{}

func (manifestErrorReader) Read([]byte) (int, error) {
	return 0, errors.New("private source error with SECRET_VALUE")
}

func TestManifestRejectsMalformedEmptyOrUnreadableInput(t *testing.T) {
	for _, source := range []io.Reader{nil, strings.NewReader(""), strings.NewReader("\n"), strings.NewReader("{SECRET_VALUE}"), strings.NewReader("null"), manifestErrorReader{}} {
		manifest, err := LoadManifest(source, manifestTestBucket)
		if err == nil || manifest != nil {
			t.Fatal("invalid manifest input accepted")
		}
		if strings.Contains(err.Error(), "SECRET_VALUE") {
			t.Fatal("reader error leaks private source")
		}
	}
	for _, bucket := range []string{"", "UPPERCASE", "ab", "https://host", "../bucket", strings.Repeat("a", 64)} {
		if manifest, err := LoadManifest(strings.NewReader(encodeManifestRecord(t, validManifestRecord())), bucket); err == nil || manifest != nil {
			t.Fatal("invalid bucket parameter accepted")
		}
	}
}

func TestManifestEnforcesLineLimit(t *testing.T) {
	base := strings.TrimSuffix(encodeManifestRecord(t, validManifestRecord()), "}") + `,"extra":"`
	line := base + strings.Repeat("x", maxManifestLineBytes-len(base)-2) + `"}`
	if len(line) != maxManifestLineBytes {
		t.Fatal("invalid line limit fixture")
	}
	if _, err := LoadManifest(strings.NewReader(line+"\r\n"), manifestTestBucket); err != nil {
		t.Fatalf("line at limit rejected: %v", err)
	}
	if manifest, err := LoadManifest(strings.NewReader(strings.Replace(line, `,"extra":"`, `,"extra":"x`, 1)), manifestTestBucket); err == nil || manifest != nil {
		t.Fatal("overlong line accepted")
	}
}

type generatedManifestReader struct {
	remaining int
	index     int
	pending   []byte
}

func (r *generatedManifestReader) Read(destination []byte) (int, error) {
	if len(destination) == 0 {
		return 0, nil
	}
	if len(r.pending) == 0 {
		if r.remaining == 0 {
			return 0, io.EOF
		}
		record := validManifestRecord()
		record.MediaID = fmt.Sprintf("media-%d", r.index)
		record.StorageKey = fmt.Sprintf("legacy/%064x", r.index)
		record.ObjectKey = objectPrefix + record.StorageKey
		r.pending, _ = json.Marshal(record)
		r.pending = append(r.pending, '\n')
		r.index++
		r.remaining--
	}
	n := copy(destination, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func TestManifestEnforcesEntryLimit(t *testing.T) {
	manifest, err := LoadManifest(&generatedManifestReader{remaining: maxManifestEntries}, manifestTestBucket)
	if err != nil || len(manifest.entries) != maxManifestEntries {
		t.Fatalf("entry count at limit rejected: %v", err)
	}
	manifest = nil
	if manifest, err = LoadManifest(&generatedManifestReader{remaining: maxManifestEntries + 1}, manifestTestBucket); err == nil || manifest != nil {
		t.Fatal("entry count above limit accepted")
	}
}
