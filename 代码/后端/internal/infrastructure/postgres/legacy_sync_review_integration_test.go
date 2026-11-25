package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"qingye/backend/internal/application/legacysync"
	"qingye/backend/internal/infrastructure/mediafiles"
	"qingye/backend/internal/infrastructure/postgres"
	"qingye/backend/internal/infrastructure/security"
	"qingye/backend/internal/transport/httpapi"
)

func TestLegacySyncReviewDecisions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db := legacySyncDB(t, ctx)
	store := postgres.LegacySyncStore{DB: db}
	const actor = "review-operator"
	if _, err := db.Exec(ctx, `INSERT INTO users(id,phone,nickname,password_hash,role)
 VALUES('review-operator','21002','管理员','fixture','admin'),('review-member','21003','会员','fixture','member');
 INSERT INTO categories(id,name,trade_type,price_units) VALUES('legacy-sale','QQ炫舞买号','sale','["account"]')`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	source := &testLegacySource{images: map[string][]byte{}}
	var records []legacysync.Record
	for i := range 5 {
		picture := fmt.Sprintf("https://cdn.xuansiwei.com/test%d.png", i)
		records = append(records, fakeListing(fmt.Sprint(i), fmt.Sprintf("QYCH%d", i+1), "商品", "QQ炫舞买号", "200", picture, now))
		source.images[picture] = legacyImage(t, byte(i))
	}
	service := &legacysync.Service{Source: source, Store: store, Objects: testLegacyObjects{}, Environment: "test",
		NewID: security.NewID, Images: mediafiles.LegacyImages{Directory: t.TempDir()}}
	const origin = "http://127.0.0.1:5181"
	token := security.RandomToken()
	if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at,audience)
 VALUES($1,$2,now()+interval '1 hour','admin')`, security.TokenHash(token), actor); err != nil {
		t.Fatal(err)
	}
	router := func(runner *legacysync.ManualRunner) http.Handler {
		return (&httpapi.App{DB: db, LegacySync: runner, Config: httpapi.Config{Surface: "admin", AllowedOrigins: []string{origin}}}).Routes()
	}
	request := func(api http.Handler, id, action, body string, expected int) legacysync.Batch {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/admin/legacy-sync/batches/"+id+"/"+action, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.AddCookie(&http.Cookie{Name: "qingye_admin_session", Value: token})
		response := httptest.NewRecorder()
		api.ServeHTTP(response, req)
		var payload struct{ Data legacysync.Batch }
		if response.Code != expected || json.Unmarshal(response.Body.Bytes(), &payload) != nil {
			t.Fatalf("%s: %d %s", action, response.Code, response.Body.String())
		}
		return payload.Data
	}
	run := func(parent, status string) legacysync.Batch {
		t.Helper()
		runner, id := legacysync.NewManualRunner(ctx, service), security.NewID()
		var err error
		if parent == "" {
			_, err = runner.Start(actor, id)
		} else {
			job := request(router(runner), parent, "continue", fmt.Sprintf(`{"confirm":true,"request_id":%q}`, id), http.StatusAccepted)
			if job.ID != id || job.ReviewOf != parent {
				t.Fatal("unexpected linked job", job)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		runner.Wait()
		return waitLegacyBatch(t, ctx, store, id, status)
	}
	assertPublished := func(n int) {
		t.Helper()
		var count int
		if err := db.QueryRow(ctx, "SELECT count(*) FROM products WHERE status='published'").Scan(&count); err != nil || count != n {
			t.Fatalf("published count=%d, expected %d, error=%v", count, n, err)
		}
	}
	source.inventory.Records = records
	run("", "applied")
	source.inventory.Records = records[:1]
	first := run("", "blocked")
	if !first.CanContinue || !first.CanCancel || first.ReviewKind != "bulk_offline" || first.Summary.Review != 4 {
		t.Fatalf("protected batch actions: %+v", first)
	}
	if _, _, err := store.BeginReviewedBatch(ctx, security.NewID(), "review-member", first.ID); err == nil {
		t.Fatal("member could approve")
	}
	if _, err := store.CancelBlocked(ctx, first.ID, "review-member"); err == nil {
		t.Fatal("member could cancel")
	}
	api := router(nil)
	request(api, first.ID, "cancel", `{}`, http.StatusUnprocessableEntity)
	request(api, first.ID, "continue", `{"confirm":false}`, http.StatusUnprocessableEntity)
	for range 2 {
		cancelled := request(api, first.ID, "cancel", `{"confirm":true}`, http.StatusOK)
		if cancelled.Status != "cancelled" || cancelled.CanContinue || cancelled.CanCancel {
			t.Fatalf("cancel/replay: %+v", cancelled)
		}
	}
	var logs int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM admin_logs WHERE action='legacy.sync.cancel' AND resource_id=$1", first.ID).Scan(&logs); err != nil || logs != 1 {
		t.Fatalf("cancel audit entries=%d, err=%v", logs, err)
	}
	if _, _, err := store.BeginReviewedBatch(ctx, security.NewID(), actor, first.ID); err == nil {
		t.Fatal("cancelled batch could be approved")
	}
	assertPublished(5)
	// Authorization applies only to the bulk-offline threshold. A malformed
	// retained listing must still abort the entire transaction.
	badListing := records[0]
	badListing.Title = ""
	source.inventory.Records = []legacysync.Record{badListing}
	protectedBad := run("", "blocked")
	bad := run(protectedBad.ID, "blocked")
	if bad.CanContinue || !bad.CanCancel || bad.ReviewKind != "" {
		t.Fatalf("validation failure offered an override: %+v", bad)
	}
	if _, _, err := store.BeginReviewedBatch(ctx, security.NewID(), actor, bad.ID); err == nil {
		t.Fatal("invalid listing could be approved")
	}
	assertPublished(5)
	source.inventory.Records = records[:1]
	older := run("", "blocked")
	review := run("", "blocked")
	if _, _, err := store.BeginReviewedBatch(ctx, security.NewID(), actor, older.ID); err == nil {
		t.Fatal("a newer batch did not invalidate older approval")
	}
	// Same count but a different missing product. The new run must stop and
	// present the changed list even though the operator approved four removals.
	source.inventory.Records = records[1:2]
	changed := run(review.ID, "blocked")
	if changed.ReviewOf != review.ID || !changed.CanContinue || !strings.Contains(changed.Reason, "原确认失效") {
		t.Fatalf("changed directory bypassed protection: %+v", changed)
	}
	assertPublished(5)
	// A local edit on a candidate also invalidates the confirmation.
	if _, err := db.Exec(ctx, "UPDATE products SET updated_at=clock_timestamp() WHERE legacy_id=$1", records[0].ID); err != nil {
		t.Fatal(err)
	}
	refreshed := run(changed.ID, "blocked")
	if !refreshed.CanContinue || !strings.Contains(refreshed.Reason, "原确认失效") {
		t.Fatalf("local edit did not require another decision: %+v", refreshed)
	}
	assertPublished(5)
	reads := source.reads
	applied := run(refreshed.ID, "applied")
	if applied.ReviewOf != refreshed.ID || applied.Summary.Removed != 4 || applied.RollbackUntil == nil || source.reads != reads+2 {
		t.Fatalf("approved batch: %+v, reads=%d", applied, source.reads-reads)
	}
	assertPublished(1)
	if _, started, err := store.BeginReviewedBatch(ctx, applied.ID, actor, refreshed.ID); err != nil || started {
		t.Fatalf("duplicate approval not idempotent: %v, started=%t", err, started)
	}
	if _, _, err := store.BeginReviewedBatch(ctx, security.NewID(), actor, refreshed.ID); err == nil {
		t.Fatal("approval could be reused")
	}
	if err := store.RollbackBatch(ctx, applied.ID, actor); err != nil {
		t.Fatal(err)
	}
	assertPublished(5)
	// Subsequent ordinary and scheduled attempts never inherit an approval.
	latest := run("", "blocked")
	if _, err := db.Exec(ctx, "UPDATE legacy_sync_control SET mode='automatic'"); err != nil {
		t.Fatal(err)
	}
	scheduled, err := service.RunScheduled(ctx, now)
	if err == nil || scheduled.Status != "blocked" || !scheduled.CanContinue || scheduled.ReviewOf != "" {
		t.Fatalf("fresh scheduled batch: %+v %v", scheduled, err)
	}
	assertPublished(5)
	if _, err := store.CancelBlocked(ctx, latest.ID, actor); err != nil {
		t.Fatal(err)
	}
	// Cancellation and approval racing for the same pending decision cannot
	// both win. Check the linked job and parent in the same isolated database.
	var wg sync.WaitGroup
	wg.Add(2)
	var cancelErr, approveErr error
	go func() { defer wg.Done(); _, cancelErr = store.CancelBlocked(ctx, scheduled.ID, actor) }()
	go func() {
		defer wg.Done()
		_, _, approveErr = store.BeginReviewedBatch(ctx, security.NewID(), actor, scheduled.ID)
	}()
	wg.Wait()
	if (cancelErr == nil) == (approveErr == nil) {
		t.Fatalf("cancel/approve race = %v/%v", cancelErr, approveErr)
	}
	assertPublished(5)
}
