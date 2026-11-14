package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func takeoverFixture(t *testing.T) (importOptions, []replicaRecord, completionReceipt, []byte, []productImageOrder) {
	t.Helper()
	o, records, receipt := fixture(t)
	o.ProductionTakeover, o.Prefix = true, "qingye-test/media/"
	receipt.Status, receipt.Prefix, receipt.SourcePrefix = "production_takeover_verified", o.Prefix, o.Prefix
	receipt.ReferenceEnvironment, receipt.SourceChecks, receipt.SourceHeadChecks = "production", 0, len(records)
	receipt.Verification = []string{"source_manifest_sha256", "source_head_size_crc64_sha256_metadata", "get_object_acl", "source_preservation", "product_image_order_mapping"}
	records[1].StorageKey = "legacy/" + strings.Repeat("c", 64)
	var source []byte
	var order []productImageOrder
	for i := range records {
		records[i].ObjectKey = o.Prefix + records[i].StorageKey
		raw, _ := json.Marshal(records[i])
		source = append(append(source, raw...), '\n')
		records[i].Status, records[i].SourceObjectKey, records[i].SourceETag = "existing_verified", records[i].ObjectKey, `"existing-original"`
		records[i].VerifiedAt = receipt.CompletedAt
		order = append(order, productImageOrder{MediaID: records[i].MediaID, ProductID: "takeover-product", SortOrder: i})
	}
	// The bound original evidence is complete, while the takeover selects a subset.
	extra := records[0]
	extra.MediaID = "unselected-source"
	extra.StorageKey = "images/11223344-1122-3344-5566-112233445566.jpg"
	extra.ObjectKey = o.Prefix + extra.StorageKey
	raw, _ := json.Marshal(extra)
	source = append(append(source, raw...), '\n')
	receipt.SourceManifestSHA = sha(source)
	writeTakeoverFixture(t, o, records, &receipt, source, order)
	return o, records, receipt, source, order
}

func writeTakeoverFixture(t *testing.T, o importOptions, records []replicaRecord, receipt *completionReceipt, source []byte, order []productImageOrder) {
	t.Helper()
	var orderBytes []byte
	for _, row := range order {
		raw, _ := json.Marshal(row)
		orderBytes = append(append(orderBytes, raw...), '\n')
	}
	receipt.ProductImageOrderSHA = sha(orderBytes)
	writeCopyFixture(t, o, records, *receipt, source)
	path := filepath.Join(filepath.Dir(o.Manifest), takeoverOrderName)
	if err := os.WriteFile(path, orderBytes, 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(o.Hashes)
	hashes := map[string]string{}
	if err := json.Unmarshal(raw, &hashes); err != nil {
		t.Fatal(err)
	}
	hashes[takeoverOrderName] = sha(orderBytes)
	raw, _ = json.Marshal(hashes)
	if err := os.WriteFile(o.Hashes, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestTakeoverBindsExistingSubsetWithoutCopyEvidence(t *testing.T) {
	o, _, _, _, _ := takeoverFixture(t)
	input, err := loadVerified(o)
	if err != nil || !input.ProductionTakeover || len(input.Records) != 2 || input.SourceRecords != 3 || input.Images != 0 || len(input.Order) != 2 {
		t.Fatal("takeover selection failed", err)
	}
	if validateTakeoverCLI(input) == nil {
		t.Fatal("small fixture accepted as complete production snapshot")
	}
}

func TestTakeoverRejectsUnboundOrFalseEvidence(t *testing.T) {
	for _, mode := range []string{"no-explicit-mode", "copy-status", "copy-claim", "copy-response", "source-etag", "source-key", "source-sha", "source-size", "source-heads", "source-stat", "mutation", "environment", "images", "unselected", "order-count", "duplicate-order", "order-id", "missing-order", "order-hash", "source-hash"} {
		t.Run(mode, func(t *testing.T) {
			o, records, receipt, source, order := takeoverFixture(t)
			switch mode {
			case "no-explicit-mode":
				o.ProductionTakeover = false
			case "copy-status":
				records[0].Status = "copied_verified"
			case "copy-claim":
				receipt.Verification = append(receipt.Verification, "copy_source_etag")
			case "copy-response":
				records[0].TargetETag = `"invented-copy"`
			case "source-etag":
				records[0].SourceETag = "*"
			case "source-key":
				records[0].SourceObjectKey = "qingye-production/media/" + records[0].StorageKey
			case "source-sha":
				records[0].SHA256 = strings.Repeat("d", 64)
			case "source-size":
				records[0].Size++
				receipt.VerifiedBytes++
			case "source-heads":
				receipt.SourceHeadChecks--
			case "source-stat":
				receipt.SourceChecks = 1
			case "mutation":
				receipt.StorageMutations = 1
			case "environment":
				receipt.ReferenceEnvironment = "test"
			case "images":
				records[0].StorageKey = "images/11223344-1122-3344-5566-112233445566.jpg"
				records[0].ObjectKey = o.Prefix + records[0].StorageKey
			case "unselected":
				records[0].MediaID = "unselected-source"
			case "order-count":
				order = order[:1]
			case "duplicate-order":
				order[1].SortOrder = order[0].SortOrder
			case "order-id":
				order[0].MediaID = "not-in-manifest"
			case "source-hash":
				receipt.SourceManifestSHA = strings.Repeat("f", 64)
			}
			writeTakeoverFixture(t, o, records, &receipt, source, order)
			path := filepath.Join(filepath.Dir(o.Manifest), takeoverOrderName)
			if mode == "missing-order" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "order-hash" {
				if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := loadVerified(o); err == nil {
				t.Fatal("unbound takeover accepted")
			}
		})
	}
}

func TestTakeoverRegistrationChecksLiveProductOrderAndAllowsOwnedDeletion(t *testing.T) {
	db := replicaTestDB(t)
	ctx := context.Background()
	o, _, _, _, _ := takeoverFixture(t)
	input, err := loadVerified(o)
	if err != nil {
		t.Fatal(err)
	}
	seedReplicaSources(t, db, input)
	if _, err = db.Exec(ctx, `INSERT INTO categories(id,name) VALUES('takeover-category','fixture'); INSERT INTO products(id,category_id,product_code,trade_type,price_unit,status,approved_version,source) VALUES('takeover-product','takeover-category','takeover','sale','account','published',1,'platform')`); err != nil {
		t.Fatal(err)
	}
	for _, r := range input.Order {
		if _, err = db.Exec(ctx, `INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,$2,$3)`, r.ProductID, r.MediaID, r.SortOrder); err != nil {
			t.Fatal(err)
		}
	}
	if added, err := register(ctx, db, input, false); err != nil || added != 2 {
		t.Fatal("takeover preview failed", err)
	}
	if added, err := register(ctx, db, input, true); err != nil || added != 2 {
		t.Fatal("takeover apply failed", err)
	}
	if added, err := register(ctx, db, input, true); err != nil || added != 0 {
		t.Fatal("takeover replay changed mapping", err)
	}
	if _, err = db.Exec(ctx, `UPDATE product_images SET sort_order=sort_order+10`); err != nil {
		t.Fatal(err)
	}
	if _, err = register(ctx, db, input, true); err == nil {
		t.Fatal("reordered gallery accepted")
	}
	// Ownership is normal: no shared marker or trigger prevents physical cleanup.
	if _, err = db.Exec(ctx, `UPDATE media_legacy_replicas SET deleted_at=now() WHERE media_id=$1`, input.Records[0].MediaID); err != nil {
		t.Fatal("owned replica could not complete deletion", err)
	}
}
