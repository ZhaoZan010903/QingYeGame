package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	dsecurity "qingye/backend/internal/domain/security"
	"qingye/backend/internal/infrastructure/mediaobject"
	"qingye/backend/internal/infrastructure/postgres"
)

type r20CandidateGate struct {
	*postgres.MediaLifecycleStore
	before, after func()
}

func (r *r20CandidateGate) StageCandidate(ctx context.Context, id, token, variant, key, mime, sha string, size int64) error {
	if r.before != nil {
		r.before()
	}
	err := r.MediaLifecycleStore.StageCandidate(ctx, id, token, variant, key, mime, sha, size)
	if err == nil && r.after != nil {
		r.after()
	}
	return err
}

func TestR20SecurityCandidateGateRechecksRevocationAndLease(t *testing.T) {
	for _, at := range []string{"purge_before_registration", "purge_after_registration", "expiry_after_registration"} {
		t.Run(at, func(t *testing.T) {
			f := newR20SecurityFixture(t)
			ctx := context.Background()
			ticket := f.ticket(t, f.member, "product", "")
			id := ticket["media_id"].(string)
			r20ChunkHTTP(f.chatFixture, t, f.member, ticket["upload_url"].(string), fmt.Sprintf("bytes 0-%d/%d", len(f.image)-1, len(f.image)), f.image, 200)
			change := func() {
				t.Helper()
				if at == "expiry_after_registration" {
					if _, err := f.a.DB.Exec(ctx, `UPDATE media_files SET created_at=now()-interval '6 days' WHERE id=$1`, id); err != nil {
						t.Fatal(err)
					}
					return
				}
				if _, err := (&postgres.UploadSecurityStore{DB: f.a.DB, NewID: newID}).Restrict(ctx, f.first, f.member, dsecurity.RestrictInput{Blocked: true, PurgeStaging: true, Reason: "隔离测试管理员确认停权"}); err != nil {
					t.Fatal(err)
				}
			}
			gate := &r20CandidateGate{MediaLifecycleStore: &postgres.MediaLifecycleStore{DB: f.a.DB, NewID: newID}}
			if at == "purge_before_registration" {
				gate.before = change
			} else {
				gate.after = change
			}
			f.a.MediaFiles.(*mediaobject.Files).Repository = gate
			want := 403
			if at == "purge_before_registration" {
				want = 503 // The storage adapter exposes a safe staging-failure message.
			} else if at == "expiry_after_registration" {
				want = 409
			}
			f.mediaCall(t, f.member, "POST", "/api/v1/uploads/"+id+"/complete", nil, want)
			var ready, processing bool
			var objects, candidates, due int
			if err := f.a.DB.QueryRow(ctx, `SELECT status='ready',processing_token IS NOT NULL,
 (SELECT count(*) FROM media_objects WHERE media_id=$1),
 (SELECT count(*) FROM media_stage_candidates WHERE media_id=$1),
 (SELECT count(*) FROM media_stage_candidates WHERE media_id=$1 AND expires_at<=now()) FROM media_files WHERE id=$1`, id).Scan(&ready, &processing, &objects, &candidates, &due); err != nil || ready || processing || objects != 0 || candidates != due {
				t.Fatal("revoked/expired completion left visible state or a five-day candidate", ready, processing, objects, candidates, due, err)
			}
			if at == "purge_before_registration" && (candidates != 0 || len(f.stage.files) != 0) {
				t.Fatal("purged ticket wrote a new object after revocation")
			}
			if at != "purge_before_registration" && candidates != 1 {
				t.Fatal("in-flight successful write lost its cleanup record", candidates)
			}
			drainRetentionReview(t, f.a, f.stage)
			if len(f.stage.files) != 0 {
				t.Fatal("retired in-flight output waited for the default five-day expiry")
			}
			if at == "expiry_after_registration" {
				f.unblocked(t, f.member)
				// A timeout must not become a persistent ban or affect a fresh ticket.
				f.a.MediaFiles.(*mediaobject.Files).Repository = gate.MediaLifecycleStore
				f.finish(t, f.member, "product", f.ticket(t, f.member, "product", ""), false)
			}
		})
	}
}

func TestR20SecurityCandidateOldClaimCannotBorrowOrReleaseNewLease(t *testing.T) {
	f := newR20SecurityFixture(t)
	ctx := context.Background()
	ticket := f.ticket(t, f.member, "product", "")
	id := ticket["media_id"].(string)
	r20ChunkHTTP(f.chatFixture, t, f.member, ticket["upload_url"].(string), fmt.Sprintf("bytes 0-%d/%d", len(f.image)-1, len(f.image)), f.image, 200)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := f.a.DB.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	repo := &postgres.MediaLifecycleStore{DB: f.a.DB, NewID: newID}
	uploads := &postgres.MediaStore{DB: f.a.DB, ManagedEnabled: true}
	exec(`UPDATE media_files SET processing_token='old-claim',processing_until=now()+interval '5 minutes' WHERE id=$1`, id)
	oldKey := "r20/test/objects/" + id + "/old/original"
	if err := repo.StageCandidate(ctx, id, "old-claim", "original", oldKey, "image/png", strings.Repeat("a", 64), 2048); err != nil {
		t.Fatal(err)
	}
	f.stage.files[oldKey] = bytes.Repeat([]byte{1}, 2048)
	// Simulate a replacement Claim after the old processor timed out.
	exec(`UPDATE media_files SET processing_token='new-claim',processing_until=now()+interval '5 minutes' WHERE id=$1`, id)
	lateKey := "r20/test/objects/" + id + "/late/original"
	if err := repo.StageCandidate(ctx, id, "old-claim", "original", lateKey, "image/png", strings.Repeat("a", 64), 2048); err == nil {
		t.Fatal("old processor borrowed the new processing lease")
	}
	if err := uploads.ReleaseProcessing(ctx, id, "public", f.other, "new-claim"); err == nil {
		t.Fatal("another member released the processing lease")
	}
	if err := uploads.ReleaseProcessing(ctx, id, "admin", f.member, "new-claim"); err == nil {
		t.Fatal("wrong surface released the processing lease")
	}
	if err := uploads.ReleaseProcessing(ctx, id, "public", f.member, "old-claim"); err != nil {
		t.Fatal(err)
	}
	var current string
	if err := f.a.DB.QueryRow(ctx, `SELECT processing_token FROM media_files WHERE id=$1`, id).Scan(&current); err != nil || current != "new-claim" {
		t.Fatal("stale cleanup released the current processor", current, err)
	}
	newKey := "r20/test/objects/" + id + "/new/original"
	if err := repo.StageCandidate(ctx, id, "new-claim", "original", newKey, "image/png", strings.Repeat("a", 64), 2048); err != nil {
		t.Fatal(err)
	}
	f.stage.files[newKey] = bytes.Repeat([]byte{2}, 2048)
	exec(`UPDATE media_files SET processing_until=now()-interval '1 second' WHERE id=$1`, id)
	if err := repo.StageCandidate(ctx, id, "new-claim", "original", lateKey, "image/png", strings.Repeat("a", 64), 2048); err == nil {
		t.Fatal("expired processing lease registered another output")
	}
	// No callback remains: the expired lease itself makes candidates eligible.
	drainRetentionReview(t, f.a, f.stage)
	if len(f.stage.files) != 0 {
		t.Fatal("abandoned processing candidates survived their expired lease")
	}
	f.unblocked(t, f.member)
}
