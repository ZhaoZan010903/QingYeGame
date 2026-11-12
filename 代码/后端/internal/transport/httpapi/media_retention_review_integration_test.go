package httpapi

import (
	"context"
	"fmt"
	"testing"
	"time"

	"qingye/backend/internal/application/medialifecycle"
	"qingye/backend/internal/domain/management"
	"qingye/backend/internal/domain/member"
	"qingye/backend/internal/infrastructure/postgres"
	"qingye/backend/internal/maintenance"
)

// Advance only the isolated fixture's deadlines; business retention remains five days.
func TestR20RejectedDraftKeepsImageGraceUntilItsActualDeadline(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	id := r16Draft(t, a, u, in)["id"].(string)
	stage := &lifecycleMemoryStore{files: map[string][]byte{}}
	lifecycleEnroll(t, a, id, u.ID, "review-grace", stage)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.DB.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// D4: the original D0 draft and image deadlines have one day remaining.
	exec(`UPDATE products SET created_at=now()-interval '4 days',draft_expires_at=now()+interval '1 day' WHERE id=$1`, id)
	exec(`UPDATE media_files SET created_at=now()-interval '4 days',ready_at=now()-interval '4 days' WHERE id='review-grace'`)
	exec(`UPDATE media_objects SET retain_until=now()+interval '1 day' WHERE media_id='review-grace'`)
	if _, err := a.memberService().Transition(ctx, u, id, member.TransitionInput{ExpectedVersion: 1}, true); err != nil {
		t.Fatal(err)
	}
	recommendationUser(t, a, "grace-admin", "admin")
	admin := &postgres.ManagementStore{DB: a.DB, NewID: newID}
	if _, err := admin.TransitionProduct(ctx, management.Actor{ID: "grace-admin", Role: "admin"}, id, "review", management.TransitionInput{ExpectedVersion: 2, Decision: "reject", Reason: "补充说明后重新提交"}); err != nil {
		t.Fatal(err)
	}
	var aligned bool
	var deadline time.Time
	if err := a.DB.QueryRow(ctx, `SELECT p.draft_expires_at,p.draft_expires_at=(SELECT max(retain_until) FROM media_objects WHERE product_id=p.id) AND p.draft_expires_at>now()+interval '4 days 23 hours' FROM products p WHERE p.id=$1`, id).Scan(&deadline, &aligned); err != nil || !aligned {
		t.Fatal("rejection did not align the visible draft deadline with the new five-day image grace", aligned, err)
	}
	in.ExpectedVersion = 3
	in.Description = "补充后的完整介绍"
	if _, err := a.memberService().Edit(ctx, u, id, in); err != nil {
		t.Fatal(err)
	}
	var afterEdit time.Time
	if err := a.DB.QueryRow(ctx, `SELECT draft_expires_at FROM products WHERE id=$1 AND status='draft'`, id).Scan(&afterEdit); err != nil || !afterEdit.Equal(deadline) {
		t.Fatal("editing after rejection changed the granted deadline", afterEdit, deadline, err)
	}
	// D5 plus a stale metadata deadline: the worker must still honor the image's D9 deadline.
	exec(`UPDATE products SET draft_expires_at=now()-interval '1 second' WHERE id=$1`, id)
	exec(`UPDATE media_objects SET retain_until=now()+interval '4 days' WHERE media_id='review-grace'`)
	canExpire := func(want bool) {
		t.Helper()
		tx, err := a.DB.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, `SELECT id FROM products WHERE id=$1 FOR UPDATE`, id); err != nil {
			t.Fatal(err)
		}
		got, err := postgres.DraftCanExpire(ctx, tx, id)
		if err != nil || got != want {
			t.Fatal("locked expiry recheck", got, want, err)
		}
	}
	canExpire(false)
	cleaner := &maintenance.Service{DB: a.DB, MediaDir: a.Config.MediaDir}
	if out, err := cleaner.CleanupDrafts(ctx, 20); err != nil || out.Expired != 0 {
		t.Fatal("D5 prematurely expired D9 revision", out, err)
	}
	var intact bool
	if err := a.DB.QueryRow(ctx, `SELECT p.deleted_at IS NULL AND p.description=$2 AND NOT EXISTS(SELECT 1 FROM media_objects o WHERE o.product_id=p.id AND (o.force_delete OR o.deleted_at IS NOT NULL)) FROM products p WHERE p.id=$1`, id, in.Description).Scan(&intact); err != nil || !intact {
		t.Fatal("revision content was lost before its deadline", err)
	}
	// D9: both deadlines have elapsed, so automatic cleanup is permitted again.
	exec(`UPDATE media_objects SET retain_until=now()-interval '1 second' WHERE media_id='review-grace'`)
	canExpire(true)
	if out, err := cleaner.CleanupDrafts(ctx, 20); err != nil || out.Expired != 1 {
		t.Fatal("D9 revision did not expire", out, err)
	}
	drainRetentionReview(t, a, stage)
	if len(stage.files) != 0 {
		t.Fatal("expired revision left staging images")
	}
}

func TestR20DraftTextEditsDoNotRenewRetention(t *testing.T) {
	for _, withImages := range []bool{false, true} {
		t.Run(fmt.Sprint(withImages), func(t *testing.T) {
			a, u, in := r16Fixture(t)
			ctx := context.Background()
			id := r16Draft(t, a, u, in)["id"].(string)
			stage := &lifecycleMemoryStore{files: map[string][]byte{}}
			if withImages {
				lifecycleEnroll(t, a, id, u.ID, "text-only-edit", stage)
				if _, err := a.DB.Exec(ctx, `UPDATE media_objects SET retain_until=now()-interval '1 second' WHERE media_id='text-only-edit'`); err != nil {
					t.Fatal(err)
				}
			}
			var before time.Time
			if err := a.DB.QueryRow(ctx, `UPDATE products SET draft_expires_at=now()-interval '1 second' WHERE id=$1 RETURNING draft_expires_at`, id).Scan(&before); err != nil {
				t.Fatal(err)
			}
			for version := 1; version <= 4; version++ {
				in.ExpectedVersion = version
				in.Description = fmt.Sprintf("第%d次整理说明", version)
				if _, err := a.memberService().Edit(ctx, u, id, in); err != nil {
					t.Fatal(err)
				}
			}
			var unchanged bool
			if err := a.DB.QueryRow(ctx, `SELECT draft_expires_at=$2 FROM products WHERE id=$1`, id, before).Scan(&unchanged); err != nil || !unchanged {
				t.Fatal("text edits renewed an expired draft", err)
			}
			if out, err := (&maintenance.Service{DB: a.DB, MediaDir: a.Config.MediaDir}).CleanupDrafts(ctx, 20); err != nil || out.Expired != 1 {
				t.Fatal("text edits prevented due cleanup", out, err)
			}
			drainRetentionReview(t, a, stage)
			if len(stage.files) != 0 {
				t.Fatal("text edits retained expired images")
			}
		})
	}
}

func TestR20ExplicitDraftDeletionDoesNotWaitForImageGrace(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	id := r16Draft(t, a, u, in)["id"].(string)
	stage := &lifecycleMemoryStore{files: map[string][]byte{}}
	lifecycleEnroll(t, a, id, u.ID, "explicit-delete", stage)
	if _, err := a.memberService().DeleteDraft(ctx, u, id, 1); err != nil {
		t.Fatal(err)
	}
	var forced bool
	if err := a.DB.QueryRow(ctx, `SELECT bool_and(force_delete AND retain_until<=now()) FROM media_objects WHERE media_id='explicit-delete'`).Scan(&forced); err != nil || !forced {
		t.Fatal("explicit deletion was delayed by image grace", err)
	}
	drainRetentionReview(t, a, stage)
	if len(stage.files) != 0 {
		t.Fatal("explicit deletion left retained staging images")
	}
}

func drainRetentionReview(t *testing.T, a *App, stage *lifecycleMemoryStore) {
	t.Helper()
	w := medialifecycle.Worker{Repository: &postgres.MediaLifecycleStore{DB: a.DB, NewID: newID}, Staging: stage, Published: &lifecycleMemoryStore{files: map[string][]byte{}}}
	for range 20 {
		worked, err := w.RunOnce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			return
		}
	}
	t.Fatal("retention review worker did not drain")
}
