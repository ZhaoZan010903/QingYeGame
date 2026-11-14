package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"qingye/backend/internal/application/medialifecycle"
	"time"
)

// lockObject is used only in short transactions. Network I/O never holds an API
// quota/product lock; final promotion rechecks the desired state and generation.
type lifecycleState struct {
	medialifecycle.Object
	Product, Status                 string
	Force, Due, Trashed, PurgeGuard bool
}

func lockLifecycleObject(ctx context.Context, tx pgx.Tx, id, variant string) (lifecycleState, error) {
	var x lifecycleState
	if err := tx.QueryRow(ctx, `SELECT coalesce(product_id,'') FROM media_objects WHERE media_id=$1 AND variant=$2`, id, variant).Scan(&x.Product); err != nil {
		return x, err
	}
	if x.Product != "" {
		if err := tx.QueryRow(ctx, `SELECT status,deleted_at IS NOT NULL AND deleted_by_admin FROM products WHERE id=$1 FOR UPDATE`, x.Product).Scan(&x.Status, &x.Trashed); err != nil {
			return x, err
		}
	}
	var owner string
	if err := tx.QueryRow(ctx, `SELECT coalesce(owner_id,'') FROM media_files WHERE id=$1`, id).Scan(&owner); err != nil {
		return x, err
	}
	if owner != "" {
		if err := lockMemberQuota(ctx, tx, owner); err != nil {
			return x, err
		}
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM media_files WHERE id=$1 FOR UPDATE`, id); err != nil {
		return x, err
	}
	var actualProduct string
	err := tx.QueryRow(ctx, `SELECT media_id,store,object_key,mime_type,sha256,byte_size,force_delete,coalesce(retain_until<=now(),false),coalesce(product_id,''),permanent_purge_guard FROM media_objects WHERE media_id=$1 AND variant=$2 AND deleted_at IS NULL FOR UPDATE`, id, variant).Scan(&x.MediaID, &x.Store, &x.Key, &x.MIME, &x.SHA256, &x.Size, &x.Force, &x.Due, &actualProduct, &x.PurgeGuard)
	if err == nil && actualProduct != x.Product {
		err = pgx.ErrNoRows
	}
	return x, err
}
func desiredStore(x lifecycleState) string {
	if x.Force || x.Trashed || x.Status == "sold" || x.Status == "rented" {
		return ""
	}
	if x.Status == "publishing" && x.Store == "staging" {
		return "oss"
	}
	if x.Due && x.Store == "oss" && x.Status != "published" && x.Status != "publishing" {
		return "staging"
	}
	return ""
}
func (s *MediaLifecycleStore) Step(ctx context.Context, transfer medialifecycle.Transfer, remove medialifecycle.Remove) (bool, error) {
	if done, err := s.cleanup(ctx, remove); done || err != nil {
		return done, err
	}
	var id, variant string
	err := s.DB.QueryRow(ctx, `SELECT o.media_id,o.variant FROM media_objects o LEFT JOIN products p ON p.id=o.product_id
 WHERE o.deleted_at IS NULL AND o.retry_at<=now()
 AND NOT EXISTS(SELECT 1 FROM media_object_moves mv WHERE mv.media_id=o.media_id AND mv.variant=o.variant AND NOT mv.completed AND mv.lease_until>now())
 AND (o.force_delete OR o.retain_until<=now() OR (p.status='publishing' AND o.store='staging') OR EXISTS(SELECT 1 FROM media_object_moves mv WHERE mv.media_id=o.media_id AND mv.variant=o.variant AND NOT mv.completed)) ORDER BY o.retry_at,o.media_id,o.variant LIMIT 1`).Scan(&id, &variant)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	x, err := lockLifecycleObject(ctx, tx, id, variant)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var live bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_object_moves WHERE media_id=$1 AND variant=$2 AND NOT completed AND lease_until>now())`, id, variant).Scan(&live); err != nil {
		return false, err
	}
	if live {
		return false, nil
	}
	if x.PurgeGuard && (x.Force || x.Due && x.Status != "publishing" && x.Status != "published") {
		var referenced bool
		if err = tx.QueryRow(ctx, `SELECT `+permanentMediaReferencedSQL, id, x.Key).Scan(&referenced); err != nil {
			return false, err
		}
		if referenced {
			_, err = tx.Exec(ctx, `UPDATE media_objects SET retry_at=now()+interval '1 hour' WHERE media_id=$1 AND variant=$2`, id, variant)
			if err == nil {
				err = tx.Commit(ctx)
			}
			return true, err
		}
	}
	destination := desiredStore(x)
	if err = cancelMediaMoves(ctx, tx, id, variant, destination); err != nil {
		return false, err
	}
	if x.Force || x.Due && x.Store == "staging" && x.Status != "publishing" && x.Status != "published" && !x.Trashed && x.Status != "sold" && x.Status != "rented" {
		if x.Product != "" && (x.Status == "publishing" || x.Status == "pending") {
			_, err = tx.Exec(ctx, `UPDATE products SET status=CASE WHEN status='publishing' THEN 'offline' ELSE 'draft' END,version=version+1,approved_version=NULL,review_note='商品图片已失效，请重新整理图片后提交审核。',updated_at=now() WHERE id=$1 AND status IN ('publishing','pending')`, x.Product)
			if err != nil {
				return false, err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO media_object_cleanup(store,object_key,media_id,variant) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, x.Store, x.Key, id, variant)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE media_objects SET deleted_at=now(),updated_at=now() WHERE media_id=$1 AND variant=$2`, id, variant)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE media_files SET deleted_at=now(),status='failed',upload_token_hash=NULL WHERE id=$1`, id)
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		return true, err
	}
	if destination == "" {
		// Non-lifecycle states must not repeatedly win the due-row query. A real
		// status transition resets retry_at; ordinary grace periods wake at expiry.
		_, err = tx.Exec(ctx, `UPDATE media_objects SET retry_at=CASE WHEN $3 OR $4 IN ('published','sold','rented') THEN 'infinity'::timestamptz ELSE coalesce(retain_until,'infinity'::timestamptz) END WHERE media_id=$1 AND variant=$2`, id, variant, x.Trashed, x.Status)
		if err == nil {
			err = tx.Commit(ctx)
		}
		return true, err
	}
	var key string
	err = tx.QueryRow(ctx, `SELECT destination_key FROM media_object_moves WHERE source_key=$1 AND destination=$2 AND NOT completed`, x.Key, destination).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		key, err = medialifecycle.NextKey(x.Key, s.NewID())
		if err != nil {
			return false, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO media_object_moves(media_id,variant,source_key,destination,destination_key) VALUES($1,$2,$3,$4,$5) ON CONFLICT(source_key,destination) DO UPDATE SET destination_key=excluded.destination_key,completed=false,lease_token=NULL,lease_until=NULL,ever_started=false`, id, variant, x.Key, destination, key)
		if err == nil {
			err = tx.Commit(ctx)
		}
		return true, err
	}
	if err != nil {
		return false, err
	}
	token := s.NewID()
	tag, err := tx.Exec(ctx, `UPDATE media_object_moves SET lease_token=$3,lease_until=now()+interval '15 minutes',ever_started=true WHERE source_key=$1 AND destination=$2 AND NOT completed AND (lease_until IS NULL OR lease_until<=now())`, x.Key, destination, token)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}
	if err = tx.Commit(ctx); err != nil {
		return true, err
	}
	// This call has no checked-out database connection and can be cancelled.
	transferErr := transfer(ctx, x.Object, destination, key)
	finalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	return true, s.finishTransfer(finalCtx, x, variant, destination, key, token, transferErr)
}
func (s *MediaLifecycleStore) finishTransfer(ctx context.Context, old lifecycleState, variant, destination, key, token string, transferErr error) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	x, err := lockLifecycleObject(ctx, tx, old.MediaID, variant)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var leaseMatches bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_object_moves WHERE source_key=$1 AND destination=$2 AND destination_key=$3 AND lease_token=$4 AND NOT completed)`, old.Key, destination, key, token).Scan(&leaseMatches); e != nil {
		return e
	}
	if !leaseMatches {
		// A timed-out PUT can finish after an earlier DELETE. Re-open the exact
		// retired target's cleanup unless a newer worker or active row owns it.
		if err = retireStaleTargetTx(ctx, tx, old.MediaID, variant, destination, key); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	valid := err == nil && x.Key == old.Key && x.Store == old.Store && desiredStore(x) == destination
	if !valid {
		if _, err = tx.Exec(ctx, `INSERT INTO media_object_cleanup(store,object_key,media_id,variant,reconcile) VALUES($1,$2,$3,$4,true) ON CONFLICT(store,object_key) DO UPDATE SET reconcile=true,completed_at=NULL,retry_at=now(),lease_token=NULL,lease_until=NULL`, destination, key, old.MediaID, variant); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE media_object_moves SET completed=true,lease_token=NULL,lease_until=NULL WHERE source_key=$1 AND destination=$2 AND lease_token=$3`, old.Key, destination, token)
		if err == nil {
			err = tx.Commit(ctx)
		}
		return err
	}
	if transferErr != nil {
		_, err = tx.Exec(ctx, `UPDATE media_object_moves SET lease_token=NULL,lease_until=NULL WHERE source_key=$1 AND destination=$2 AND lease_token=$3`, old.Key, destination, token)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE media_objects SET attempts=attempts+1,last_error='OBJECT_TRANSFER_FAILED',retry_at=now()+least(interval '15 minutes',interval '15 seconds'*(attempts+1)),updated_at=now() WHERE media_id=$1 AND variant=$2`, old.MediaID, variant)
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		if err != nil {
			return err
		}
		return errors.New("media object transfer failed; source retained")
	}
	_, err = tx.Exec(ctx, `INSERT INTO media_object_cleanup(store,object_key,media_id,variant) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, old.Store, old.Key, old.MediaID, variant)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE media_object_moves SET completed=true,lease_token=NULL,lease_until=NULL WHERE source_key=$1 AND destination=$2 AND lease_token=$3`, old.Key, destination, token)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE media_objects SET store=$3,object_key=$4,generation=generation+1,retain_until=CASE WHEN $3='staging' THEN now()+interval '5 days' ELSE NULL END,attempts=0,last_error='',retry_at=now(),updated_at=now() WHERE media_id=$1 AND variant=$2`, old.MediaID, variant, destination, key)
	}
	if err == nil && variant == "original" {
		_, err = tx.Exec(ctx, `UPDATE media_files SET storage_key=$2 WHERE id=$1`, old.MediaID, key)
	}
	if err == nil && destination == "oss" {
		_, err = tx.Exec(ctx, `UPDATE products p SET status='published',approved_version=version,published_at=coalesce(published_at,now()),updated_at=now() WHERE p.id=$1 AND p.status='publishing' AND p.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM product_images pi JOIN media_objects o ON o.media_id=pi.media_id WHERE pi.product_id=p.id AND (o.store<>'oss' OR o.deleted_at IS NOT NULL OR o.force_delete))`, x.Product)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return err
}
