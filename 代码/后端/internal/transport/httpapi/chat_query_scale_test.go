package httpapi

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	domain "qingye/backend/internal/domain/chat"
	"qingye/backend/internal/infrastructure/postgres"
)

// Uses the existing isolated schema; synthetic history is removed with the fixture.
func TestChatQueryScale(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.a.DB.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,phone,nickname,password_hash) SELECT 'scale-member-'||n,'scale-member-'||n,'规模测试','unused' FROM generate_series(1,3000) n`)
	exec(`INSERT INTO chat_conversations(id,member_id,admin_id,product_id,product_snapshot,status,updated_at)
 SELECT 'scale-chat-'||lpad(n::text,5,'0'),'scale-member-'||n,$1,$2,'{}','closed','2026-01-01'::timestamptz+n*interval '1 second' FROM generate_series(1,3000) n`, f.first, f.product)
	exec(`INSERT INTO chat_messages(conversation_id,sender_id,sender_role,client_id,body)
 SELECT c.id,c.member_id,'member','seed-'||n,repeat('测试消息',20) FROM chat_conversations c CROSS JOIN generate_series(1,40) n`)
	exec(`INSERT INTO chat_messages(conversation_id,sender_id,sender_role,client_id,body)
 SELECT 'scale-chat-03000','scale-member-3000','member','long-'||n,repeat('长会话',20) FROM generate_series(1,50000) n`)
	exec(`INSERT INTO chat_messages(conversation_id,sender_id,sender_role,client_id,kind,product_snapshot)
 SELECT 'scale-chat-02999','scale-member-2999','member','product-'||n,'product',jsonb_build_object('id','product-'||(n%200),'title','商品快照') FROM generate_series(1,50000) n`)
	for _, table := range []string{"users", "chat_conversations", "chat_messages"} {
		exec("VACUUM (ANALYZE) " + table)
	}
	store := &postgres.ChatStore{DB: f.a.DB}
	actor := domain.Actor{ID: f.first, Role: "admin", Surface: "admin", SessionHash: "scale-session"}
	measure := func(name string, call func() (map[string]any, error)) {
		t.Helper()
		samples := []float64{}
		for i := 0; i < 16; i++ {
			start := time.Now()
			_, err := call()
			if err != nil {
				t.Fatal(err)
			}
			if i > 0 {
				samples = append(samples, float64(time.Since(start).Microseconds())/1000)
			}
		}
		sort.Float64s(samples)
		t.Logf("SCALE %s median_ms=%.3f p95_ms=%.3f", name, samples[len(samples)/2], samples[len(samples)-1])
	}
	t.Log("SCALE fixture conversations=3000 messages=220000 product_contexts=50000 distinct_contexts=200")
	measure("unread_first_page", func() (map[string]any, error) { return f.admin.chatService().List(ctx, actor, 0) })
	measure("unread_deep_offset", func() (map[string]any, error) { return f.admin.chatService().List(ctx, actor, 2900) })
	var boundary time.Time
	if err := f.a.DB.QueryRow(ctx, `SELECT updated_at FROM chat_conversations WHERE id='scale-chat-00101'`).Scan(&boundary); err != nil {
		t.Fatal(err)
	}
	cursor := domain.EncodeConversationCursor(domain.ConversationCursor{UpdatedAt: boundary, ID: "scale-chat-00101", Position: 2900})
	measure("unread_deep_cursor", func() (map[string]any, error) { return f.admin.chatService().List(ctx, actor, 0, cursor) })
	measure("long_product_detail", func() (map[string]any, error) { return store.Detail(ctx, actor, "scale-chat-02999") })
	info, err := store.Detail(ctx, actor, "scale-chat-02999")
	if err != nil {
		t.Fatal(err)
	}
	detail := info["data"].(map[string]any)
	if detail["product_count"] != float64(200) || detail["unread"] != float64(50040) {
		t.Fatalf("scale detail: %v %v", info, err)
	}
	page, err := f.admin.chatService().List(ctx, actor, 0)
	if err != nil || page["total"] != float64(3000) || page["unread"] != float64(220000) {
		t.Fatal("scale totals mismatch", err)
	}
	var before int64
	if err := f.a.DB.QueryRow(ctx, `SELECT max(id)-25000 FROM chat_messages WHERE conversation_id='scale-chat-03000'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	measure("long_message_history", func() (map[string]any, error) {
		return store.Messages(ctx, actor, "scale-chat-03000", domain.MessagePage{Before: before})
	})
	exec(`UPDATE chat_conversations c SET admin_read_id=coalesce((SELECT max(id) FROM chat_messages WHERE conversation_id=c.id),0)`)
	measure("all_read_first_page", func() (map[string]any, error) { return f.admin.chatService().List(ctx, actor, 0) })
	page, err = f.admin.chatService().List(ctx, actor, 0)
	if err != nil || page["unread"] != float64(0) {
		t.Fatal("all-read total mismatch", err)
	}
	// Concurrent writes target distinct conversations; the normal send path still owns locks and deduplication.
	exec(`UPDATE chat_conversations SET status='active' WHERE id BETWEEN 'scale-chat-00001' AND 'scale-chat-00020'`)
	start := time.Now()
	errors := make(chan error, 20)
	for i := 1; i <= 20; i++ {
		go func(n int) {
			_, err := store.Send(ctx, domain.Actor{ID: fmt.Sprintf("scale-member-%d", n), Role: "member", Surface: "public"}, fmt.Sprintf("scale-chat-%05d", n), domain.SendInput{Body: "并发写入", ClientID: "scale-write"})
			errors <- err
		}(i)
	}
	for i := 0; i < 20; i++ {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("SCALE concurrent_writes count=20 total_ms=%.3f", float64(time.Since(start).Microseconds())/1000)
}
