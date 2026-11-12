package httpapi

import (
	"context"
	"fmt"
	"net/http/httptest"
	"qingye/backend/internal/infrastructure/postgres"
	"strings"
	"sync"
	"testing"
	"time"
)

func (f *chatFixture) mediaCall(t *testing.T, actor, method, path string, body any, want int) *httptest.ResponseRecorder {
	t.Helper()
	handler, origin := f.pub, "http://qingye.test"
	if f.cookies[actor] != nil && f.cookies[actor].Name == adminSessionCookie {
		handler, origin = f.adm, "http://qingye-admin.test"
	}
	w := surfaceRequest(t, handler, method, path, origin, body, f.cookies[actor])
	statusIs(t, w, want)
	return w
}
func (f *chatFixture) image(t *testing.T, actor, purpose, conversation string) string {
	t.Helper()
	data := testPNG(t)
	ticket := memberData(t, f.mediaCall(t, actor, "POST", "/api/v1/uploads", map[string]any{"filename": "chat.png", "mime_type": "image/png", "byte_size": len(data), "purpose": purpose, "conversation_id": conversation}, 201))
	f.mediaCall(t, actor, "PUT", ticket["upload_url"].(string), data, 200)
	mid := ticket["media_id"].(string)
	f.mediaCall(t, actor, "POST", "/api/v1/uploads/"+mid+"/complete", nil, 200)
	return mid
}
func TestChatImagesPersistencePermissionsAndAvatar(t *testing.T) {
	f := newChatFixture(t)
	f.online(t, f.first, true)
	id := f.start(t, f.member, f.product)
	memberPath := "/api/v1/chat/conversations/" + id + "/messages"
	adminPath := "/api/v1/admin/chat/conversations/" + id + "/messages"
	one, two := f.image(t, f.member, "chat", id), f.image(t, f.member, "chat", id)
	file := "/api/v1/media/" + one + "/file"
	f.mediaCall(t, f.member, "GET", file, nil, 200)
	f.mediaCall(t, f.first, "GET", file, nil, 404) // A selected but unsent image stays private to its author.
	input := map[string]any{"body": "账号配置说明", "client_id": "image-intent", "image_ids": []string{two, one}}
	sent := memberData(t, f.call(t, f.member, "POST", memberPath, input, 200))
	replay := memberData(t, f.call(t, f.member, "POST", memberPath, input, 200))
	if sent["id"] != replay["id"] {
		t.Fatal("image retry duplicated the message")
	}
	f.call(t, f.member, "POST", memberPath, map[string]any{"body": "账号配置说明", "client_id": "image-intent", "image_ids": []string{one, two}}, 409)
	f.call(t, f.member, "POST", memberPath, map[string]any{"body": "reuse", "client_id": "another", "image_ids": []string{one}}, 422)
	f.mediaCall(t, f.first, "GET", file, nil, 200)
	for _, actor := range []string{f.other, f.second} {
		f.mediaCall(t, actor, "GET", file, nil, 404)
		f.mediaCall(t, actor, "GET", file+"?variant=thumb", nil, 404)
	}
	statusIs(t, surfaceRequest(t, f.pub, "GET", file, "", nil), 404)
	w := f.call(t, f.first, "GET", adminPath, nil, 200)
	if !strings.Contains(w.Body.String(), `"kind":"image"`) || !strings.Contains(w.Body.String(), "账号配置说明") || strings.Index(w.Body.String(), two) > strings.Index(w.Body.String(), one) {
		t.Fatal("image order/body not persisted")
	}
	reply := f.image(t, f.first, "chat", id)
	f.call(t, f.first, "POST", "/api/v1/admin/products/"+f.product+"/images", map[string]any{"expected_version": 1, "request_id": newID(), "media_ids": []string{reply}}, 422)
	f.call(t, f.first, "POST", adminPath, map[string]any{"body": "", "client_id": "image-only", "image_ids": []string{reply}}, 200)
	f.mediaCall(t, f.member, "GET", "/api/v1/media/"+reply+"/file?variant=thumb", nil, 200)
	avatar := f.image(t, f.member, "avatar", "")
	statusIs(t, surfaceRequest(t, f.pub, "PATCH", "/api/v1/chat/profile", "http://foreign.test", map[string]any{"avatar_id": avatar}, f.cookies[f.member]), 403)
	f.call(t, f.member, "PATCH", "/api/v1/chat/profile", map[string]any{"avatar_id": one}, 422)
	f.call(t, f.other, "PATCH", "/api/v1/chat/profile", map[string]any{"avatar_id": avatar}, 422)
	f.call(t, f.member, "PATCH", "/api/v1/chat/profile", map[string]any{"avatar_id": avatar}, 200)
	profile := f.call(t, f.member, "GET", "/api/v1/chat/profile", nil, 200)
	if !strings.Contains(profile.Body.String(), avatar) {
		t.Fatal("avatar was not saved")
	}
	if !strings.Contains(f.call(t, f.first, "GET", adminPath, nil, 200).Body.String(), avatar) {
		t.Fatal("sender avatar missing")
	}
	f.mediaCall(t, f.first, "GET", "/api/v1/media/"+avatar+"/file", nil, 200)
	f.call(t, f.member, "POST", memberPath, map[string]any{"client_id": "avatar-as-message", "image_ids": []string{avatar}}, 422)
	// GC must preserve durable attachments and the currently bound avatar.
	for _, mid := range []string{one, two, reply, avatar} {
		tx, err := f.a.DB.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err = postgres.QueueManagedMediaGC(context.Background(), tx, mid); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	var deleted int
	if err := f.a.DB.QueryRow(context.Background(), `SELECT count(*) FROM media_files WHERE id=ANY($1::text[]) AND deleted_at IS NOT NULL`, []string{one, two, reply, avatar}).Scan(&deleted); err != nil || deleted != 0 {
		t.Fatal("referenced media queued", err)
	}
	// Reassignment revokes the former agent, including thumbnails and its own sent images.
	if _, err := f.a.DB.Exec(context.Background(), `UPDATE chat_conversations SET admin_id=$2 WHERE id=$1`, id, f.second); err != nil {
		t.Fatal(err)
	}
	f.mediaCall(t, f.first, "GET", file, nil, 404)
	f.mediaCall(t, f.first, "GET", "/api/v1/media/"+reply+"/file", nil, 404)
	f.mediaCall(t, f.second, "GET", file, nil, 200)
	f.mediaCall(t, f.first, "POST", "/api/v1/uploads", map[string]any{"filename": "x.png", "mime_type": "image/png", "byte_size": 50, "purpose": "chat", "conversation_id": id}, 404)
	f.call(t, f.member, "PATCH", "/api/v1/chat/profile", map[string]any{"avatar_id": ""}, 200)
	f.mediaCall(t, f.second, "GET", "/api/v1/media/"+avatar+"/file", nil, 404)
}

func TestChatImagesTransportAndConcurrentRetry(t *testing.T) {
	f := newChatFixture(t)
	id := f.start(t, f.member, f.product)
	mid := f.image(t, f.member, "chat", id)
	socket := newChatTestSocket(t, f, f.a)
	conn := socket.dial(t, socket.ticket(t, f.member), f.member, "http://qingye.test", 101)
	payload := map[string]any{"body": "图文通过兼容连接", "client_id": "cross-transport", "image_ids": []string{mid}}
	socketCommand(t, conn, "send", id, "ws", payload, 200)
	wt := newChatTestTransport(t, f.a)
	stream := chatTestStream(t, wt.dial(t, wt.ticket(t, f, f.member), wt.origin, 200))
	chatTestCommand(t, stream, "send", id, "wt-retry", payload, 200)
	another := f.image(t, f.member, "chat", id)
	chatTestCommand(t, stream, "send", id, "wt-new", map[string]any{"body": "图文通过WebTransport", "client_id": "wt-new", "image_ids": []string{another}}, 200)
	start := time.Now()
	var wg sync.WaitGroup
	failures := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := surfaceRequest(t, f.pub, "POST", "/api/v1/chat/conversations/"+id+"/messages", "http://qingye.test", payload, f.cookies[f.member])
			if w.Code != 200 {
				failures <- fmt.Sprint(w.Code)
			}
		}()
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	var n int
	err := f.a.DB.QueryRow(context.Background(), `SELECT count(*) FROM chat_messages WHERE conversation_id=$1 AND client_id='cross-transport'`, id).Scan(&n)
	if err != nil || n != 1 {
		t.Fatal("concurrent retry duplicated message", n, err)
	}
	t.Logf("8 concurrent image retries completed in %s, one durable message", time.Since(start))
}

func TestChatImagesRejectWrongScopeAndConversation(t *testing.T) {
	f := newChatFixture(t)
	id := f.start(t, f.member, f.product)
	otherID := f.start(t, f.other, f.product)
	other := f.image(t, f.other, "chat", otherID)
	product := f.image(t, f.member, "product", "")
	for _, mid := range []string{other, product, "missing"} {
		f.call(t, f.member, "POST", "/api/v1/chat/conversations/"+id+"/messages", map[string]any{"body": "wrong scope", "client_id": newID(), "image_ids": []string{mid}}, 422)
	}
	f.mediaCall(t, f.member, "POST", "/api/v1/uploads", map[string]any{"filename": "x.png", "mime_type": "image/png", "byte_size": 20, "purpose": "chat", "conversation_id": otherID}, 404)
	f.mediaCall(t, f.member, "POST", "/api/v1/uploads", map[string]any{"filename": "x.png", "mime_type": "image/png", "byte_size": 20, "purpose": "invalid"}, 422)
	f.call(t, f.member, "POST", "/api/v1/chat/conversations/"+id+"/close", nil, 200)
	f.mediaCall(t, f.member, "POST", "/api/v1/uploads", map[string]any{"filename": "x.png", "mime_type": "image/png", "byte_size": 20, "purpose": "chat", "conversation_id": id}, 404)
}
