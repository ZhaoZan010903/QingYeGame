package httpapi

import (
	"context"
	"errors"
	"qingye/backend/internal/application/medialifecycle"
	"qingye/backend/internal/infrastructure/postgres"
	"strings"
	"testing"
)

func TestPermanentQueueRetriesExactObjectAndRejectsResurrection(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	key := "legacy/" + strings.Repeat("a", 64)
	purgeExec(t, f, `INSERT INTO media_files(id,owner_id,storage_key,status,byte_size,mime_type) VALUES('permanent-local',$1,$2,'ready',100,'image/png');
 INSERT INTO media_legacy_replicas(media_id,storage_key,bucket,object_key,byte_size,sha256,crc64,verified_at,manifest_sha256) VALUES('permanent-local',$2,'test-bucket','qingye-test/media/'||$2,100,$3,'123',now(),$3);
 INSERT INTO product_images(product_id,media_id,sort_order) VALUES($4,'permanent-local',0)`, f.member, key, strings.Repeat("a", 64), f.product)
	purgeTrash(t, f, f.product, 1)
	f.call(t, f.first, "POST", "/api/v1/admin/products/"+f.product+"/purge", purgeSingleInput(2, "permanent-queue"), 200)
	for _, query := range []string{
		`INSERT INTO media_files(id,storage_key) VALUES('replacement',$1)`,
		`INSERT INTO contacts(id,name,qr_media_id) VALUES('forbidden','test','permanent-local')`,
		`UPDATE users SET avatar_media_id='permanent-local' WHERE id='` + f.member + `'`,
	} {
		args := []any{}
		if strings.Contains(query, "$1") {
			args = append(args, key)
		}
		if _, err := f.a.DB.Exec(ctx, query, args...); err == nil {
			t.Fatal("retired media resurrected")
		}
	}
	repo := &postgres.MediaPermanentStore{DB: f.a.DB, NewID: newID}
	plans, calls := 0, 0
	plan := func(context.Context, string, string) (string, error) {
		plans++
		return "derived/" + strings.Repeat("b", 64) + ".jpg", nil
	}
	remove := func(_ context.Context, o postgres.PermanentCleanupObject) error {
		calls++
		if o.MediaID != "permanent-local" || o.StorageKey != key {
			t.Fatal("wrong cleanup target")
		}
		if calls == 1 {
			return errors.New("storage unavailable")
		}
		return nil
	}
	if worked, err := repo.StepPermanent(ctx, plan, remove); !worked || err == nil {
		t.Fatal("failed removal not retained for retry", worked, err)
	}
	purgeExec(t, f, `UPDATE media_permanent_cleanup SET retry_at=now()`)
	for i := 0; i < 5; i++ {
		worked, err := repo.StepPermanent(ctx, plan, remove)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	var done bool
	if err := f.a.DB.QueryRow(ctx, `SELECT (SELECT count(*)=2 FROM media_permanent_cleanup WHERE completed_at IS NOT NULL) AND (SELECT deleted_at IS NOT NULL FROM media_legacy_replicas WHERE media_id='permanent-local')`).Scan(&done); err != nil || !done || plans != 1 || calls != 3 {
		t.Fatal("cleanup retry/receipt mismatch", err, done, plans, calls)
	}
	if err := repo.CommitThumbnail(ctx, "permanent-local", func() error { t.Fatal("late thumbnail committed"); return nil }); err == nil {
		t.Fatal("retired thumbnail allowed")
	}
}

func TestPermanentSharedR20ImageSurvivesExpiry(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	f.addProduct(t, "reference-owner")
	stage := &lifecycleMemoryStore{files: map[string][]byte{}}
	lifecycleEnroll(t, f.a, f.product, f.member, "permanent-shared", stage)
	purgeExec(t, f, `INSERT INTO product_reviews(id,product_id,decision,version,snapshot) VALUES('protected-review','reference-owner','published',1,'{"images":[{"id":"permanent-shared"}]}')`)
	purgeTrash(t, f, f.product, 1)
	f.call(t, f.first, "POST", "/api/v1/admin/products/"+f.product+"/purge", purgeSingleInput(2, "shared"), 200)
	purgeExec(t, f, `UPDATE media_objects SET retain_until=now()-interval '1 day',retry_at=now()`)
	repo := &postgres.MediaLifecycleStore{DB: f.a.DB, NewID: newID}
	calls := 0
	transfer := func(context.Context, medialifecycle.Object, string, string) error { calls++; return nil }
	remove := func(context.Context, string, string) error { calls++; return nil }
	for i := 0; i < 4; i++ {
		_, err := repo.Step(ctx, transfer, remove)
		if err != nil {
			t.Fatal(err)
		}
	}
	var intact bool
	if err := f.a.DB.QueryRow(ctx, `SELECT (SELECT deleted_at IS NULL FROM media_files WHERE id='permanent-shared') AND NOT EXISTS(SELECT 1 FROM media_permanent_cleanup) AND NOT EXISTS(SELECT 1 FROM media_objects WHERE deleted_at IS NOT NULL)`).Scan(&intact); err != nil || !intact || calls != 0 {
		t.Fatal("shared R20 image was cleaned", err, intact, calls)
	}
}

func TestPermanentR20PurgeDrainsActiveObjects(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	stage := &lifecycleMemoryStore{files: map[string][]byte{}}
	lifecycleEnroll(t, f.a, f.product, f.member, "permanent-r20", stage)
	purgeTrash(t, f, f.product, 1)
	f.call(t, f.first, "POST", "/api/v1/admin/products/"+f.product+"/purge", purgeSingleInput(2, "r20-purge"), 200)
	repo := &postgres.MediaLifecycleStore{DB: f.a.DB, NewID: newID}
	for i := 0; i < 10; i++ {
		worked, err := repo.Step(ctx, func(context.Context, medialifecycle.Object, string, string) error {
			t.Fatal("purged media transferred")
			return nil
		}, func(ctx context.Context, store, key string) error { return stage.Delete(ctx, key) })
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	if len(stage.files) != 0 || len(stage.deleted) != 2 {
		t.Fatal("R20 originals not removed", len(stage.files), len(stage.deleted))
	}
}
