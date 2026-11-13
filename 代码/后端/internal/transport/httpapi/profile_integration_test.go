package httpapi

import (
	"context"
	"qingye/backend/internal/infrastructure/postgres"
	"qingye/backend/internal/maintenance"
	"strings"
	"sync"
	"testing"
)

func nicknameRequestID(t *testing.T, f *chatFixture, user string) string {
	t.Helper()
	data := memberData(t, f.call(t, user, "GET", "/api/v1/me/profile", nil, 200))
	return data["nickname_request"].(map[string]any)["id"].(string)
}
func TestR211NicknameReviewCooldownAndRejection(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	data := memberData(t, f.call(t, f.member, "PATCH", "/api/v1/me", map[string]any{"nickname": "新会员昵称"}, 200))
	if data["nickname"] != "聊天验收" {
		t.Fatal("unapproved nickname became visible")
	}
	id := nicknameRequestID(t, f, f.member)
	f.call(t, f.member, "PATCH", "/api/v1/me", map[string]any{"nickname": "重复申请"}, 409)
	f.call(t, f.member, "POST", "/api/v1/admin/nickname-requests/"+id+"/review", map[string]any{"decision": "approved"}, 401)
	f.call(t, f.first, "GET", "/api/v1/admin/nickname-requests?status=unknown", nil, 422)
	list := f.call(t, f.first, "GET", "/api/v1/admin/nickname-requests?status=pending&page=1&limit=20", nil, 200)
	if !strings.Contains(list.Body.String(), "新会员昵称") {
		t.Fatal("review queue did not include pending nickname")
	}
	f.call(t, f.first, "POST", "/api/v1/admin/nickname-requests/"+id+"/review", map[string]any{"decision": "rejected", "note": "请调整昵称"}, 200)
	f.call(t, f.member, "PATCH", "/api/v1/me", map[string]any{"nickname": "通过的昵称"}, 200)
	id = nicknameRequestID(t, f, f.member)
	f.call(t, f.first, "POST", "/api/v1/admin/nickname-requests/"+id+"/review", map[string]any{"decision": "approved"}, 200)
	f.call(t, f.second, "POST", "/api/v1/admin/nickname-requests/"+id+"/review", map[string]any{"decision": "rejected"}, 409)
	data = memberData(t, f.call(t, f.member, "GET", "/api/v1/me/profile", nil, 200))
	if data["nickname"] != "通过的昵称" || data["can_request_nickname"] != false || data["next_nickname_change_at"] == nil {
		t.Fatal("approved nickname/cooldown not exposed", data)
	}
	f.call(t, f.member, "PATCH", "/api/v1/me", map[string]any{"nickname": "冷却期申请"}, 409)
	if _, err := f.a.DB.Exec(ctx, `UPDATE users SET nickname_changed_at=now()-interval '30 days'+interval '1 minute' WHERE id=$1`, f.member); err != nil {
		t.Fatal(err)
	}
	f.call(t, f.member, "PATCH", "/api/v1/me", map[string]any{"nickname": "尚差一分钟"}, 409)
	if _, err := f.a.DB.Exec(ctx, `UPDATE users SET nickname_changed_at=now()-interval '30 days' WHERE id=$1`, f.member); err != nil {
		t.Fatal(err)
	}
	f.call(t, f.member, "PATCH", "/api/v1/me", map[string]any{"nickname": "满三十天"}, 200)
}

func TestR211NicknameConcurrentRequestAndReview(t *testing.T) {
	f := newChatFixture(t)
	var wg sync.WaitGroup
	outcomes := make(chan int, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := surfaceRequest(t, f.pub, "PATCH", "/api/v1/me", "http://qingye.test", map[string]any{"nickname": "并发申请"}, f.cookies[f.member])
			outcomes <- w.Code
		}()
	}
	wg.Wait()
	close(outcomes)
	successful := 0
	for status := range outcomes {
		if status == 200 {
			successful++
		} else if status != 409 {
			t.Fatalf("request race returned %d", status)
		}
	}
	if successful != 1 {
		t.Fatalf("accepted %d parallel nickname requests", successful)
	}
	id := nicknameRequestID(t, f, f.member)
	outcomes = make(chan int, 2)
	for _, actor := range []string{f.first, f.second} {
		wg.Add(1)
		go func(actor string) {
			defer wg.Done()
			w := surfaceRequest(t, f.adm, "POST", "/api/v1/admin/nickname-requests/"+id+"/review", "http://qingye-admin.test", map[string]any{"decision": "approved"}, f.cookies[actor])
			outcomes <- w.Code
		}(actor)
	}
	wg.Wait()
	close(outcomes)
	successful = 0
	for status := range outcomes {
		if status == 200 {
			successful++
		} else if status != 409 {
			t.Fatalf("review race returned %d", status)
		}
	}
	if successful != 1 {
		t.Fatal("parallel reviews were not atomic")
	}
	var count int
	if err := f.a.DB.QueryRow(context.Background(), `SELECT count(*) FROM admin_logs WHERE action='nickname.review' AND resource_id=$1`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal("review audit was duplicated", count, err)
	}
}

func TestR211AvatarLibraryPermissionsVersionsAndGC(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	f.mediaCall(t, f.member, "POST", "/api/v1/uploads", map[string]any{"filename": "avatar.png", "mime_type": "image/png", "byte_size": 50, "purpose": "avatar"}, 403)
	mid := f.image(t, f.first, "avatar", "")
	f.call(t, f.member, "PATCH", "/api/v1/chat/profile", map[string]any{"avatar_id": mid}, 422)
	f.call(t, f.second, "POST", "/api/v1/admin/avatar-library", map[string]any{"name": "他人上传", "media_id": mid, "sort_order": 0, "enabled": true}, 422)
	item := memberData(t, f.call(t, f.first, "POST", "/api/v1/admin/avatar-library", map[string]any{"name": "卡通头像", "media_id": mid, "sort_order": 5, "enabled": true}, 200))
	id := item["id"].(string)
	f.call(t, f.first, "POST", "/api/v1/admin/avatar-library", map[string]any{"name": "重复图", "media_id": mid, "sort_order": 1, "enabled": true}, 409)
	f.call(t, f.member, "GET", "/api/v1/avatar-library", nil, 200)
	statusIs(t, surfaceRequest(t, f.pub, "GET", "/api/v1/avatar-library", "", nil), 401)
	f.mediaCall(t, f.member, "GET", "/api/v1/media/"+mid+"/file", nil, 200)
	statusIs(t, surfaceRequest(t, f.pub, "GET", "/api/v1/media/"+mid+"/file", "", nil), 404)
	// 未选用、已停用的库图片也必须保留，管理员之后可重新启用。
	if _, err := f.a.DB.Exec(ctx, `UPDATE media_files SET created_at=now()-interval '10 days',ready_at=now()-interval '10 days',upload_expires_at=now()-interval '1 day' WHERE id=$1`, mid); err != nil {
		t.Fatal(err)
	}
	tx, err := f.a.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = postgres.QueueManagedMediaGC(ctx, tx, mid); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	f.call(t, f.member, "PATCH", "/api/v1/chat/profile", map[string]any{"avatar_id": mid}, 200)
	update := map[string]any{"name": "已停用头像", "sort_order": 0, "enabled": false, "version": item["version"]}
	f.call(t, f.first, "PATCH", "/api/v1/admin/avatar-library/"+id, update, 200)
	f.call(t, f.second, "PATCH", "/api/v1/admin/avatar-library/"+id, update, 409)
	f.call(t, f.other, "PATCH", "/api/v1/chat/profile", map[string]any{"avatar_id": mid}, 422)
	f.mediaCall(t, f.other, "GET", "/api/v1/media/"+mid+"/file", nil, 200) // current member avatar remains readable
	f.call(t, f.member, "PATCH", "/api/v1/chat/profile", map[string]any{"avatar_id": ""}, 200)
	f.mediaCall(t, f.other, "GET", "/api/v1/media/"+mid+"/file", nil, 404)
	f.mediaCall(t, f.second, "GET", "/api/v1/media/"+mid+"/file", nil, 200)
	f.mediaCall(t, f.first, "DELETE", "/api/v1/uploads/"+mid, nil, 422)
	if _, err = (&maintenance.Service{DB: f.a.DB, MediaDir: f.a.Config.MediaDir}).CleanupDrafts(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var deleted bool
	if err = f.a.DB.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM media_files WHERE id=$1`, mid).Scan(&deleted); err != nil || deleted {
		t.Fatal("library avatar was garbage-collected", err)
	}
}
