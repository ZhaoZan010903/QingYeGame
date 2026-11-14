package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (importOptions, []replicaRecord, completionReceipt) {
	t.Helper()
	directory := t.TempDir()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	options := importOptions{Manifest: filepath.Join(directory, "media-object-map-verified.jsonl"), Hashes: filepath.Join(directory, "final-record-hashes.json"), Receipt: filepath.Join(directory, "completion-receipt.json"), Success: filepath.Join(directory, "SUCCESS"), Bucket: "qingye-acceptance-bucket", Prefix: "qingye-acceptance/media/", Count: 2}
	first := replicaRecord{MediaID: "test-legacy", Purpose: "product", StorageKey: "legacy/" + strings.Repeat("a", 64), Bucket: options.Bucket, Region: "cn-beijing", MIME: "image/png", SHA256: strings.Repeat("b", 64), CRC64: "123", Size: 512, Status: "copied_verified", ACL: "private", VerifiedAt: now.Add(-time.Minute)}
	first.ObjectKey = options.Prefix + first.StorageKey
	second := first
	second.MediaID = "11223344-1122-3344-5566-112233445566"
	second.StorageKey = "images/" + second.MediaID + ".jpg"
	second.ObjectKey = options.Prefix + second.StorageKey
	second.Status = "existing_verified"
	receipt := completionReceipt{CompletedAt: now, Status: "snapshot_copy_complete", Bucket: options.Bucket, Prefix: options.Prefix, Region: "cn-beijing", VerifiedFiles: 2, VerifiedBytes: 1024, ACL: "private", SourcePreserved: true, SourceChecks: 2, Verification: []string{"source_sha256", "upload_content_md5", "put_crc64", "head_length_crc64_sha256_metadata", "get_object_acl", "source_preservation", "product_image_order_mapping"}}
	records := []replicaRecord{first, second}
	writeFixture(t, options, records, receipt)
	return options, records, receipt
}
func writeFixture(t *testing.T, o importOptions, records []replicaRecord, receipt completionReceipt) {
	t.Helper()
	var lines []byte
	for _, record := range records {
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, raw...)
		lines = append(lines, '\n')
	}
	rawReceipt, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	hashes, err := json.Marshal(map[string]string{"media-object-map-verified.jsonl": sha(lines), "completion-receipt.json": sha(rawReceipt)})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{o.Manifest: lines, o.Receipt: rawReceipt, o.Hashes: hashes, o.Success: []byte(receipt.CompletedAt.Format(time.RFC3339Nano) + "\n")} {
		if err = os.WriteFile(name, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func TestVerifiedImportChecksCompleteBoundBundle(t *testing.T) {
	o, _, _ := fixture(t)
	got, err := loadVerified(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != 2 || got.Bytes != 1024 || got.Legacy != 1 || got.Images != 1 || !digestPattern.MatchString(got.SHA256) {
		t.Fatal("incomplete import summary")
	}
	if o.Apply {
		t.Fatal("fixture must preserve preview default")
	}
}

func TestProductionReplicaImportRequiresIndependentBoundPrefix(t *testing.T) {
	o, records, receipt := fixture(t)
	o.Bucket, o.Prefix = "qingye-production-bucket", "qingye-production/media/"
	receipt.Bucket, receipt.Prefix = o.Bucket, o.Prefix
	for i := range records {
		records[i].Bucket = o.Bucket
		records[i].ObjectKey = o.Prefix + records[i].StorageKey
	}
	writeFixture(t, o, records, receipt)
	if _, err := loadVerified(o); err != nil {
		t.Fatal("verified production scope rejected", err)
	}
	records[0].ObjectKey = "qingye-test/media/" + records[0].StorageKey
	writeFixture(t, o, records, receipt)
	if _, err := loadVerified(o); err == nil {
		t.Fatal("production import accepted a test object")
	}
}
func TestVerifiedJSONRejectsAmbiguousDuplicateFields(t *testing.T) {
	for _, raw := range []string{`{"media_id":"one","media_id":"two"}`, `{"media_id":"one","MEDIA_ID":"two"}`, `{} {}`, `null`, `[]`} {
		var record replicaRecord
		if err := decodeObject([]byte(raw), &record); err == nil {
			t.Fatal("ambiguous record accepted")
		}
	}
}
func TestVerifiedImportRejectsTamperedOrUnboundFiles(t *testing.T) {
	cases := []struct {
		name  string
		alter func(*testing.T, importOptions)
	}{
		{"manifest changed", func(t *testing.T, o importOptions) {
			f, e := os.OpenFile(o.Manifest, os.O_APPEND|os.O_WRONLY, 0600)
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			f.WriteString("\n")
		}},
		{"receipt changed", func(t *testing.T, o importOptions) {
			if e := os.WriteFile(o.Receipt, []byte("{}"), 0600); e != nil {
				t.Fatal(e)
			}
		}},
		{"success not bound", func(t *testing.T, o importOptions) {
			if e := os.WriteFile(o.Success, []byte("2026-01-02T03:04:06Z\n"), 0600); e != nil {
				t.Fatal(e)
			}
		}},
		{"success missing", func(t *testing.T, o importOptions) {
			if e := os.Remove(o.Success); e != nil {
				t.Fatal(e)
			}
		}},
		{"hash missing", func(t *testing.T, o importOptions) {
			if e := os.WriteFile(o.Hashes, []byte("{}"), 0600); e != nil {
				t.Fatal(e)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, _, _ := fixture(t)
			c.alter(t, o)
			if _, err := loadVerified(o); err == nil {
				t.Fatal("unsafe evidence accepted")
			}
		})
	}
}
func TestVerifiedImportRejectsInvalidRecordsEvenWithMatchingHashes(t *testing.T) {
	cases := []struct {
		name  string
		alter func(*replicaRecord)
	}{
		{"pending", func(r *replicaRecord) { r.Status = "planned" }},
		{"wrong purpose", func(r *replicaRecord) { r.Purpose = "avatar" }},
		{"public ACL", func(r *replicaRecord) { r.ACL = "public-read" }},
		{"wrong bucket", func(r *replicaRecord) { r.Bucket = "other-bucket" }},
		{"wrong prefix", func(r *replicaRecord) { r.ObjectKey = "qingye-test/media/" + r.StorageKey }},
		{"traversal", func(r *replicaRecord) {
			r.StorageKey = "legacy/../a"
			r.ObjectKey = "qingye-acceptance/media/" + r.StorageKey
		}},
		{"bad digest", func(r *replicaRecord) { r.SHA256 = strings.Repeat("z", 64) }},
		{"database digest changed", func(r *replicaRecord) { r.DatabaseHashAvailable = true; r.DatabaseSHA = strings.Repeat("c", 64) }},
		{"crc overflow", func(r *replicaRecord) { r.CRC64 = "18446744073709551616" }},
		{"crc noncanonical", func(r *replicaRecord) { r.CRC64 = "+123" }},
		{"future verification", func(r *replicaRecord) { r.VerifiedAt = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC) }},
		{"oversized", func(r *replicaRecord) { r.Size = 64<<20 + 1 }},
		{"active MIME", func(r *replicaRecord) { r.MIME = "image/svg+xml" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, records, receipt := fixture(t)
			c.alter(&records[0])
			writeFixture(t, o, records, receipt)
			if _, err := loadVerified(o); err == nil {
				t.Fatal("invalid record accepted")
			}
		})
	}
}
func TestVerifiedImportRejectsScopeAndDuplicateIdentities(t *testing.T) {
	for _, kind := range []string{"count", "bytes", "duplicate id", "duplicate key", "test incomplete", "receipt incomplete", "verification missing"} {
		t.Run(kind, func(t *testing.T) {
			o, records, receipt := fixture(t)
			switch kind {
			case "count":
				o.Count = 3
			case "bytes":
				receipt.VerifiedBytes++
			case "duplicate id":
				records[1].MediaID = records[0].MediaID
			case "duplicate key":
				records[1].StorageKey = records[0].StorageKey
				records[1].ObjectKey = records[0].ObjectKey
			case "test incomplete":
				o.Prefix = "qingye-test/media/"
			case "receipt incomplete":
				receipt.SourcePreserved = false
			case "verification missing":
				receipt.Verification = receipt.Verification[1:]
			}
			writeFixture(t, o, records, receipt)
			if _, err := loadVerified(o); err == nil {
				t.Fatal("scope or identity violation accepted")
			}
		})
	}
}
