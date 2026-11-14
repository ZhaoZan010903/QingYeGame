package httpapi

import (
	"context"
	"qingye/backend/internal/application/medialifecycle"
	"qingye/backend/internal/domain/management"
	"qingye/backend/internal/infrastructure/postgres"
	"testing"
	"time"
)

func TestAdminPurgeOldReviewAndReassociatedMediaPublishConcurrently(t *testing.T) {
	a, owner, input := r16Fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	recommendationUser(t, a, "purge-race-admin", "admin")
	actor := management.Actor{ID: "purge-race-admin", Role: "admin"}
	store := postgres.ManagementStore{DB: a.DB, NewID: newID}
	oldID := r16Draft(t, a, owner, input)["id"].(string)
	currentID := r16Draft(t, a, owner, input)["id"].(string)
	stage := &lifecycleMemoryStore{files: map[string][]byte{}}
	oss := &lifecycleMemoryStore{files: map[string][]byte{}}
	lifecycleEnroll(t, a, oldID, owner.ID, "reassociated-purge-image", stage)
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := a.DB.Exec(ctx, statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO product_reviews(id,product_id,decision,version,snapshot) VALUES('old-purge-review',$1,'rejected',1,jsonb_build_object('images',jsonb_build_array(jsonb_build_object('id','reassociated-purge-image'))))`, oldID)
	exec(`DELETE FROM product_images WHERE product_id=$1`, oldID)
	exec(`INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,'reassociated-purge-image',0)`, currentID)
	exec(`UPDATE products SET status='pending' WHERE id=$1`, currentID)
	if _, err := store.TrashProduct(ctx, actor, oldID, false, management.TransitionInput{ExpectedVersion: 1, Reason: "旧商品隔离清理"}); err != nil {
		t.Fatal(err)
	}
	gate, err := a.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err = gate.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "member-quota:"+owner.ID); err != nil {
		t.Fatal(err)
	}
	reviewDone := make(chan error, 1)
	go func() {
		_, e := store.TransitionProduct(ctx, actor, currentID, "review", management.TransitionInput{ExpectedVersion: 1, Decision: "approve"})
		reviewDone <- e
	}()
	// The old object-before-file order could complete while the quota was held,
	// or retain object locks and deadlock a purge holding the same file row.
	select {
	case err := <-reviewDone:
		t.Fatalf("review bypassed owner quota lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	purgeDone := make(chan error, 1)
	go func() {
		_, e := store.PurgeProduct(ctx, actor, oldID, management.PurgeProductInput{ExpectedVersion: 2, Confirmation: "永久删除", RequestID: "reassociated-race"})
		purgeDone <- e
	}()
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, done := range []<-chan error{reviewDone, purgeDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal("purge/review concurrency failed", err)
			}
		case <-ctx.Done():
			t.Fatal("purge/review lock cycle", ctx.Err())
		}
	}
	worker := medialifecycle.Worker{Repository: &postgres.MediaLifecycleStore{DB: a.DB, NewID: newID}, Staging: stage, Published: oss}
	for i := 0; i < 20; i++ {
		worked, err := worker.RunOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	var safe bool
	if err := a.DB.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM products WHERE id=$1 AND purged_at IS NOT NULL)
 AND EXISTS(SELECT 1 FROM products WHERE id=$2 AND status='published' AND approved_version=version AND purged_at IS NULL)
 AND EXISTS(SELECT 1 FROM media_files WHERE id='reassociated-purge-image' AND deleted_at IS NULL AND status='ready')
 AND (SELECT count(*)=2 FROM media_objects WHERE media_id='reassociated-purge-image' AND product_id=$2 AND store='oss' AND deleted_at IS NULL AND NOT force_delete)
 AND EXISTS(SELECT 1 FROM product_reviews WHERE product_id=$2 AND snapshot->'images' @> '[{"id":"reassociated-purge-image"}]')`, oldID, currentID).Scan(&safe); err != nil || !safe {
		t.Fatal("purging an old review blocked current product publication or removed shared media", err)
	}
	if len(oss.files) != 2 {
		t.Fatal("shared media did not reach verified OSS publication", len(oss.files))
	}
}
