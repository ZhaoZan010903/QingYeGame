package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
func seedReplicaSources(t *testing.T, db *pgxpool.Pool, input verifiedManifest) {
	t.Helper()
	for _, r := range input.Records {
		_, err := db.Exec(context.Background(), `INSERT INTO media_files(id,purpose,storage_key,byte_size,status,sha256) VALUES($1,'product',$2,$3,'ready',$4)`, r.MediaID, r.StorageKey, r.Size, r.SHA256)
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
