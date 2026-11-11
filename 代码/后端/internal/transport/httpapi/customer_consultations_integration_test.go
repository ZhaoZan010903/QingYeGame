package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCustomerConversationAcrossProductsAndConsultationDedup(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	second := newID()
	f.addProduct(t, second)
	id := f.start(t, f.member, f.product)
	if next := f.start(t, f.member, second); next != id {
		t.Fatal("another product created another customer conversation")
	}
	if next := f.start(t, f.member, second); next != id {
		t.Fatal("double click created another conversation")
	}
	list := f.call(t, f.member, "GET", "/api/v1/chat/conversations", nil, 200)
	var listed struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || listed.Total != 1 {
		t.Fatal("customer list is duplicated", list.Body.String())
	}
	page := f.call(t, f.member, "GET", "/api/v1/chat/conversations/"+id+"/messages", nil, 200)
	var messages struct {
		Data []struct {
			Kind    string `json:"kind"`
			Product struct {
				ID string `json:"id"`
			} `json:"product"`
		} `json:"data"`
	}
	if err := json.Unmarshal(page.Body.Bytes(), &messages); err != nil {
		t.Fatal(err)
	}
	if len(messages.Data) != 2 || messages.Data[0].Product.ID != f.product || messages.Data[1].Product.ID != second {
		t.Fatal("historical product cards changed or double click inserted a card", page.Body.String())
	}
	f.call(t, f.member, "POST", "/api/v1/chat/conversations/"+id+"/messages", map[string]any{"body": "请问这个月租多少钱？😊👍", "client_id": "emoji-question"}, 200)
	f.online(t, f.first, true)
	f.call(t, f.first, "POST", "/api/v1/admin/chat/conversations/"+id+"/close", map[string]any{}, 200)
	if reopened := f.start(t, f.member, second); reopened != id {
		t.Fatal("closed history did not reopen")
	}
	f.start(t, f.other, second)
	var unique int
	if err := f.a.DB.QueryRow(ctx, "SELECT count(*) FROM product_consultations WHERE product_id=$1", second).Scan(&unique); err != nil || unique != 2 {
		t.Fatalf("unique inquiries=%d err=%v", unique, err)
	}
	if err := f.a.RefreshRecommendations(ctx); err != nil {
		t.Fatal(err)
	}
	var score, people int
	if err := f.a.DB.QueryRow(ctx, "SELECT score,consultations FROM product_hot_scores WHERE product_id=$1", second).Scan(&score, &people); err != nil || score != 16 || people != 2 {
		t.Fatalf("popularity score=%d people=%d err=%v", score, people, err)
	}
	// 已存在客服会话也不能再次添加下架商品。
	if _, err := f.a.DB.Exec(ctx, "UPDATE products SET status='offline' WHERE id=$1", second); err != nil {
		t.Fatal(err)
	}
	f.call(t, f.member, "POST", "/api/v1/chat/conversations", map[string]any{"product_id": second}, 404)
}

func TestCustomerConcurrentStartsKeepOneConversation(t *testing.T) {
	f := newChatFixture(t)
	second := newID()
	f.addProduct(t, second)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			product := f.product
			if index%2 == 1 {
				product = second
			}
			w := surfaceRequest(t, f.pub, "POST", "/api/v1/chat/conversations", "http://qingye.test", map[string]any{"product_id": product}, f.cookies[f.member])
			if w.Code != http.StatusOK {
				ids <- "ERROR"
				return
			}
			var result struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &result)
			ids <- result.Data.ID
		}(i)
	}
	wg.Wait()
	close(ids)
	canonical := ""
	for id := range ids {
		if id == "" || id == "ERROR" {
			t.Fatal("parallel start failed")
		}
		if canonical == "" {
			canonical = id
		}
		if canonical != id {
			t.Fatal("parallel customer history duplicated")
		}
	}
	var people int
	if err := f.a.DB.QueryRow(context.Background(), "SELECT count(*) FROM product_consultations WHERE member_id=$1", f.member).Scan(&people); err != nil || people != 2 {
		t.Fatalf("consultation dedup=%d err=%v", people, err)
	}
}

func TestCustomerConsultationsExcludeOwnerAndPreserveFirstTimestamp(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	if _, err := f.a.DB.Exec(ctx, "UPDATE products SET owner_id=$1 WHERE id=$2", f.member, f.product); err != nil {
		t.Fatal(err)
	}
	f.start(t, f.member, f.product)
	var count int
	_ = f.a.DB.QueryRow(ctx, "SELECT count(*) FROM product_consultations WHERE product_id=$1", f.product).Scan(&count)
	if count != 0 {
		t.Fatal("owner consultation raised popularity")
	}
	f.start(t, f.other, f.product)
	old := time.Now().UTC().Add(-10 * 24 * time.Hour)
	if _, err := f.a.DB.Exec(ctx, "UPDATE product_consultations SET first_consulted_at=$1 WHERE product_id=$2", old, f.product); err != nil {
		t.Fatal(err)
	}
	f.start(t, f.other, f.product)
	var first time.Time
	_ = f.a.DB.QueryRow(ctx, "SELECT first_consulted_at FROM product_consultations WHERE product_id=$1", f.product).Scan(&first)
	if first.Sub(old).Abs() > time.Microsecond {
		t.Fatal("repeated inquiry refreshed first timestamp")
	}
	if err := f.a.RefreshRecommendations(ctx); err != nil {
		t.Fatal(err)
	}
	var score int
	_ = f.a.DB.QueryRow(ctx, "SELECT score FROM product_hot_scores WHERE product_id=$1", f.product).Scan(&score)
	if score != 0 {
		t.Fatal("old inquiry regained popularity")
	}
}

func TestCustomerMigrationKeepsMessagesAndOldLinks(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	second := newID()
	f.addProduct(t, second)
	// 只在本测试拥有的schema还原旧聊天结构，验证有历史数据时的有序迁移。
	_, err := f.a.DB.Exec(ctx, `DROP INDEX chat_one_customer;DROP INDEX chat_product_contexts;
 ALTER TABLE chat_conversations DROP COLUMN merged_into;
 ALTER TABLE chat_messages DROP COLUMN product_snapshot;
 DROP TABLE product_consultations;
 ALTER TABLE product_hot_scores DROP COLUMN consultations;
 CREATE UNIQUE INDEX chat_open_product ON chat_conversations(member_id,product_id) WHERE status<>'closed';
 DELETE FROM schema_migrations WHERE version='0006_customer_consultations.sql'`)
	if err != nil {
		t.Fatal(err)
	}
	one, two := newID(), newID()
	for i, pair := range [][2]string{{one, f.product}, {two, second}} {
		_, err = f.a.DB.Exec(ctx, `INSERT INTO chat_conversations(id,member_id,product_id,product_snapshot,updated_at)
 VALUES($1,$2,$3,jsonb_build_object('id',$3::text,'title','原商品','code','历史编号','unit','account','price','168','cover_url',''),now()+make_interval(secs=>$4))`, pair[0], f.member, pair[1], i)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.a.DB.Exec(ctx, `INSERT INTO chat_messages(conversation_id,sender_id,sender_role,client_id,kind,body)
 VALUES($1,$2,'member','product-context','product',''),($1,$2,'member','same-retry-id','text','历史询价')`, pair[0], f.member)
		if err != nil {
			t.Fatal(err)
		}
	}
	// 客户端可自定义幂等键；即使碰到迁移前缀也不能丢失或阻止合并。
	_, err = f.a.DB.Exec(ctx, `INSERT INTO chat_messages(conversation_id,sender_id,sender_role,client_id,body)
 SELECT $1,$2,'member','migrated:'||id::text,'保留迁移键碰撞消息'
 FROM chat_messages WHERE conversation_id=$3 AND client_id='same-retry-id'`, two, f.member, one)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.a.Migrate(ctx); err != nil {
		t.Fatal("migration is not idempotent", err)
	}
	if got := f.detail(t, f.member, one)["id"]; got != two {
		t.Fatal("old link did not resolve", got)
	}
	page := f.call(t, f.member, "GET", "/api/v1/chat/conversations/"+one+"/messages", nil, 200)
	var payload struct {
		Data []json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(page.Body.Bytes(), &payload)
	if len(payload.Data) != 5 || !strings.Contains(page.Body.String(), f.product) || !strings.Contains(page.Body.String(), second) {
		t.Fatal("migration lost messages or product context")
	}
	f.call(t, f.other, "GET", "/api/v1/chat/conversations/"+one, nil, 404)
	f.online(t, f.first, true)
	f.call(t, f.first, "GET", "/api/v1/admin/chat/conversations/"+one+"/messages", nil, 200)
	f.call(t, f.second, "GET", "/api/v1/admin/chat/conversations/"+one+"/messages", nil, 404)
}
