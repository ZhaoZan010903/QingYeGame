package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"qingye/backend/internal/application/medialifecycle"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
	"qingye/backend/internal/domain/member"
	dsecurity "qingye/backend/internal/domain/security"
	"qingye/backend/internal/infrastructure/postgres"
	"qingye/backend/internal/maintenance"
)

type lifecycleMemoryStore struct {
	files      map[string][]byte
	failVerify bool
	deleted    []string
}

func (s *lifecycleMemoryStore) Put(ctx context.Context, key string, r io.Reader, size int64, mime, sha string) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.files[key] = raw
	return nil
}
func (s *lifecycleMemoryStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	raw, ok := s.files[key]
	if !ok {
		return nil, errors.New("missing")
	}
	return io.NopCloser(bytes.NewReader(raw)), nil
}
func (s *lifecycleMemoryStore) Verify(ctx context.Context, key string, size int64, sha string) error {
	sum := sha256.Sum256(s.files[key])
	if s.failVerify || int64(len(s.files[key])) != size || hex.EncodeToString(sum[:]) != sha {
		return errors.New("checksum")
	}
	return nil
}
func (s *lifecycleMemoryStore) Delete(ctx context.Context, key string) error {
	delete(s.files, key)
	s.deleted = append(s.deleted, key)
	return nil
}
func (s *lifecycleMemoryStore) SignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func lifecycleEnroll(t *testing.T, a *App, product, owner, id string, staging *lifecycleMemoryStore) {
	t.Helper()
	ctx := context.Background()
	raw := bytes.Repeat([]byte("fixture"), 300)
	sum := sha256.Sum256(raw)
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO media_files(id,owner_id,filename,mime_type,byte_size,storage_key,status,managed_upload,lifecycle_managed,ready_at) VALUES($1,$2,'test.png','image/png',$3,$4,'ready',true,true,now())`, id, owner, len(raw), "r20/test/objects/"+id+"/initial/original")
	if err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"original", "preview"} {
		key := "r20/test/objects/" + id + "/initial/" + variant
		staging.files[key] = raw
		if err = postgres.RegisterMediaObjectTx(ctx, tx, id, variant, key, "image/png", hex.EncodeToString(sum[:]), int64(len(raw))); err != nil {
			t.Fatal(err)
		}
	}
	if product != "" {
		_, err = tx.Exec(ctx, `INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,$2,0)`, product, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestR20LifecycleVerifiedPublishReturnRelistAndExpiry(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	p := r16Draft(t, a, u, in)
	id := p["id"].(string)
	staging := &lifecycleMemoryStore{files: map[string][]byte{}}
	oss := &lifecycleMemoryStore{files: map[string][]byte{}}
	lifecycleEnroll(t, a, id, u.ID, "r20-image", staging)
	repo := &postgres.MediaLifecycleStore{DB: a.DB, NewID: newID}
	worker := medialifecycle.Worker{Repository: repo, Staging: staging, Published: oss}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := a.DB.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	state := func(want string) {
		t.Helper()
		var got string
		if err := a.DB.QueryRow(ctx, `SELECT status FROM products WHERE id=$1`, id).Scan(&got); err != nil || got != want {
			t.Fatalf("state=%s want=%s err=%v", got, want, err)
		}
	}
	drain := func() {
		t.Helper()
		for i := 0; i < 30; i++ {
			worked, err := worker.RunOnce(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !worked {
				return
			}
		}
		t.Fatal("worker did not drain")
	}
	exec(`UPDATE products SET status='published',approved_version=version WHERE id=$1`, id)
	state("publishing")
	oss.failVerify = true
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	} // persistent target reservation
	if _, err := worker.RunOnce(ctx); err == nil {
		t.Fatal("checksum failure accepted")
	}
	state("publishing")
	if len(staging.files) != 2 || len(staging.deleted) != 0 {
		t.Fatal("source deleted before verify")
	}
	var plans int
	if err := a.DB.QueryRow(ctx, `SELECT count(*) FROM media_object_moves WHERE NOT completed`).Scan(&plans); err != nil || plans != 1 {
		t.Fatal(plans, err)
	}
	oss.failVerify = false
	exec(`UPDATE media_objects SET retry_at=now()`)
	drain()
	state("published")
	if len(staging.files) != 0 || len(oss.files) != 2 {
		t.Fatal("publish copies", len(staging.files), len(oss.files))
	}
	var fiveDays bool
	exec(`UPDATE products SET status='offline',version=version+1,approved_version=NULL WHERE id=$1`, id)
	if err := a.DB.QueryRow(ctx, `SELECT bool_and(retain_until>now()+interval '4 days 23 hours') FROM media_objects`).Scan(&fiveDays); err != nil || !fiveDays {
		t.Fatal("missing OSS grace", err)
	}
	if worked, err := worker.RunOnce(ctx); worked || err != nil {
		t.Fatal("premature migration", worked, err)
	}
	exec(`UPDATE media_objects SET retain_until=now()-interval '1 second'`)
	drain()
	if len(staging.files) != 2 || len(oss.files) != 0 {
		t.Fatal("return copies")
	}
	if err := a.DB.QueryRow(ctx, `SELECT bool_and(retain_until>now()+interval '4 days 23 hours') FROM media_objects`).Scan(&fiveDays); err != nil || !fiveDays {
		t.Fatal("ECS grace not migration-based", err)
	}
	// Relist cancels retention and uses fresh generations; old cleanup cannot hit them.
	exec(`UPDATE products SET status='published',version=version+1,approved_version=version+1 WHERE id=$1`, id)
	drain()
	state("published")
	for key := range oss.files {
		if strings.Contains(key, "/initial/") {
			t.Fatal("reused source generation")
		}
	}
	exec(`UPDATE products SET status='offline',version=version+1,approved_version=NULL WHERE id=$1`, id)
	exec(`UPDATE media_objects SET retain_until=now()-interval '1 second'`)
	drain()
	exec(`UPDATE media_objects SET retain_until=now()-interval '1 second'`)
	drain()
	if len(staging.files) != 0 || len(oss.files) != 0 {
		t.Fatal("expiry retained objects")
	}
	if _, err := a.DB.Exec(ctx, `UPDATE products SET status='published' WHERE id=$1`, id); err == nil {
		t.Fatal("expired product published")
	}
}

func TestR20UploadRestrictionOnlyOwnStagingAndCancellation(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	p := r16Draft(t, a, u, in)
	id := p["id"].(string)
	recommendationUser(t, a, "r20-admin", "admin")
	recommendationUser(t, a, "r20-other", "member")
	stage := &lifecycleMemoryStore{files: map[string][]byte{}}
	oss := &lifecycleMemoryStore{files: map[string][]byte{}}
	lifecycleEnroll(t, a, id, u.ID, "blocked-stage", stage)
	lifecycleEnroll(t, a, "", "r20-other", "other-stage", stage)
	if _, err := a.DB.Exec(ctx, `UPDATE products SET status='published',approved_version=version WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	store := postgres.UploadSecurityStore{DB: a.DB, NewID: newID}
	if _, err := store.Restrict(ctx, "r20-admin", u.ID, dsecurity.RestrictInput{Blocked: true, Reason: "测试确认", PurgeStaging: true}); err != nil {
		t.Fatal(err)
	}
	worker := medialifecycle.Worker{Repository: &postgres.MediaLifecycleStore{DB: a.DB, NewID: newID}, Staging: stage, Published: oss}
	for i := 0; i < 12; i++ {
		worked, err := worker.RunOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	if len(stage.files) != 2 {
		t.Fatal("wrong account scope", len(stage.files))
	}
	var status string
	if err := a.DB.QueryRow(ctx, `SELECT status FROM products WHERE id=$1`, id).Scan(&status); err != nil || status != "offline" {
		t.Fatal("purged publishing item stuck", status, err)
	}
	for key := range stage.files {
		if !strings.Contains(key, "other-stage") {
			t.Fatal("banned account retained")
		}
	}
	if _, err := store.Restrict(ctx, "r20-admin", u.ID, dsecurity.RestrictInput{Blocked: false, ExpectedVersion: 0, Reason: "stale"}); err == nil {
		t.Fatal("stale version accepted")
	}
	if _, err := store.Restrict(ctx, "r20-admin", u.ID, dsecurity.RestrictInput{Blocked: false, ExpectedVersion: 1, Reason: "恢复"}); err != nil {
		t.Fatal(err)
	}
	if target, _, err := management.Transition("pending", "member", "review", management.TransitionInput{Decision: "reject_final", Reason: "确认违规"}); err != nil || target != "rejected_final" {
		t.Fatal(target, err)
	}
}

func TestR20TransferDoesNotLockEditsAndStaleTargetIsCleaned(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	p := r16Draft(t, a, u, in)
	id := p["id"].(string)
	stage := &lifecycleMemoryStore{files: map[string][]byte{}}
	oss := &lifecycleMemoryStore{files: map[string][]byte{}}
	lifecycleEnroll(t, a, id, u.ID, "cancel-inflight", stage)
	repo := &postgres.MediaLifecycleStore{DB: a.DB, NewID: newID}
	worker := medialifecycle.Worker{Repository: repo, Staging: stage, Published: oss}
	if _, err := a.DB.Exec(ctx, `UPDATE products SET status='published',approved_version=version WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := repo.Step(ctx, func(ctx context.Context, o medialifecycle.Object, destination, key string) error {
		editCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if _, err := a.DB.Exec(editCtx, `UPDATE products SET status='offline',version=version+1,approved_version=NULL WHERE id=$1`, id); err != nil {
			return err
		}
		oss.files[key] = append([]byte(nil), stage.files[o.Key]...)
		return nil
	}, func(ctx context.Context, store, key string) error {
		if store == "oss" {
			return oss.Delete(ctx, key)
		}
		return stage.Delete(ctx, key)
	})
	if err != nil {
		t.Fatal("edit was blocked by network transfer", err)
	}
	for i := 0; i < 12; i++ {
		worked, err := worker.RunOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	if len(stage.files) != 2 || len(oss.files) != 0 {
		t.Fatal("stale promotion or lost source", len(stage.files), len(oss.files))
	}
	var status string
	if err := a.DB.QueryRow(ctx, `SELECT status FROM products WHERE id=$1`, id).Scan(&status); err != nil || status != "offline" {
		t.Fatal(status, err)
	}
}

func TestR20StageCandidateRecoveryHonorsProcessingLease(t *testing.T) {
	a, u, _ := r16Fixture(t)
	ctx := context.Background()
	repo := &postgres.MediaLifecycleStore{DB: a.DB, NewID: newID}
	key := "r20/test/objects/crashed/attempt/original"
	_, err := a.DB.Exec(ctx, `INSERT INTO media_files(id,owner_id,filename,mime_type,byte_size,storage_key,status,managed_upload,lifecycle_managed,processing_token,processing_until) VALUES('crashed',$1,'test.png','image/png',2048,'uploads/crashed.raw','uploaded',true,true,'lease',now()+interval '5 minutes')`, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.StageCandidate(ctx, "crashed", "lease", "original", key, "image/png", strings.Repeat("a", 64), 2048); err != nil {
		t.Fatal(err)
	}
	stage := &lifecycleMemoryStore{files: map[string][]byte{key: bytes.Repeat([]byte{1}, 2048)}}
	worker := medialifecycle.Worker{Repository: repo, Staging: stage, Published: &lifecycleMemoryStore{files: map[string][]byte{}}}
	if done, err := worker.RunOnce(ctx); err != nil || done {
		t.Fatal("active finalizer cleaned", done, err)
	}
	if _, err = a.DB.Exec(ctx, `UPDATE media_files SET processing_until=now()-interval '1 second' WHERE id='crashed'`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err = worker.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(stage.files) != 0 {
		t.Fatal("crash orphan retained")
	}
}

func TestR20WorkerLeasesAndLateCompletionDoNotHoldConnections(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	p := r16Draft(t, a, u, in)
	id := p["id"].(string)
	stage := &lifecycleMemoryStore{files: map[string][]byte{}}
	lifecycleEnroll(t, a, id, u.ID, "lease-image", stage)
	repo := &postgres.MediaLifecycleStore{DB: a.DB, NewID: newID}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.DB.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE products SET status='published',approved_version=version WHERE id=$1`, id)
	exec(`UPDATE media_objects SET retry_at=now()+interval '1 hour' WHERE variant='preview'`)
	noopRemove := func(context.Context, string, string) error { return nil }
	noopTransfer := func(context.Context, medialifecycle.Object, string, string) error { return nil }
	if _, err := repo.Step(ctx, noopTransfer, noopRemove); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	var calls atomic.Int32
	go func() {
		_, err := repo.Step(ctx, func(context.Context, medialifecycle.Object, string, string) error {
			calls.Add(1)
			close(entered)
			<-release
			return errors.New("late failure")
		}, noopRemove)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first worker never entered")
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	if got := a.DB.Stat().AcquiredConns(); got != 0 {
		t.Fatalf("network transfer holds %d database connections", got)
	}
	if worked, err := repo.Step(ctx, func(context.Context, medialifecycle.Object, string, string) error { calls.Add(1); return nil }, noopRemove); err != nil || worked {
		t.Fatal("second worker stole live lease", worked, err)
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate transfer")
	}
	// Simulate a crashed worker's expired lease; its late response must not mutate the winner.
	exec(`UPDATE media_object_moves SET lease_until=now()-interval '1 second' WHERE NOT completed`)
	if _, err := repo.Step(ctx, noopTransfer, noopRemove); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("late completion blocked")
	}
	var generation, attempts int
	var location string
	if err := a.DB.QueryRow(ctx, `SELECT generation,attempts,store FROM media_objects WHERE media_id='lease-image' AND variant='original'`).Scan(&generation, &attempts, &location); err != nil || generation != 2 || attempts != 0 || location != "oss" {
		t.Fatal(generation, attempts, location, err)
	}
}

func TestR20RelistDuringReturnKeepsOSSAndCleansStaleTarget(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	p := r16Draft(t, a, u, in)
	id := p["id"].(string)
	stage := &lifecycleMemoryStore{files: map[string][]byte{}}
	oss := &lifecycleMemoryStore{files: map[string][]byte{}}
	lifecycleEnroll(t, a, id, u.ID, "return-image", stage)
	repo := &postgres.MediaLifecycleStore{DB: a.DB, NewID: newID}
	worker := medialifecycle.Worker{Repository: repo, Staging: stage, Published: oss}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.DB.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	drain := func() {
		t.Helper()
		for i := 0; i < 30; i++ {
			worked, err := worker.RunOnce(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !worked {
				return
			}
		}
		t.Fatal("did not drain")
	}
	exec(`UPDATE products SET status='published',approved_version=version WHERE id=$1`, id)
	drain()
	exec(`UPDATE products SET status='offline',version=version+1,approved_version=NULL WHERE id=$1`, id)
	exec(`UPDATE media_objects SET retain_until=now()-interval '1 second'`)
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := repo.Step(ctx, func(ctx context.Context, o medialifecycle.Object, destination, key string) error {
		if destination != "staging" {
			return errors.New("wrong direction")
		}
		exec(`UPDATE products SET status='published',version=version+1,approved_version=version+1 WHERE id=$1`, id)
		stage.files[key] = append([]byte(nil), oss.files[o.Key]...)
		return nil
	}, func(ctx context.Context, store, key string) error {
		if store == "oss" {
			return oss.Delete(ctx, key)
		}
		return stage.Delete(ctx, key)
	})
	if err != nil {
		t.Fatal(err)
	}
	drain()
	if len(oss.files) != 2 || len(stage.files) != 0 {
		t.Fatal("relist lost OSS or retained stale target")
	}
	var status string
	if err := a.DB.QueryRow(ctx, `SELECT status FROM products WHERE id=$1`, id).Scan(&status); err != nil || status != "published" {
		t.Fatal(status, err)
	}
}

func TestR20RetiredDeleteRetriesAndKeyCannotResurrect(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	p := r16Draft(t, a, u, in)
	id := p["id"].(string)
	stage := &lifecycleMemoryStore{files: map[string][]byte{}}
	lifecycleEnroll(t, a, id, u.ID, "delete-image", stage)
	repo := &postgres.MediaLifecycleStore{DB: a.DB, NewID: newID}
	if _, err := a.DB.Exec(ctx, `UPDATE products SET status='rejected_final',version=version+1 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	remove := func(context.Context, string, string) error { return errors.New("storage temporarily offline") }
	transfer := func(context.Context, medialifecycle.Object, string, string) error {
		return errors.New("unexpected transfer")
	}
	if _, err := repo.Step(ctx, transfer, remove); err != nil {
		t.Fatal(err)
	} // committed tombstone first
	if _, err := repo.Step(ctx, transfer, remove); err == nil {
		t.Fatal("deletion failure hidden")
	}
	var pending int
	if err := a.DB.QueryRow(ctx, `SELECT count(*) FROM media_object_cleanup WHERE completed_at IS NULL`).Scan(&pending); err != nil || pending < 1 {
		t.Fatal(pending, err)
	}
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = postgres.RegisterMediaObjectTx(ctx, tx, "delete-image", "original", "r20/test/objects/delete-image/initial/original", "image/png", strings.Repeat("a", 64), 2048)
	tx.Rollback(ctx)
	if err == nil {
		t.Fatal("retired key resurrected")
	}
	if _, err = a.DB.Exec(ctx, `UPDATE media_object_cleanup SET retry_at=now()`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		worked, err := repo.Step(ctx, transfer, func(ctx context.Context, store, key string) error { return stage.Delete(ctx, key) })
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	if len(stage.files) != 0 {
		t.Fatal("retry did not remove tombstoned objects")
	}
}

func TestR20RetainedStatesParkAndManagedMediaCannotBeShared(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	p := r16Draft(t, a, u, in)
	id := p["id"].(string)
	other := r16Draft(t, a, u, in)["id"].(string)
	stage := &lifecycleMemoryStore{files: map[string][]byte{}}
	lifecycleEnroll(t, a, id, u.ID, "unshared", stage)
	if _, err := a.DB.Exec(ctx, `INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,'unshared',0)`, other); err == nil {
		t.Fatal("R20 media associated across products")
	}
	repo := &postgres.MediaLifecycleStore{DB: a.DB, NewID: newID}
	for _, status := range []string{"sold", "rented", "draft"} {
		if _, err := a.DB.Exec(ctx, `UPDATE products SET status=$2,deleted_at=CASE WHEN $2='draft' THEN now() ELSE NULL END,deleted_by_admin=($2='draft') WHERE id=$1`, id, status); err != nil {
			t.Fatal(err)
		}
		if _, err := a.DB.Exec(ctx, `UPDATE media_objects SET retain_until=now()-interval '1 second',retry_at=now()`); err != nil {
			t.Fatal(err)
		}
		calls := 0
		transfer := func(context.Context, medialifecycle.Object, string, string) error { calls++; return nil }
		remove := func(context.Context, string, string) error { calls++; return nil }
		for i := 0; i < 2; i++ {
			if _, err := repo.Step(ctx, transfer, remove); err != nil {
				t.Fatal(err)
			}
		}
		if worked, err := repo.Step(ctx, transfer, remove); err != nil || worked || calls != 0 {
			t.Fatal("retained state spins or accesses storage", status, worked, calls, err)
		}
	}
}

func TestR20LatePutAfterRetiredDeleteRequeuesExactTarget(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	p := r16Draft(t, a, u, in)
	id := p["id"].(string)
	stage := &lifecycleMemoryStore{files: map[string][]byte{}}
	oss := &lifecycleMemoryStore{files: map[string][]byte{}}
	lifecycleEnroll(t, a, id, u.ID, "late-image", stage)
	repo := &postgres.MediaLifecycleStore{DB: a.DB, NewID: newID}
	worker := medialifecycle.Worker{Repository: repo, Staging: stage, Published: oss}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.DB.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE products SET status='published',approved_version=version WHERE id=$1`, id)
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := repo.Step(ctx, func(ctx context.Context, o medialifecycle.Object, destination, key string) error {
			close(entered)
			<-release
			oss.files[key] = append([]byte(nil), stage.files[o.Key]...)
			return nil
		}, func(ctx context.Context, store, key string) error { return oss.Delete(ctx, key) })
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("copy did not start")
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	exec(`UPDATE media_object_moves SET lease_until=now()-interval '1 second' WHERE NOT completed`)
	exec(`UPDATE products SET status='offline',version=version+1,approved_version=NULL WHERE id=$1`, id)
	for i := 0; i < 4; i++ {
		if _, err := worker.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(oss.files) != 0 || len(oss.deleted) != 1 {
		t.Fatal("first tombstone delete not executed")
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("late finalization blocked")
	}
	if len(oss.files) != 1 {
		t.Fatal("late write was not simulated")
	}
	for i := 0; i < 4; i++ {
		if _, err := worker.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(oss.files) != 0 || len(stage.files) != 2 {
		t.Fatal("late orphan retained or original lost")
	}
}

func TestR20DetachAndRepeatedSubmitDoNotExtendRetention(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	p := r16Draft(t, a, u, in)
	id := p["id"].(string)
	stage := &lifecycleMemoryStore{files: map[string][]byte{}}
	lifecycleEnroll(t, a, id, u.ID, "detach-image", stage)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.DB.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE media_objects SET retain_until=now()+interval '1 hour'`)
	for i := 0; i < 4; i++ {
		exec(`UPDATE products SET status='pending',version=version+1 WHERE id=$1`, id)
		exec(`UPDATE products SET status='draft',version=version+1 WHERE id=$1`, id)
	}
	var preserved bool
	if err := a.DB.QueryRow(ctx, `SELECT bool_and(retain_until<now()+interval '2 hours') FROM media_objects`).Scan(&preserved); err != nil || !preserved {
		t.Fatal("repeated submit renewed retention", err)
	}
	exec(`DELETE FROM product_images WHERE product_id=$1`, id)
	var detached bool
	if err := a.DB.QueryRow(ctx, `SELECT bool_and(product_id IS NULL AND retain_until IS NOT NULL) FROM media_objects`).Scan(&detached); err != nil || !detached {
		t.Fatal("removed image retained parent", err)
	}
	exec(`UPDATE products SET status='published',approved_version=version WHERE id=$1`, id)
	if err := a.DB.QueryRow(ctx, `SELECT bool_and(product_id IS NULL AND retain_until IS NOT NULL) FROM media_objects`).Scan(&detached); err != nil || !detached {
		t.Fatal("parent publishing renewed removed image", err)
	}
}

func TestR20ExpiredLeaseWithoutCallbackIsReconciled(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	id := r16Draft(t, a, u, in)["id"].(string)
	stage := &lifecycleMemoryStore{files: map[string][]byte{}}
	oss := &lifecycleMemoryStore{files: map[string][]byte{}}
	lifecycleEnroll(t, a, id, u.ID, "crash-move", stage)
	repo := &postgres.MediaLifecycleStore{DB: a.DB, NewID: newID}
	worker := medialifecycle.Worker{Repository: repo, Staging: stage, Published: oss}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.DB.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE products SET status='published',approved_version=version WHERE id=$1`, id)
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var target string
	if err := a.DB.QueryRow(ctx, `SELECT destination_key FROM media_object_moves WHERE NOT completed`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE media_object_moves SET lease_token='dead-process',lease_until=now()-interval '1 second',ever_started=true`)
	exec(`UPDATE products SET status='offline',version=version+1,approved_version=NULL WHERE id=$1`, id)
	for i := 0; i < 4; i++ {
		if _, err := worker.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	oss.files[target] = bytes.Repeat([]byte{1}, 2048) // The abandoned external PUT arrives with no callback.
	exec(`UPDATE media_object_cleanup SET retry_at=now() WHERE reconcile`)
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(oss.files) != 0 || len(stage.files) != 2 {
		t.Fatal("abandoned write retained or source lost")
	}
	var deferred bool
	if err := a.DB.QueryRow(ctx, `SELECT completed_at IS NOT NULL AND retry_at>now()+interval '23 hours' FROM media_object_cleanup WHERE object_key=$1`, target).Scan(&deferred); err != nil || !deferred {
		t.Fatal("reconciliation is not scheduled", err)
	}
}

func TestR20MemberCannotBypassFinalRejectionAndCanCancelPublishing(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	id := r16Draft(t, a, u, in)["id"].(string)
	// Reuse the fixture user with a newly issued authenticated public session.
	token := randomToken()
	if _, err := a.DB.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at,audience) VALUES($1,$2,now()+interval '1 hour','public')`, tokenHash(token), u.ID); err != nil {
		t.Fatal(err)
	}
	h := memberHTTP{t: t, handler: a.Routes(), ip: "r20-final-member", origin: "http://qingye.test"}
	cookie := &http.Cookie{Name: sessionCookie, Value: token}
	stage := &lifecycleMemoryStore{files: map[string][]byte{}}
	lifecycleEnroll(t, a, id, u.ID, "final-image", stage)
	if _, err := a.DB.Exec(ctx, `UPDATE products SET status='published',approved_version=version WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	h.call("POST", "/api/v1/me/products/"+id+"/withdraw", member.TransitionInput{ExpectedVersion: 1}, cookie, 200)
	if _, err := a.DB.Exec(ctx, `UPDATE products SET status='rejected_final' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	in.ExpectedVersion = 2
	for _, w := range []*httptest.ResponseRecorder{
		h.call("PATCH", "/api/v1/me/products/"+id, in, cookie, 409),
		h.call("POST", "/api/v1/me/products/"+id+"/images", member.AppendImagesInput{ExpectedVersion: 2, RequestID: newID(), MediaIDs: []string{"final-image"}}, cookie, 409),
		h.call("DELETE", "/api/v1/me/products/"+id+"/images/final-image", member.ChangeImageInput{ExpectedVersion: 2}, cookie, 409),
	} {
		if !strings.Contains(w.Body.String(), "PRODUCT_FINALLY_REJECTED") {
			t.Fatal(w.Body.String())
		}
	}
}

func TestR20DraftDeletionAndExpiryRetireBeforeDetach(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(fmt.Sprint(automatic), func(t *testing.T) {
			a, u, in := r16Fixture(t)
			ctx := context.Background()
			id := r16Draft(t, a, u, in)["id"].(string)
			stage := &lifecycleMemoryStore{files: map[string][]byte{}}
			lifecycleEnroll(t, a, id, u.ID, "expire-draft", stage)
			if automatic {
				if _, err := a.DB.Exec(ctx, `UPDATE products SET draft_expires_at=now()-interval '1 second' WHERE id=$1;`, id); err != nil {
					t.Fatal(err)
				}
				if _, err := a.DB.Exec(ctx, `UPDATE media_files SET ready_at=now()-interval '1 hour' WHERE id='expire-draft'`); err != nil {
					t.Fatal(err)
				}
				if _, err := a.DB.Exec(ctx, `UPDATE media_objects SET retain_until=now()-interval '1 second' WHERE media_id='expire-draft'`); err != nil {
					t.Fatal(err)
				}
				out, err := (&maintenance.Service{DB: a.DB, MediaDir: a.Config.MediaDir}).CleanupDrafts(ctx, 20)
				if err != nil || out.Expired != 1 {
					t.Fatal(out, err)
				}
			} else {
				if _, err := (&postgres.MemberStore{DB: a.DB, NewID: newID}).DeleteDraft(ctx, u, id, 1); err != nil {
					t.Fatal(err)
				}
			}
			var retiring bool
			if err := a.DB.QueryRow(ctx, `SELECT bool_and(force_delete AND product_id IS NULL AND retain_until<=now()) FROM media_objects`).Scan(&retiring); err != nil || !retiring {
				t.Fatal("draft detach renewed image grace", err)
			}
		})
	}
}

func TestR20WholeUploadFourDaysDoesNotUseLegacyThreeDayGC(t *testing.T) {
	a, u, _ := r16Fixture(t)
	ctx := context.Background()
	_, err := a.DB.Exec(ctx, `INSERT INTO media_files(id,owner_id,status,managed_upload,lifecycle_managed,resumable,storage_key,created_at,upload_expires_at) VALUES('whole-four-days',$1,'pending',true,true,false,'uploads/whole-four-days.raw',now()-interval '4 days',now()-interval '1 day')`, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&maintenance.Service{DB: a.DB, MediaDir: a.Config.MediaDir}).CleanupDrafts(ctx, 20); err != nil {
		t.Fatal(err)
	}
	var alive bool
	if err := a.DB.QueryRow(ctx, `SELECT deleted_at IS NULL FROM media_files WHERE id='whole-four-days'`).Scan(&alive); err != nil || !alive {
		t.Fatal("managed whole upload expired at 72 hours", err)
	}
}

func TestR20CrossProductTransitionsRestrictionsAndAppendLockOrder(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	recommendationUser(t, a, "lock-admin", "admin")
	p1 := r16Draft(t, a, u, in)["id"].(string)
	p2 := r16Draft(t, a, u, in)["id"].(string)
	stage := &lifecycleMemoryStore{files: map[string][]byte{}}
	for i, p := range []string{p1, p2} {
		lifecycleEnroll(t, a, p, u.ID, fmt.Sprintf("lock-existing-%d", i), stage)
		lifecycleEnroll(t, a, "", u.ID, fmt.Sprintf("lock-append-%d", i), stage)
	}
	cfg := a.DB.Config()
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "1500ms"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	members := &postgres.MemberStore{DB: pool, NewID: newID}
	restrictions := &postgres.UploadSecurityStore{DB: pool, NewID: newID}
	start := make(chan struct{})
	results := make(chan error, 3)
	for _, product := range []string{p1, p2} {
		go func(id string) {
			<-start
			for i := 0; i < 8; i++ {
				var version int
				var status string
				if err := pool.QueryRow(ctx, `SELECT version,status FROM products WHERE id=$1`, id).Scan(&version, &status); err != nil {
					results <- err
					return
				}
				_, err := members.Transition(ctx, u, id, member.TransitionInput{ExpectedVersion: version}, status != "pending")
				if err != nil {
					f, ok := fault.As(err)
					if !ok || (f.Code != "VERSION_CONFLICT" && f.Code != "INVALID_STATUS" && f.Code != "IMAGE_REQUIRED") {
						results <- err
						return
					}
				}
			}
			results <- nil
		}(product)
	}
	go func() {
		<-start
		for i := 0; i < 8; i++ {
			_, err := restrictions.Restrict(ctx, "lock-admin", u.ID, dsecurity.RestrictInput{ExpectedVersion: int64(i), Blocked: i%2 == 0, PurgeStaging: i == 0, Reason: "并发隔离验证"})
			if err != nil {
				results <- err
				return
			}
			if i%2 == 1 {
				var version int
				if err = pool.QueryRow(ctx, `SELECT version FROM products WHERE id=$1`, p2).Scan(&version); err != nil {
					results <- err
					return
				}
				_, err = members.AppendImages(ctx, u, p2, member.AppendImagesInput{ExpectedVersion: version, RequestID: newID(), MediaIDs: []string{"lock-append-1"}})
				if err != nil {
					f, ok := fault.As(err)
					if !ok || (f.Code != "VERSION_CONFLICT" && f.Code != "MEDIA_NOT_READY") {
						results <- err
						return
					}
				}
			}
		}
		results <- nil
	}()
	close(start)
	for i := 0; i < 3; i++ {
		if err := <-results; err != nil {
			t.Fatal("cross-module locking failed", err)
		}
	}
}
