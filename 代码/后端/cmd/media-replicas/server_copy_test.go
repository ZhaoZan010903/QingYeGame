package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCopyFixture(t *testing.T, o importOptions, records []replicaRecord, receipt completionReceipt, source []byte) {
	t.Helper()
	writeFixture(t, o, records, receipt)
	path := filepath.Join(filepath.Dir(o.Manifest), copySourceManifestName)
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(o.Hashes)
	if err != nil {
		t.Fatal(err)
	}
	hashes := map[string]string{}
	if err = json.Unmarshal(raw, &hashes); err != nil {
		t.Fatal(err)
	}
	hashes[copySourceManifestName] = sha(source)
	raw, err = json.Marshal(hashes)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(o.Hashes, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func serverCopyFixture(t *testing.T) (importOptions, []replicaRecord, completionReceipt, []byte) {
	t.Helper()
	o, records, receipt := fixture(t)
	o.Prefix, o.Bucket = "qingye-production/media/", "qingye-production-bucket"
	receipt.Prefix, receipt.Bucket = o.Prefix, o.Bucket
	receipt.Status, receipt.SourcePrefix = "server_side_copy_complete", "qingye-test/media/"
	receipt.SourceChecks, receipt.SourceHeadChecks = 0, len(records)
	receipt.Verification = []string{"source_manifest_sha256", "source_head_size_crc64_sha256_metadata", "copy_source_etag", "target_head_size_crc64_sha256_metadata", "get_object_acl", "source_preservation", "product_image_order_mapping"}
	var source []byte
	for i := range records {
		s := records[i]
		s.Bucket, s.ObjectKey = o.Bucket, receipt.SourcePrefix+s.StorageKey
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		source = append(source, raw...)
		source = append(source, '\n')
		records[i].Bucket, records[i].ObjectKey = o.Bucket, o.Prefix+s.StorageKey
		records[i].SourceObjectKey, records[i].SourceETag = s.ObjectKey, `"source-object-etag"`
		records[i].TargetETag = `"target-object-etag"`
		records[i].CopySourceIfMatch, records[i].CopiedCRC64 = records[i].SourceETag, s.CRC64
		records[i].VerifiedAt = receipt.CompletedAt
	}
	receipt.SourceManifestSHA = sha(source)
	writeCopyFixture(t, o, records, receipt, source)
	return o, records, receipt, source
}

func TestProductionServerCopyBindsOriginalManifestAndTransfer(t *testing.T) {
	o, _, _, _ := serverCopyFixture(t)
	got, err := loadVerified(o)
	if err != nil || len(got.Records) != 2 || got.Bytes != 1024 {
		t.Fatal("complete server-side copy evidence rejected", err)
	}
}

func TestProductionServerCopyRejectsFalseOrIncompleteEvidence(t *testing.T) {
	for _, mode := range []string{"source-hash", "source-prefix", "source-head-count", "claimed-source-stat", "claimed-put", "missing-verification", "source-object", "source-etag", "copy-condition", "copy-crc", "target-etag", "content-sha", "content-size", "media-id", "missing-source", "tampered-source", "test-scope"} {
		t.Run(mode, func(t *testing.T) {
			o, records, receipt, source := serverCopyFixture(t)
			switch mode {
			case "source-hash":
				receipt.SourceManifestSHA = strings.Repeat("b", 64)
			case "source-prefix":
				receipt.SourcePrefix = "qingye-production/media/"
			case "source-head-count":
				receipt.SourceHeadChecks--
			case "claimed-source-stat":
				receipt.SourceChecks = 2
			case "claimed-put":
				receipt.Verification = append(receipt.Verification, "put_crc64")
			case "missing-verification":
				receipt.Verification = receipt.Verification[1:]
			case "source-object":
				records[0].SourceObjectKey = "qingye-acceptance/media/" + records[0].StorageKey
			case "source-etag":
				records[0].SourceETag = "*"
			case "copy-condition":
				records[0].CopySourceIfMatch = `"another-source"`
			case "copy-crc":
				records[0].CopiedCRC64 = "999"
			case "target-etag":
				records[0].TargetETag = ""
			case "content-sha":
				records[0].SHA256 = strings.Repeat("c", 64)
			case "content-size":
				records[0].Size++
				receipt.VerifiedBytes++
			case "media-id":
				records[0].MediaID = "not-in-source"
			case "test-scope":
				o.Prefix, receipt.Prefix = "qingye-acceptance/media/", "qingye-acceptance/media/"
				for i := range records {
					records[i].ObjectKey = o.Prefix + records[i].StorageKey
				}
			}
			writeCopyFixture(t, o, records, receipt, source)
			if mode == "missing-source" {
				if err := os.Remove(filepath.Join(filepath.Dir(o.Manifest), copySourceManifestName)); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "tampered-source" {
				if err := os.WriteFile(filepath.Join(filepath.Dir(o.Manifest), copySourceManifestName), append(source, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := loadVerified(o); err == nil {
				t.Fatal("unbound production copy accepted")
			}
		})
	}
}

func TestExistingProductionCopyDoesNotInventUploadOrCopyResponse(t *testing.T) {
	o, records, receipt, source := serverCopyFixture(t)
	for i := range records {
		records[i].Status = "existing_verified"
		records[i].CopySourceIfMatch, records[i].CopiedCRC64 = "", ""
	}
	writeCopyFixture(t, o, records, receipt, source)
	if _, err := loadVerified(o); err != nil {
		t.Fatal("verified existing copy must not require an invented copy response", err)
	}
	records[0].CopiedCRC64 = "999"
	writeCopyFixture(t, o, records, receipt, source)
	if _, err := loadVerified(o); err == nil {
		t.Fatal("conflicting response accepted for existing target")
	}
}
