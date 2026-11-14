package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"qingye/backend/internal/infrastructure/postgres"
	"qingye/backend/internal/maintenance"
)

func replicaTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL enables isolated replica registration integration checks")
	}
	ctx := context.Background()
	base, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("integration database unavailable")
	}
	entropy := make([]byte, 12)
	if _, err = rand.Read(entropy); err != nil {
		base.Close()
		t.Fatal(err)
	}
	schema := "replica_test_" + hex.EncodeToString(entropy)
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = base.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		base.Close()
		t.Fatal("isolated schema creation failed")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid integration database configuration")
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = 2
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("isolated pool creation failed")
	}
	t.Cleanup(func() { db.Close(); _, _ = base.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); base.Close() })
	if err = (&maintenance.Service{DB: db}).Migrate(ctx); err != nil {
		t.Fatal("isolated migration failed", err)
	}
	return db
}

func TestProductionReplicaReadsRecheckLiveRegistration(t *testing.T) {
	db := replicaTestDB(t)
	ctx := context.Background()
	o, records, receipt := fixture(t)
	o.Bucket, o.Prefix = "qingye-production-bucket", "qingye-production/media/"
	receipt.Bucket, receipt.Prefix = o.Bucket, o.Prefix
	for i := range records {
		records[i].Bucket = o.Bucket
		records[i].ObjectKey = o.Prefix + records[i].StorageKey
	}
	writeFixture(t, o, records, receipt)
	input, err := loadVerified(o)
	if err != nil {
		t.Fatal(err)
	}
	seedReplicaSources(t, db, input)
	if _, err = register(ctx, db, input, true); err != nil {
		t.Fatal(err)
	}
	r := records[0]
	if _, err = db.Exec(ctx, `UPDATE media_files SET mime_type=$2 WHERE id=$1`, r.MediaID, r.MIME); err != nil {
		t.Fatal(err)
	}
	store := &postgres.MediaLifecycleStore{DB: db}
	got, found, err := store.ReadLegacyReplica(ctx, r.MediaID, r.StorageKey)
	if err != nil || !found || got.ObjectKey != r.ObjectKey || got.SHA256 != r.SHA256 || got.Size != r.Size || got.MIME != r.MIME {
		t.Fatal("registered production copy unavailable", err)
	}
	if _, _, err = store.ReadLegacyReplica(ctx, r.MediaID, "legacy/wrong"); err == nil {
		t.Fatal("stale source key accepted")
	}
	for _, change := range []struct {
		name, mutate, restore string
	}{
		{"size", `UPDATE media_files SET byte_size=byte_size+1 WHERE id=$1`, `UPDATE media_files SET byte_size=byte_size-1 WHERE id=$1`},
		{"digest", `UPDATE media_files SET sha256=repeat('c',64) WHERE id=$1`, `UPDATE media_files SET sha256=NULL WHERE id=$1`},
		{"source deleted", `UPDATE media_files SET deleted_at=now() WHERE id=$1`, `UPDATE media_files SET deleted_at=NULL WHERE id=$1`},
		{"replica deleted", `UPDATE media_legacy_replicas SET deleted_at=now() WHERE media_id=$1`, `UPDATE media_legacy_replicas SET deleted_at=NULL WHERE media_id=$1`},
		{"source not ready", `UPDATE media_files SET status='failed' WHERE id=$1`, `UPDATE media_files SET status='ready' WHERE id=$1`},
		{"retired", `INSERT INTO media_retired_keys(storage_key,media_id) SELECT storage_key,id FROM media_files WHERE id=$1`, `DELETE FROM media_retired_keys WHERE media_id=$1`},
	} {
		t.Run(change.name, func(t *testing.T) {
			if _, err := db.Exec(ctx, change.mutate, r.MediaID); err != nil {
				t.Fatal(err)
			}
			if _, found, err := store.ReadLegacyReplica(ctx, r.MediaID, r.StorageKey); err == nil || !found {
				t.Fatal("inconsistent copy fell back to local storage")
			}
			if _, err := db.Exec(ctx, change.restore, r.MediaID); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err = db.Exec(ctx, `DELETE FROM media_legacy_replicas WHERE media_id=$1`, r.MediaID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.ReadLegacyReplica(ctx, r.MediaID, r.StorageKey); err == nil || !found {
		t.Fatal("unregistered historical original fell back to local storage")
	}
}

func TestLegacyReadAllowsOnlyUnpublishedNewLocalFallback(t *testing.T) {
	db := replicaTestDB(t)
	ctx := context.Background()
	store := &postgres.MediaLifecycleStore{DB: db}
	key := "images/11223344-1122-3344-5566-112233445566.png"
	if _, err := db.Exec(ctx, `INSERT INTO media_files(id,purpose,storage_key,byte_size,status,mime_type) VALUES('new-local','product',$1,512,'ready','image/png')`, key); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.ReadLegacyReplica(ctx, "new-local", key); err != nil || found {
		t.Fatal("new unpublished upload lost local storage", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO contacts(id,name,qr_media_id) VALUES('old-contact','isolated fixture','new-local')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO media_legacy_replicas(media_id,storage_key,bucket,object_key,byte_size,sha256,crc64,verified_at,manifest_sha256)
 VALUES('new-local',$1,'test-bucket','qingye-test/media/'||$1,512,repeat('a',64),'42',now(),repeat('b',64))`, key); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.ReadLegacyReplica(ctx, "new-local", key); err != nil || found {
		t.Fatal("legacy contact image was taken over by an old test registration", err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM media_legacy_replicas WHERE media_id='new-local'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO categories(id,name) VALUES('replica-category','isolated fixture')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO products(id,category_id,product_code,trade_type,price_unit,status,approved_version,source) VALUES('replica-product','replica-category','replica-fixture','sale','account','published',1,'platform')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO product_images(product_id,media_id,sort_order) VALUES('replica-product','new-local',0)`); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.ReadLegacyReplica(ctx, "new-local", key); err == nil || !found {
		t.Fatal("published local product bypassed required verified mapping")
	}
	if _, err := db.Exec(ctx, `DELETE FROM product_images WHERE media_id='new-local'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE media_files SET purpose='avatar' WHERE id='new-local'`); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.ReadLegacyReplica(ctx, "new-local", key); err != nil || found {
		t.Fatal("avatar was routed through product OSS storage", err)
	}
}
func seedReplicaSources(t *testing.T, db *pgxpool.Pool, input verifiedManifest) {
	t.Helper()
	for _, r := range input.Records {
		_, err := db.Exec(context.Background(), `INSERT INTO media_files(id,purpose,storage_key,byte_size,status,sha256,mime_type) VALUES($1,'product',$2,$3,'ready',$4,$5)`, r.MediaID, r.StorageKey, r.Size, r.SHA256, r.MIME)
		if err != nil {
			t.Fatal("source fixture insertion failed", err)
		}
	}
}
func TestReplicaRegistrationPreviewApplyAndIdempotency(t *testing.T) {
	db := replicaTestDB(t)
	ctx := context.Background()
	options, _, _ := fixture(t)
	input, err := loadVerified(options)
	if err != nil {
		t.Fatal(err)
	}
	seedReplicaSources(t, db, input)
	if added, err := register(ctx, db, input, false); err != nil || added != 2 {
		t.Fatal("preview failed", added, err)
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM media_legacy_replicas`).Scan(&count); err != nil || count != 0 {
		t.Fatal("preview changed persistent state", count, err)
	}
	if added, err := register(ctx, db, input, true); err != nil || added != 2 {
		t.Fatal("apply failed", added, err)
	}
	if added, err := register(ctx, db, input, true); err != nil || added != 0 {
		t.Fatal("replay was not idempotent", added, err)
	}
	if err = db.QueryRow(ctx, `SELECT (SELECT count(*) FROM media_permanent_cleanup)+(SELECT count(*) FROM media_retired_keys)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("registration enqueued a storage mutation", count, err)
	}
	if _, err = db.Exec(ctx, `UPDATE media_legacy_replicas SET deleted_at=now() WHERE media_id=$1`, input.Records[0].MediaID); err != nil {
		t.Fatal(err)
	}
	if _, err = register(ctx, db, input, true); err == nil {
		t.Fatal("deleted replica was revived")
	}
}
func TestReplicaRegistrationRejectsSourceAndRegistrationMismatchAtomically(t *testing.T) {
	for _, kind := range []string{"missing source", "different source size", "different source digest", "retired source", "existing map mismatch"} {
		t.Run(kind, func(t *testing.T) {
			db := replicaTestDB(t)
			ctx := context.Background()
			options, _, _ := fixture(t)
			input, err := loadVerified(options)
			if err != nil {
				t.Fatal(err)
			}
			seedReplicaSources(t, db, input)
			var before int
			switch kind {
			case "missing source":
				_, err = db.Exec(ctx, `DELETE FROM media_files WHERE id=$1`, input.Records[1].MediaID)
			case "different source size":
				_, err = db.Exec(ctx, `UPDATE media_files SET byte_size=byte_size+1 WHERE id=$1`, input.Records[1].MediaID)
			case "different source digest":
				_, err = db.Exec(ctx, `UPDATE media_files SET sha256=repeat('a',64) WHERE id=$1`, input.Records[1].MediaID)
			case "retired source":
				_, err = db.Exec(ctx, `UPDATE media_files SET deleted_at=now() WHERE id=$1`, input.Records[1].MediaID)
			case "existing map mismatch":
				_, err = register(ctx, db, input, true)
				if err == nil {
					_, err = db.Exec(ctx, `UPDATE media_legacy_replicas SET crc64='999' WHERE media_id=$1`, input.Records[1].MediaID)
				}
				before = 2
			}
			if err != nil {
				t.Fatal("mismatch fixture failed", err)
			}
			if _, err = register(ctx, db, input, true); err == nil {
				t.Fatal("mismatch was accepted")
			}
			var after int
			if err = db.QueryRow(ctx, `SELECT count(*) FROM media_legacy_replicas`).Scan(&after); err != nil || after != before {
				t.Fatal("failed import partially committed", after, err)
			}
		})
	}
}
