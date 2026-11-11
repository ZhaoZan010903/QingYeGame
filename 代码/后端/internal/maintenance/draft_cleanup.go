package maintenance

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"qingye/backend/internal/infrastructure/mediafiles"
	"qingye/backend/internal/infrastructure/postgres"
)

type DraftCleanupResult struct{ Expired, Queued, Removed int }

// Only new, never-published member drafts and tracked new uploads are eligible.
func (s *Service) CleanupDrafts(ctx context.Context, limit int) (DraftCleanupResult, error) {
	out := DraftCleanupResult{}
	if limit < 1 || limit > 200 {
		limit = 100
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(1834186)`).Scan(&locked); err != nil || !locked {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT p.id FROM products p WHERE p.deleted_at IS NULL AND p.status='draft' AND p.source='member' AND p.published_at IS NULL AND p.legacy_id IS NULL AND p.draft_expires_at<=now()
 AND NOT EXISTS(SELECT 1 FROM product_reviews r WHERE r.product_id=p.id AND r.decision='published')
 AND NOT `+postgres.DraftHasActiveUploadsSQL+`
 ORDER BY p.draft_expires_at,p.id LIMIT $1 FOR UPDATE OF p SKIP LOCKED`, limit)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		eligible, e := postgres.DraftCanExpire(ctx, tx, id)
		if e != nil {
			return out, e
		}
		if !eligible {
			continue
		}
		if err = postgres.DeleteNewDraft(ctx, tx, id); err != nil {
			return out, err
		}
		out.Expired++
	}
	rows, err = tx.Query(ctx, `SELECT m.id FROM media_files m WHERE m.managed_upload AND m.source_url='' AND m.deleted_at IS NULL AND COALESCE(m.ready_at,m.created_at)<=now()-interval '72 hours' AND (m.upload_expires_at IS NULL OR m.upload_expires_at<=now()) AND NOT EXISTS(SELECT 1 FROM product_images pi WHERE pi.media_id=m.id)
 AND NOT EXISTS(SELECT 1 FROM contacts c WHERE c.qr_media_id=m.id)
 AND NOT EXISTS(SELECT 1 FROM product_reviews r WHERE r.snapshot->'images' @> jsonb_build_array(jsonb_build_object('id',m.id)))
 AND NOT EXISTS(SELECT 1 FROM media_files other WHERE other.storage_key=m.storage_key AND other.id<>m.id AND other.deleted_at IS NULL)
 ORDER BY m.id LIMIT $1 FOR UPDATE OF m SKIP LOCKED`, limit)
	if err != nil {
		return out, err
	}
	ids = nil
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		if err = postgres.QueueManagedMediaGC(ctx, tx, id); err != nil {
			return out, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	// File removal follows the committed reference removal; failed removals remain retryable.
	attempted := []string{}
	var failures []error
	for i := 0; i < limit; i++ {
		gc, e := s.DB.Begin(ctx)
		if e != nil {
			return out, e
		}
		var id, key string
		e = gc.QueryRow(ctx, `SELECT media_id,storage_key FROM media_gc_queue WHERE completed_at IS NULL AND NOT(media_id=ANY($1::text[])) ORDER BY attempts,created_at,media_id LIMIT 1 FOR UPDATE SKIP LOCKED`, attempted).Scan(&id, &key)
		if e == pgx.ErrNoRows {
			gc.Rollback(ctx)
			break
		}
		if e != nil {
			gc.Rollback(ctx)
			return out, e
		}
		out.Queued++
		attempted = append(attempted, id)
		e = (&mediafiles.Store{Root: s.MediaDir}).RemoveManaged(id, key)
		if e != nil {
			_, _ = gc.Exec(ctx, `UPDATE media_gc_queue SET attempts=attempts+1 WHERE media_id=$1`, id)
			_ = gc.Commit(ctx)
			failures = append(failures, e)
			continue
		}
		if _, e = gc.Exec(ctx, `UPDATE media_gc_queue SET completed_at=now(),attempts=attempts+1 WHERE media_id=$1`, id); e == nil {
			e = gc.Commit(ctx)
		} else {
			gc.Rollback(ctx)
		}
		if e != nil {
			return out, e
		}
		out.Removed++
	}
	return out, errors.Join(failures...)
}
