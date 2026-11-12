package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qingye/backend/internal/domain/media"
	dsecurity "qingye/backend/internal/domain/security"
	"qingye/backend/internal/infrastructure/postgres"
	"qingye/backend/internal/maintenance"
)

func r20SmallPNG(t *testing.T) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 32, 32))
	var state uint32 = 21945
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			im.SetRGBA(x, y, color.RGBA{R: uint8(state), G: uint8(state >> 8), B: uint8(state >> 16), A: 255})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, im); err != nil {
		t.Fatal(err)
	}
	if out.Len() < 1024 || out.Len() > 10240 {
		t.Fatal("fixture outside requested 1–10 KB", out.Len())
	}
	return out.Bytes()
}
func r20ChunkHTTP(f *chatFixture, t *testing.T, actor, url, rangeValue string, data []byte, want int) *httptest.ResponseRecorder {
	t.Helper()
	handler, origin := f.pub, "http://qingye.test"
	if f.cookies[actor].Name == adminSessionCookie {
		handler, origin = f.adm, "http://qingye-admin.test"
	}
	req := httptest.NewRequest(http.MethodPut, url, bytes.NewReader(data))
	req.RemoteAddr = "192.0.2.112:4321"
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Range", rangeValue)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.AddCookie(f.cookies[actor])
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	statusIs(t, w, want)
	return w
}
func TestR20ResumableUploadReplayCancelAndRevocation(t *testing.T) {
	f := newChatFixture(t)
	data := r20SmallPNG(t)
	create := func() map[string]any {
		return memberData(t, f.mediaCall(t, f.member, "POST", "/api/v1/uploads", map[string]any{"filename": "batch.png", "mime_type": "image/png", "byte_size": len(data), "purpose": "product", "resumable": true}, 201))
	}
	ticket := create()
	id := ticket["media_id"].(string)
	url := ticket["upload_url"].(string)
	header := fmt.Sprintf("bytes 0-%d/%d", len(data)-1, len(data))
	f.mediaCall(t, f.other, "GET", "/api/v1/uploads/"+id+"/status", nil, 404)
	f.mediaCall(t, f.first, "GET", "/api/v1/uploads/"+id+"/status", nil, 404)
	r20ChunkHTTP(f, t, f.member, url, header, data, 200)
	r20ChunkHTTP(f, t, f.member, url, header, data, 200)
	altered := append([]byte(nil), data...)
	altered[len(altered)-1] ^= 1
	r20ChunkHTTP(f, t, f.member, url, header, altered, 409)
	status := memberData(t, f.mediaCall(t, f.member, "GET", "/api/v1/uploads/"+id+"/status", nil, 200))
	if int(status["offset"].(float64)) != len(data) || status["status"] != "uploaded" {
		t.Fatal("lost-response resume offset", status)
	}
	r20ChunkHTTP(f, t, f.member, url, header, data, 403) // The old ticket is revoked when status refreshes it.
	f.mediaCall(t, f.member, "POST", "/api/v1/uploads/"+id+"/complete", nil, 200)
	f.mediaCall(t, f.member, "POST", "/api/v1/uploads/"+id+"/complete", nil, 200)
	f.mediaCall(t, f.member, "GET", "/api/v1/media/"+id+"/preview", nil, 200)
	ticket = create()
	id = ticket["media_id"].(string)
	f.mediaCall(t, f.member, "DELETE", "/api/v1/uploads/"+id, nil, 200)
	f.mediaCall(t, f.member, "DELETE", "/api/v1/uploads/"+id, nil, 200)
	r20ChunkHTTP(f, t, f.member, ticket["upload_url"].(string), header, data, 403)
	f.mediaCall(t, f.member, "GET", "/api/v1/uploads/"+id+"/status", nil, 409)
	ticket = create()
	id = ticket["media_id"].(string)
	store := &postgres.UploadSecurityStore{DB: f.a.DB, NewID: newID}
	blocked, err := store.Restrict(context.Background(), f.first, f.member, dsecurity.RestrictInput{Blocked: true, Reason: "isolated test", ExpectedVersion: 0})
	if err != nil {
		t.Fatal(err)
	}
	f.mediaCall(t, f.member, "GET", "/api/v1/uploads/"+id+"/status", nil, 403)
	_, err = store.Restrict(context.Background(), f.first, f.member, dsecurity.RestrictInput{Blocked: false, Reason: "isolated test", ExpectedVersion: blocked.Version})
	if err != nil {
		t.Fatal(err)
	}
	f.mediaCall(t, f.member, "GET", "/api/v1/uploads/"+id+"/status", nil, 403)
	// A new session after unblock starts at the new security version.
	fresh := create()
	freshID := fresh["media_id"].(string)
	statusIs(t, surfaceRequest(t, f.pub, "GET", "/api/v1/uploads/"+freshID+"/status", "", nil, f.cookies[f.member]), 403)
	if _, err = f.a.DB.Exec(context.Background(), `UPDATE media_files SET created_at=now()-interval '6 days' WHERE id=$1`, freshID); err != nil {
		t.Fatal(err)
	}
	f.mediaCall(t, f.member, "GET", "/api/v1/uploads/"+freshID+"/status", nil, 409)
	r20ChunkHTTP(f, t, f.member, fresh["upload_url"].(string), header, data, 403)
}
func TestR20ThreeHundredSmallUploadsDoNotHitOld120Limit(t *testing.T) {
	f := newChatFixture(t)
	data := r20SmallPNG(t)
	for index := 0; index < 300; index++ {
		ticket := memberData(t, f.mediaCall(t, f.member, "POST", "/api/v1/uploads", map[string]any{"filename": fmt.Sprintf("batch-%03d.png", index), "mime_type": "image/png", "byte_size": len(data), "purpose": "product", "resumable": true}, 201))
		r20ChunkHTTP(f, t, f.member, ticket["upload_url"].(string), fmt.Sprintf("bytes 0-%d/%d", len(data)-1, len(data)), data, 200)
		f.mediaCall(t, f.member, "POST", "/api/v1/uploads/"+ticket["media_id"].(string)+"/complete", nil, 200)
	}
	var count int
	if err := f.a.DB.QueryRow(context.Background(), `SELECT count(*) FROM media_files WHERE owner_id=$1 AND status='ready' AND resumable`, f.member).Scan(&count); err != nil || count != 300 {
		t.Fatalf("ready uploads=%d err=%v", count, err)
	}
}

func TestR20ReadyRawCleanupIsDurableAndProtectsProcessing(t *testing.T) {
	f := newChatFixture(t)
	a := f.a
	a.Config.MediaR20.Enabled = true
	ctx := context.Background()
	data := r20SmallPNG(t)
	create := func() string {
		t.Helper()
		ticket := memberData(t, f.mediaCall(t, f.member, "POST", "/api/v1/uploads", map[string]any{"filename": "cleanup.png", "mime_type": "image/png", "byte_size": len(data), "purpose": "product", "resumable": true}, 201))
		id := ticket["media_id"].(string)
		if err := os.MkdirAll(filepath.Join(a.Config.MediaDir, "uploads"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(a.Config.MediaDir, "uploads", id+".raw"), data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := a.DB.Exec(ctx, `UPDATE media_files SET status='uploaded' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	id, active := create(), create()
	store := &postgres.MediaStore{DB: a.DB, ManagedEnabled: true}
	key := "r20/acceptance/objects/" + id + "/cleanup-test/original"
	processed := media.Processed{Key: key, MIME: "image/png", Size: int64(len(data)), Managed: []media.ManagedObject{
		{Variant: "original", Key: key, MIME: "image/png", Size: int64(len(data)), SHA256: strings.Repeat("a", 64)},
		{Variant: "preview", Key: strings.TrimSuffix(key, "original") + "preview", MIME: "image/jpeg", Size: 1024, SHA256: strings.Repeat("b", 64)},
	}}
	// Exercise the actual Ready transaction, then simulate a crash before RemoveRaw.
	if err := store.WithUpload(ctx, id, "public", f.member, func(_ media.Upload, m media.Mutation) error { return m.Ready(ctx, processed) }); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(ctx, `UPDATE media_files SET created_at=now()-interval '6 days',upload_expires_at=now()-interval '1 day',processing_token='active-test',processing_until=now()+interval '5 minutes' WHERE id=$1`, active); err != nil {
		t.Fatal(err)
	}
	var queued int
	if err := a.DB.QueryRow(ctx, `SELECT count(*) FROM media_gc_queue WHERE media_id=$1 AND storage_key=$2 AND completed_at IS NULL`, id, "uploads/"+id+".raw").Scan(&queued); err != nil || queued != 1 {
		t.Fatal("Ready did not persist exact raw cleanup", queued, err)
	}
	maintenanceService := &maintenance.Service{DB: a.DB, MediaDir: a.Config.MediaDir}
	if _, err := maintenanceService.CleanupDrafts(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.Config.MediaDir, "uploads", id+".raw")); !os.IsNotExist(err) {
		t.Fatal("committed raw not removed", err)
	}
	if raw, err := os.ReadFile(filepath.Join(a.Config.MediaDir, "uploads", active+".raw")); err != nil || !bytes.Equal(raw, data) {
		t.Fatal("active upload was cleaned", err)
	}
	var ready, activeAlive bool
	var objects int
	if err := a.DB.QueryRow(ctx, `SELECT status='ready' AND deleted_at IS NULL AND storage_key=$2,(SELECT count(*) FROM media_objects WHERE media_id=$1 AND deleted_at IS NULL) FROM media_files WHERE id=$1`, id, key).Scan(&ready, &objects); err != nil || !ready || objects != 2 {
		t.Fatal("raw cleanup damaged managed objects", err)
	}
	if err := a.DB.QueryRow(ctx, `SELECT deleted_at IS NULL AND processing_until>now() FROM media_files WHERE id=$1`, active).Scan(&activeAlive); err != nil || !activeAlive {
		t.Fatal("active processing was revoked", err)
	}
}
