package httpapi

import (
	"context"
	"qingye/backend/internal/domain/media"
	"qingye/backend/internal/infrastructure/postgres"
	"qingye/backend/internal/maintenance"
	"testing"
	"time"
)

// Wait on PostgreSQL's actual lock graph, not a scheduler-dependent sleep.
func waitR16BlockedBy(t *testing.T, a *App, ctx context.Context, blocker uint32) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::integer=ANY(pg_blocking_pids(pid)))`, int32(blocker)).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("operation never reached expected owner lock")
		case <-ticker.C:
		}
	}
}

func TestR16CleanupRechecksTicketCreatedWhileWaiting(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p := r16Draft(t, a, u, in)
	if _, err := a.DB.Exec(ctx, `UPDATE products SET draft_expires_at=now()-interval '1 hour' WHERE id=$1`, p["id"]); err != nil {
		t.Fatal(err)
	}
	creator, err := a.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer creator.Rollback(context.Background())
	if _, err = creator.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "member-quota:"+u.ID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		counts maintenance.DraftCleanupResult
		err    error
	}
	done := make(chan result, 1)
	go func() {
		out, e := (&maintenance.Service{DB: a.DB, MediaDir: a.Config.MediaDir}).CleanupDrafts(ctx, 100)
		done <- result{out, e}
	}()
	waitR16BlockedBy(t, a, ctx, creator.Conn().PgConn().PID())
	// This is the final insert of a ticket creation transaction holding its owner lock.
	if _, err = creator.Exec(ctx, `INSERT INTO media_files(id,owner_id,status,managed_upload,storage_key,upload_expires_at) VALUES('r16-concurrent-ticket',$1,'pending',true,'uploads/r16-concurrent-ticket.raw',now()+interval '15 minutes')`, u.ID); err != nil {
		t.Fatal(err)
	}
	if err = creator.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil || got.counts.Expired != 0 {
		t.Fatalf("active upload lost its draft: %+v %v", got.counts, got.err)
	}
	if _, err = a.memberService().Detail(ctx, u, p["id"].(string)); err != nil {
		t.Fatal(err)
	}
}

func TestR16UploadLocksOwnerBeforeMediaAndReadyGrace(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p := r16Draft(t, a, u, in)
	if _, err := a.DB.Exec(ctx, `UPDATE products SET draft_expires_at=now()-interval '1 hour' WHERE id=$1`, p["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(ctx, `INSERT INTO media_files(id,owner_id,status,managed_upload,storage_key,upload_audience) VALUES('r16-completing',$1,'uploaded',true,'uploads/r16-completing.raw','public')`, u.ID); err != nil {
		t.Fatal(err)
	}
	blocker, err := a.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "member-quota:"+u.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- (&postgres.MediaStore{DB: a.DB}).WithUpload(ctx, "r16-completing", "public", u.ID, func(_ media.Upload, m media.Mutation) error {
			return m.Ready(ctx, media.Processed{Key: "images/r16-completing.jpg", MIME: "image/jpeg", Size: 32})
		})
	}()
	waitR16BlockedBy(t, a, ctx, blocker.Conn().PgConn().PID())
	// If completion locks media before owner this NOWAIT statement fails, proving a lock inversion.
	if _, err = blocker.Exec(ctx, `SELECT id FROM media_files WHERE id='r16-completing' FOR UPDATE NOWAIT`); err != nil {
		t.Fatalf("media acquired before owner: %v", err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	svc := maintenance.Service{DB: a.DB, MediaDir: a.Config.MediaDir}
	got, err := svc.CleanupDrafts(ctx, 100)
	if err != nil || got.Expired != 0 {
		t.Fatalf("ready but not appended upload lost draft: %+v %v", got, err)
	}
	// A user's explicit deletion is not silently prevented by the automatic-cleanup grace period.
	if _, err = a.memberService().DeleteDraft(ctx, u, p["id"].(string), 1); err != nil {
		t.Fatal(err)
	}
}

func TestR16CleanupFirstAndTicketCreationSerialize(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p := r16Draft(t, a, u, in)
	id := p["id"].(string)
	if _, err := a.DB.Exec(ctx, `UPDATE products SET draft_expires_at=now()-interval '1 hour' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	cleanup, err := a.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup.Rollback(context.Background())
	if _, err = cleanup.Exec(ctx, `SELECT id FROM products WHERE id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	eligible, err := postgres.DraftCanExpire(ctx, cleanup, id)
	if err != nil || !eligible {
		t.Fatalf("expiry eligibility: %v %v", eligible, err)
	}
	done := make(chan error, 1)
	go func() {
		done <- (&postgres.MediaStore{DB: a.DB}).Create(ctx, media.Actor{ID: u.ID, Role: "member", Surface: "public"}, media.CreateInput{Filename: "new.jpg", MIME: "image/jpeg", Size: 32}, "r16-after-cleanup", "uploads/r16-after-cleanup.raw", "unused")
	}()
	waitR16BlockedBy(t, a, ctx, cleanup.Conn().PgConn().PID())
	if err = postgres.DeleteNewDraft(ctx, cleanup, id); err != nil {
		t.Fatal(err)
	}
	if err = cleanup.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	var tombstone bool
	if err = a.DB.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM products WHERE id=$1`, id).Scan(&tombstone); err != nil || !tombstone {
		t.Fatal("cleanup lost linearization")
	}
}

func TestR16ProtectedOrphansDoNotStarveGC(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	p := r16Draft(t, a, u, in)
	_, err := a.DB.Exec(ctx, `INSERT INTO media_files(id,status,managed_upload,storage_key,ready_at,created_at) SELECT 'a-protected-'||n,'ready',true,'images/a-protected-'||n||'.jpg',now()-interval '4 days',now()-interval '4 days' FROM generate_series(1,5) n`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec(ctx, `INSERT INTO product_reviews(id,product_id,decision,version,snapshot) SELECT 'protected-review',$1,'rejected',1,jsonb_build_object('images',jsonb_agg(jsonb_build_object('id',id))) FROM media_files WHERE id LIKE 'a-protected-%'`, p["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec(ctx, `INSERT INTO media_files(id,status,managed_upload,storage_key,ready_at,created_at) VALUES('z-orphan','ready',true,'images/z-orphan.jpg',now()-interval '4 days',now()-interval '4 days')`); err != nil {
		t.Fatal(err)
	}
	got, err := (&maintenance.Service{DB: a.DB, MediaDir: a.Config.MediaDir}).CleanupDrafts(ctx, 2)
	if err != nil || got.Removed != 1 {
		t.Fatalf("protected candidates starved later orphan: %+v %v", got, err)
	}
	var kept int
	if err = a.DB.QueryRow(ctx, `SELECT count(*) FROM media_files WHERE id LIKE 'a-protected-%' AND deleted_at IS NULL`).Scan(&kept); err != nil || kept != 5 {
		t.Fatalf("snapshot media removed: %d %v", kept, err)
	}
}
