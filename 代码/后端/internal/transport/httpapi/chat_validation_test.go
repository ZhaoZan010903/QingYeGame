package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	domain "qingye/backend/internal/domain/chat"
	"qingye/backend/internal/domain/fault"
	"testing"
)

// PostgreSQL rejects NUL text. These client-controlled identifiers must be
// rejected at the API boundary, not reported as a database/server failure.
func TestChatRejectsInvalidClientIdentifiers(t *testing.T) {
	f := newChatFixture(t)
	f.call(t, f.member, "POST", "/api/v1/chat/conversations", map[string]any{"product_id": "bad\x00product"}, 422)
	id := f.start(t, f.member, f.product)
	f.call(t, f.member, "POST", "/api/v1/chat/conversations/"+id+"/messages", map[string]any{"body": "消息", "client_id": "bad\x00request"}, 422)

	// WebSocket and WebTransport reuse the command handler; reject the same
	// identifier without terminating an otherwise valid realtime session.
	payload, _ := json.Marshal(map[string]any{"body": "消息", "client_id": "bad\x00request"})
	r, _ := http.NewRequestWithContext(context.WithValue(context.Background(), chatSessionKey{}, tokenHash(f.cookies[f.member].Value)), "POST", "/", nil)
	r.Header.Set("Origin", "http://qingye.test")
	frame := f.a.chatCommand(r, chatCommand{ID: "request-1", Op: "send", Conversation: id, Payload: payload})
	if frame.Status != 422 {
		t.Fatalf("realtime invalid identifier status=%d body=%s", frame.Status, frame.Body)
	}
	var count int
	if err := f.a.DB.QueryRow(context.Background(), "SELECT count(*) FROM chat_messages WHERE conversation_id=$1 AND kind='text'", id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid messages persisted: %d %v", count, err)
	}
}

func TestChatRejectsInvalidConversationIDs(t *testing.T) {
	f := newChatFixture(t)
	for _, endpoint := range []struct {
		method, suffix string
		payload        any
	}{
		{"GET", "", nil},
		{"GET", "/messages", nil},
		{"POST", "/messages", map[string]any{"body": "消息", "client_id": "valid"}},
		{"POST", "/read", map[string]any{"through": "1"}},
		{"POST", "/close", map[string]any{}},
	} {
		for _, invalid := range []string{"bad%00id", "bad%FFid"} {
			f.call(t, f.member, endpoint.method, "/api/v1/chat/conversations/"+invalid+endpoint.suffix, endpoint.payload, 422)
			f.call(t, f.first, endpoint.method, "/api/v1/admin/chat/conversations/"+invalid+endpoint.suffix, endpoint.payload, 422)
		}
	}
	for _, op := range []string{"detail", "messages", "send", "read", "close"} {
		r, _ := http.NewRequestWithContext(context.WithValue(context.Background(), chatSessionKey{}, tokenHash(f.cookies[f.member].Value)), "POST", "/", nil)
		r.Header.Set("Origin", "http://qingye.test")
		frame := f.a.chatCommand(r, chatCommand{ID: "request-" + op, Op: op, Conversation: "bad\x00id", Payload: json.RawMessage(`{}`)})
		if frame.Status != 422 {
			t.Fatalf("%s invalid conversation status=%d body=%s", op, frame.Status, frame.Body)
		}
	}
}

// 仅保留旧测试调用入口；实时传输直接传入已认证身份，不构造 HTTP 请求。
func (a *App) chatCommand(r *http.Request, c chatCommand) chatFrame {
	u, err := a.currentUser(r)
	if err != nil {
		return chatFailure(c.ID, fault.New(fault.Unauthenticated, "UNAUTHENTICATED", "请先登录"))
	}
	return a.executeChatCommand(r.Context(), domain.Actor{ID: u.ID, Role: u.Role, Surface: a.surface(), SessionHash: a.requestSessionHash(r)}, c)
}
