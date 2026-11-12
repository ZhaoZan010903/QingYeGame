package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	domain "qingye/backend/internal/domain/chat"
)

func TestChatConversationCursorAndSummaries(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.a.DB.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,phone,nickname,password_hash) SELECT 'page-user-'||n,'page-user-'||n,'分页测试','unused' FROM generate_series(1,123) n`)
	exec(`INSERT INTO chat_conversations(id,member_id,admin_id,product_id,product_snapshot,status,updated_at)
 SELECT 'page-chat-'||lpad(n::text,3,'0'),'page-user-'||n,$1,$2,'{}','closed','2026-01-01'::timestamptz FROM generate_series(1,123) n`, f.first, f.product)
	exec(`INSERT INTO chat_messages(conversation_id,sender_id,sender_role,client_id,body)
 SELECT id,member_id,'member','initial','普通消息' FROM chat_conversations WHERE id LIKE 'page-chat-%'`)
	readPage := func(path string) map[string]any {
		t.Helper()
		w := f.call(t, f.first, "GET", path, nil, 200)
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	base := "/api/v1/admin/chat/conversations"
	first := readPage(base)
	if first["total"] != float64(123) || first["unread"] != float64(123) {
		t.Fatalf("totals: %v", first)
	}
	seen := map[string]bool{}
	page := first
	for n := 123; n > 0; {
		for _, item := range page["data"].([]any) {
			row := item.(map[string]any)
			id := row["id"].(string)
			if id != fmt.Sprintf("page-chat-%03d", n) || seen[id] {
				t.Fatalf("order/repeat %s at %d", id, n)
			}
			seen[id] = true
			n--
			if row["unread"] != float64(1) || row["last_message"] != "普通消息" || row["product_count"] != float64(0) {
				t.Fatalf("row summary %v", row)
			}
		}
		if page["next_cursor"] == nil {
			if n != 0 {
				t.Fatal("missing page")
			}
			break
		}
		page = readPage(base + "?cursor=" + url.QueryEscape(page["next_cursor"].(string)))
	}
	if len(seen) != 123 {
		t.Fatalf("rows %d", len(seen))
	}
	legacy := readPage(base + "?offset=50")
	modern := readPage(base + "?cursor=" + url.QueryEscape(first["next_cursor"].(string)))
	l, _ := json.Marshal(legacy["data"])
	m, _ := json.Marshal(modern["data"])
	if string(l) != string(m) {
		t.Fatal("legacy offset diverges")
	}
	for _, suffix := range []string{"?cursor=" + url.QueryEscape("%%%"), "?cursor=" + url.QueryEscape(first["next_cursor"].(string)) + "&offset=1", "?offset=-1"} {
		f.call(t, f.first, "GET", base+suffix, nil, 422)
	}
	var foreign map[string]any
	w := f.call(t, f.second, "GET", base+"?cursor="+url.QueryEscape(first["next_cursor"].(string)), nil, 200)
	_ = json.Unmarshal(w.Body.Bytes(), &foreign)
	if foreign["total"] != float64(0) || len(foreign["data"].([]any)) != 0 {
		t.Fatal("cursor bypassed ownership")
	}
	// Two other staff identities must remain unread after transfer; sender role alone is insufficient.
	exec(`INSERT INTO chat_messages(conversation_id,sender_id,sender_role,client_id,body) VALUES ('page-chat-123',$1,'admin','previous-staff','之前客服的回复')`, f.second)
	exec(`INSERT INTO chat_messages(conversation_id,sender_id,sender_role,client_id,kind,product_snapshot) SELECT 'page-chat-123','page-user-123','member','product-'||n,'product',v FROM (VALUES (1,'{"id":"A"}'::jsonb),(2,'{"id":"A"}'::jsonb),(3,'{"id":"B"}'::jsonb),(4,'{}'::jsonb)) s(n,v)`)
	detail := f.detail(t, f.first, "page-chat-123")
	if detail["product_count"] != float64(2) || detail["unread"] != float64(6) || detail["last_message"] != "咨询这个商品" || detail["last_message_mine"] != false {
		t.Fatalf("exact summary %v", detail)
	}
	f.call(t, f.first, "POST", base+"/page-chat-123/read", map[string]any{"through": detail["last_message_id"]}, 200)
	updated := readPage(base)
	if updated["unread"] != float64(122) {
		t.Fatalf("unread after read %v", updated["unread"])
	}
	// Both live transports share the cursor command and preserve the same list result.
	socket := newChatTestSocket(t, f, f.admin)
	conn := socket.dial(t, socket.ticket(t, f.first), f.first, socket.a.Config.AllowedOrigins[0], 101)
	frame := socketCommand(t, conn, "list", "", "cursor-list", map[string]any{"cursor": first["next_cursor"]}, 200)
	var rpc map[string]any
	if err := json.Unmarshal(frame.Body, &rpc); err != nil {
		t.Fatal(err)
	}
	if len(rpc["data"].([]any)) != 50 {
		t.Fatal("socket cursor page size")
	}
	socketCommand(t, conn, "list", "", "bad-cursor", map[string]any{"cursor": "invalid"}, 422)
	transport := newChatTestTransport(t, f.admin)
	session := transport.dial(t, transport.ticket(t, f, f.first), transport.origin, 200)
	stream := chatTestStream(t, session)
	payload, _ := json.Marshal(map[string]any{"cursor": first["next_cursor"]})
	if err := json.NewEncoder(stream).Encode(chatCommand{ID: "wt-cursor", Op: "list", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(stream)
	for {
		var response chatFrame
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if response.ID == "wt-cursor" {
			if response.Status != 200 {
				t.Fatal("WebTransport cursor rejected")
			}
			break
		}
	}
}

func TestChatConversationLiveCursor(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	for i := 0; i < 52; i++ {
		id := fmt.Sprintf("live-%03d", i)
		if _, err := f.a.DB.Exec(ctx, `INSERT INTO users(id,phone,nickname,password_hash) VALUES($1,$1,'动态列表','unused')`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := f.a.DB.Exec(ctx, `INSERT INTO chat_conversations(id,member_id,admin_id,product_id,product_snapshot,status,updated_at) VALUES($1,$1,$2,$3,'{}','closed','2026-01-01')`, id, f.first, f.product); err != nil {
			t.Fatal(err)
		}
	}
	actor := domain.Actor{ID: f.first, Role: "admin", Surface: "admin", SessionHash: "test"}
	first, err := f.admin.chatService().List(ctx, actor, 0)
	if err != nil {
		t.Fatal(err)
	}
	cursor := first["next_cursor"].(string)
	// Activity moves a previously visible row to the top without shifting the next cursor page.
	if _, err = f.a.DB.Exec(ctx, `UPDATE chat_conversations SET updated_at=now() WHERE id='live-020'`); err != nil {
		t.Fatal(err)
	}
	second, err := f.admin.chatService().List(ctx, actor, 0, cursor)
	if err != nil {
		t.Fatal(err)
	}
	rows := second["data"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["id"] != "live-001" || rows[1].(map[string]any)["id"] != "live-000" {
		t.Fatalf("cursor drift: %v", rows)
	}
}
