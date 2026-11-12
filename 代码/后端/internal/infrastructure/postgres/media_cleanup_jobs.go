package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"qingye/backend/internal/application/medialifecycle"
	"time"
)

// A completed, revoked or abandoned processor cannot keep unregistered objects
// for another five days. A live replacement lease still protects its candidates.
const stageCandidateDueSQL = `(c.expires_at<=now() OR m.upload_cleanup_requested OR m.deleted_at IS NOT NULL
 OR m.status IN ('ready','failed') OR m.processing_until<=now())
 AND NOT(m.processing_token IS NOT NULL AND m.processing_until>now())
 AND NOT EXISTS(SELECT 1 FROM media_objects o WHERE o.object_key=c.object_key AND o.deleted_at IS NULL)`

func retireStaleTargetTx(ctx context.Context, tx pgx.Tx, id, variant, store, key string) error {
	_, err := tx.Exec(ctx, `INSERT INTO media_object_cleanup(store,object_key,media_id,variant,reconcile)
 SELECT $1,$2,$3,$4,true WHERE NOT EXISTS(SELECT 1 FROM media_objects WHERE object_key=$2 AND store=$1 AND deleted_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM media_object_moves WHERE destination_key=$2 AND destination=$1 AND NOT completed)
 ON CONFLICT(store,object_key) DO UPDATE SET completed_at=NULL,retry_at=now(),lease_token=NULL,lease_until=NULL,reconcile=true`, store, key, id, variant)
	return err
}
func cancelMediaMoves(ctx context.Context, tx pgx.Tx, id, variant, keep string) error {
	_, err := tx.Exec(ctx, `INSERT INTO media_object_cleanup(store,object_key,media_id,variant,reconcile) SELECT destination,destination_key,media_id,variant,ever_started FROM media_object_moves WHERE media_id=$1 AND variant=$2 AND NOT completed AND destination<>$3 AND (lease_until IS NULL OR lease_until<=now()) ON CONFLICT DO NOTHING`, id, variant, keep)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE media_object_moves SET completed=true WHERE media_id=$1 AND variant=$2 AND NOT completed AND destination<>$3 AND (lease_until IS NULL OR lease_until<=now())`, id, variant, keep)
	}
	return err
}
func (s *MediaLifecycleStore) cleanup(ctx context.Context, remove medialifecycle.Remove) (bool, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var candidate, mid, variant string
	err = tx.QueryRow(ctx, `SELECT c.object_key,c.media_id,c.variant FROM media_stage_candidates c JOIN media_files m ON m.id=c.media_id WHERE `+stageCandidateDueSQL+` ORDER BY c.expires_at LIMIT 1`).Scan(&candidate, &mid, &variant)
	if err == nil {
		var owner string
		if err = tx.QueryRow(ctx, `SELECT coalesce(owner_id,'') FROM media_files WHERE id=$1`, mid).Scan(&owner); err != nil {
			return false, err
		}
		if owner != "" {
			if err = lockMemberQuota(ctx, tx, owner); err != nil {
				return false, err
			}
		}
		if _, err = tx.Exec(ctx, `SELECT id FROM media_files WHERE id=$1 FOR UPDATE`, mid); err != nil {
			return false, err
		}
		if err = tx.QueryRow(ctx, `SELECT c.object_key FROM media_stage_candidates c JOIN media_files m ON m.id=c.media_id WHERE c.object_key=$1 AND `+stageCandidateDueSQL+` FOR UPDATE OF c`, candidate).Scan(&candidate); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return false, nil
			}
			return false, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO media_object_cleanup(store,object_key,media_id,variant,reconcile) VALUES('staging',$1,$2,$3,true) ON CONFLICT(store,object_key) DO UPDATE SET reconcile=true,completed_at=NULL,retry_at=now(),lease_token=NULL,lease_until=NULL`, candidate, mid, variant)
		if err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM media_stage_candidates WHERE object_key=$1`, candidate)
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		return true, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	var store, key string
	err = tx.QueryRow(ctx, `SELECT store,object_key FROM media_object_cleanup WHERE (completed_at IS NULL OR reconcile) AND retry_at<=now() AND (lease_until IS NULL OR lease_until<=now()) ORDER BY retry_at,created_at LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&store, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_objects WHERE store=$1 AND object_key=$2 AND deleted_at IS NULL)`, store, key).Scan(&active); err != nil {
		return false, err
	}
	if active {
		return false, errors.New("refusing active object cleanup")
	}
	token := s.NewID()
	if _, err = tx.Exec(ctx, `UPDATE media_object_cleanup SET lease_token=$3,lease_until=now()+interval '15 minutes' WHERE store=$1 AND object_key=$2`, store, key, token); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	deleteErr := remove(ctx, store, key)
	finalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if deleteErr != nil {
		_, err = s.DB.Exec(finalCtx, `UPDATE media_object_cleanup SET attempts=attempts+1,retry_at=now()+interval '1 minute',lease_token=NULL,lease_until=NULL WHERE store=$1 AND object_key=$2 AND lease_token=$3`, store, key, token)
		if err != nil {
			return true, err
		}
		return true, errors.New("retired object cleanup failed; will retry")
	}
	_, err = s.DB.Exec(finalCtx, `UPDATE media_object_cleanup SET completed_at=now(),attempts=attempts+1,lease_token=NULL,lease_until=NULL,retry_at=CASE WHEN reconcile THEN now()+interval '24 hours' ELSE retry_at END WHERE store=$1 AND object_key=$2 AND lease_token=$3`, store, key, token)
	return true, err
}
