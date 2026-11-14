// media-replicas registers a completed, verified legacy copy. It never reads or
// mutates OSS and never deletes media. Preview is the default; --apply is explicit.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		// A raw driver/JSON error can disclose credentials, private paths or records.
		fmt.Fprintln(os.Stderr, "media-replicas: validation or database registration failed; no partial import committed")
		os.Exit(1)
	}
}
func run() error {
	var options importOptions
	flag.StringVar(&options.Manifest, "verified-manifest", "", "completed private JSONL manifest")
	flag.StringVar(&options.Hashes, "record-hashes", "", "private filename-to-SHA256 record")
	flag.StringVar(&options.Receipt, "completion-receipt", "", "completed copy receipt")
	flag.StringVar(&options.Success, "success-file", "", "copy completion marker")
	flag.StringVar(&options.Bucket, "bucket", "", "expected configured OSS bucket")
	flag.StringVar(&options.Prefix, "prefix", "", "exact registered object prefix")
	flag.IntVar(&options.Count, "expected-count", 48345, "expected number of verified objects")
	flag.BoolVar(&options.Apply, "apply", false, "commit verified mappings; otherwise preview only")
	flag.BoolVar(&options.ProductionTakeover, "production-takeover", false, "register verified existing production originals without copying")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected command arguments")
	}
	manifest, err := loadVerified(options)
	if err != nil {
		return err
	}
	if err = validateTakeoverCLI(manifest); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if os.Getenv("DATABASE_URL") == "" {
		return errors.New("database configuration missing")
	}
	config, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	config.MaxConns = 1
	config.ConnConfig.ConnectTimeout = 10 * time.Second
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return err
	}
	defer db.Close()
	added, err := register(ctx, db, manifest, options.Apply)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"mode": map[bool]string{false: "preview", true: "apply"}[options.Apply], "verified_files": len(manifest.Records),
		"verified_bytes": manifest.Bytes, "legacy_files": manifest.Legacy, "image_files": manifest.Images,
		"manifest_sha256": manifest.SHA256, "new_registrations": added, "storage_mutations": 0,
		"production_takeover": manifest.ProductionTakeover,
	})
}

func register(ctx context.Context, db *pgxpool.Pool, input verifiedManifest, apply bool) (int64, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if input.ProductionTakeover {
		// Keep the same product-before-media lock order as business mutations.
		if _, err = tx.Exec(ctx, `LOCK TABLE products,product_images IN SHARE MODE`); err != nil {
			return 0, err
		}
	}
	// Serialize maintenance imports; shared source row locks also serialize purge.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(1834198)`); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `CREATE TEMP TABLE replica_import (
 media_id text PRIMARY KEY,storage_key text UNIQUE NOT NULL,bucket text NOT NULL,object_key text UNIQUE NOT NULL,
 byte_size bigint NOT NULL,sha256 text NOT NULL,crc64 text NOT NULL,verified_at timestamptz NOT NULL,
 database_sha256 text NOT NULL,database_hash_available boolean NOT NULL,manifest_sha256 text NOT NULL,
 product_id text NOT NULL,sort_order integer NOT NULL,mime_type text NOT NULL
 ) ON COMMIT DROP`); err != nil {
		return 0, err
	}
	_, err = tx.CopyFrom(ctx, pgx.Identifier{"replica_import"}, []string{"media_id", "storage_key", "bucket", "object_key", "byte_size", "sha256", "crc64", "verified_at", "database_sha256", "database_hash_available", "manifest_sha256", "product_id", "sort_order", "mime_type"},
		pgx.CopyFromSlice(len(input.Records), func(index int) ([]any, error) {
			r := input.Records[index]
			order := input.Order[r.MediaID]
			return []any{r.MediaID, r.StorageKey, r.Bucket, r.ObjectKey, r.Size, r.SHA256, r.CRC64, r.VerifiedAt, r.DatabaseSHA, r.DatabaseHashAvailable, input.SHA256, order.ProductID, order.SortOrder, r.MIME}, nil
		}))
	if err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, `SELECT m.id FROM media_files m JOIN replica_import i ON i.media_id=m.id ORDER BY m.id FOR SHARE OF m`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	var mismatches int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM replica_import i LEFT JOIN media_files m ON m.id=i.media_id
 WHERE m.id IS NULL OR m.storage_key<>i.storage_key OR m.byte_size<>i.byte_size OR m.purpose<>'product'
 OR m.status<>'ready' OR m.deleted_at IS NOT NULL
 OR (i.database_hash_available AND (coalesce(m.sha256,'')<>i.database_sha256 OR i.database_sha256<>i.sha256))
 OR (NOT i.database_hash_available AND coalesce(m.sha256,'')<>'' AND m.sha256<>i.sha256)
 OR EXISTS(SELECT 1 FROM media_retired_keys k WHERE k.storage_key=i.storage_key)`).Scan(&mismatches)
	if err != nil {
		return 0, err
	}
	if mismatches != 0 {
		return 0, errors.New("manifest identities do not match live source rows")
	}
	if input.ProductionTakeover {
		// The maintenance target is still offline. Lock the selected relationships
		// so registration cannot race a reorder or associate an unrelated original.
		err = tx.QueryRow(ctx, `SELECT count(*) FROM replica_import i
 JOIN media_files m ON m.id=i.media_id
 LEFT JOIN product_images pi ON pi.media_id=i.media_id AND pi.product_id=i.product_id AND pi.sort_order=i.sort_order
 LEFT JOIN products p ON p.id=pi.product_id
 WHERE pi.media_id IS NULL OR p.deleted_at IS NOT NULL OR p.status<>'published' OR p.approved_version IS DISTINCT FROM p.version
 OR m.mime_type<>i.mime_type OR coalesce(m.sha256,'')<>i.sha256
 OR (SELECT count(*) FROM product_images x WHERE x.media_id=i.media_id)<>1`).Scan(&mismatches)
		if err != nil || mismatches != 0 {
			return 0, errors.New("takeover product image association or order differs")
		}
		var productImages int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM product_images`).Scan(&productImages); err != nil || productImages != len(input.Records) || len(input.Order) != len(input.Records) {
			return 0, errors.New("takeover must bind every migrated product image")
		}
	}
	// Existing rows are immutable. An idempotent replay may not overwrite a prior
	// mapping or revive a replica that has already been permanently removed.
	err = tx.QueryRow(ctx, `SELECT count(*) FROM replica_import i JOIN media_legacy_replicas r ON r.media_id=i.media_id
 WHERE (r.storage_key,r.bucket,r.object_key,r.byte_size,r.sha256,r.crc64,r.verified_at,r.manifest_sha256)
 IS DISTINCT FROM (i.storage_key,i.bucket,i.object_key,i.byte_size,i.sha256,i.crc64,i.verified_at,i.manifest_sha256)
 OR r.deleted_at IS NOT NULL`).Scan(&mismatches)
	if err != nil {
		return 0, err
	}
	if mismatches != 0 {
		return 0, errors.New("existing replica registration conflicts")
	}
	err = tx.QueryRow(ctx, `SELECT count(*) FROM replica_import i JOIN media_legacy_replicas r ON r.bucket=i.bucket AND r.object_key=i.object_key WHERE r.media_id<>i.media_id`).Scan(&mismatches)
	if err != nil {
		return 0, err
	}
	if mismatches != 0 {
		return 0, errors.New("registered replica identity conflicts")
	}
	var added int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM replica_import i WHERE NOT EXISTS(SELECT 1 FROM media_legacy_replicas r WHERE r.media_id=i.media_id)`).Scan(&added); err != nil {
		return 0, err
	}
	if !apply {
		return added, tx.Rollback(ctx)
	}
	_, err = tx.Exec(ctx, `INSERT INTO media_legacy_replicas(media_id,storage_key,bucket,object_key,byte_size,sha256,crc64,verified_at,manifest_sha256)
 SELECT media_id,storage_key,bucket,object_key,byte_size,sha256,crc64,verified_at,manifest_sha256 FROM replica_import ON CONFLICT(media_id) DO NOTHING`)
	if err != nil {
		return 0, err
	}
	return added, tx.Commit(ctx)
}
