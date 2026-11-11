package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type chatFixture struct {
	a, admin                     *App
	pub, adm                     http.Handler
	member, other, first, second string
	cookies                      map[string]*http.Cookie
	product                      string
}

func newChatFixture(t *testing.T) *chatFixture {
	t.Helper()
	a := integrationMemberApp(t)
	admin := App{DB: a.DB, Redis: a.Redis, Config: a.Config}
	admin.Config = a.Config
	admin.Config.Surface = "admin"
	admin.Config.AllowedOrigins = []string{"http://qingye-admin.test"}
	f := &chatFixture{a: a, admin: &admin, pub: a.Routes(), adm: admin.Routes(), cookies: map[string]*http.Cookie{}, product: newID()}
	f.member = f.actor(t, "member")
	f.other = f.actor(t, "member")
	f.first = f.actor(t, "admin")
	f.second = f.actor(t, "admin")
	if _, e := a.DB.Exec(context.Background(), "INSERT INTO categories(id,name,trade_type,price_units) VALUES('chat-test','聊天测试','sale','[\"account\"]')"); e != nil {
		t.Fatal(e)
	}
	f.addProduct(t, f.product)
	return f
}
func (f *chatFixture) actor(t *testing.T, role string) string {
	t.Helper()
	id, token := newID(), randomToken()
	surface, name := "public", sessionCookie
	if role == "admin" {
		surface, name = "admin", adminSessionCookie
	}
	if _, e := f.a.DB.Exec(context.Background(), "INSERT INTO users(id,phone,nickname,password_hash,role) VALUES($1,$1,$2,'unused',$3)", id, "聊天验收", role); e != nil {
		t.Fatal(e)
	}
	if _, e := f.a.DB.Exec(context.Background(), "INSERT INTO sessions(token_hash,user_id,audience,expires_at) VALUES($1,$2,$3,now()+interval '1 hour')", tokenHash(token), id, surface); e != nil {
		t.Fatal(e)
	}
	f.cookies[id] = &http.Cookie{Name: name, Value: token}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, key := range []string{"chat-start:", "chat-send:", "chat-presence:", "chat-read:"} {
			_ = f.a.Redis.Del(ctx, "qingye:limit:"+key+id).Err()
		}
	})
	return id
}
func (f *chatFixture) addProduct(t *testing.T, id string) {
	t.Helper()
	if _, e := f.a.DB.Exec(context.Background(), `INSERT INTO products(id,product_code,category_id,title,trade_type,price_unit,price_amount,status,source,version,approved_version,legacy_payload) VALUES($1,$1,'chat-test','咨询商品','sale','account',168,'published','platform',1,1,'{"secret":"private-source-never-expose"}')`, id); e != nil {
		t.Fatal(e)
	}
}
func (f *chatFixture) call(t *testing.T, id, method, path string, body any, want int) *httptest.ResponseRecorder {
	t.Helper()
	handler, origin := f.pub, "http://qingye.test"
	if strings.HasPrefix(path, "/api/v1/admin/") {
		handler, origin = f.adm, "http://qingye-admin.test"
	}
	w := surfaceRequest(t, handler, method, path, origin, body, f.cookies[id])
	statusIs(t, w, want)
	return w
}
func (f *chatFixture) start(t *testing.T, user, product string) string {
	t.Helper()
	return memberData(t, f.call(t, user, "POST", "/api/v1/chat/conversations", map[string]any{"product_id": product}, 200))["id"].(string)
}
func (f *chatFixture) online(t *testing.T, id string, accepting bool) {
	t.Helper()
	f.call(t, id, "POST", "/api/v1/admin/chat/presence", map[string]any{"accepting": accepting}, 200)
}
func (f *chatFixture) detail(t *testing.T, user, id string) map[string]any {
	t.Helper()
	prefix := "/api/v1/chat"
	if f.cookies[user].Name == adminSessionCookie {
		prefix = "/api/v1/admin/chat"
	}
	return memberData(t, f.call(t, user, "GET", prefix+"/conversations/"+id, nil, 200))
}
func (f *chatFixture) assigned(t *testing.T, id string) string {
	t.Helper()
	var value string
	if e := f.a.DB.QueryRow(context.Background(), "SELECT coalesce(admin_id,'') FROM chat_conversations WHERE id=$1", id).Scan(&value); e != nil {
		t.Fatal(e)
	}
	return value
}

func TestChatLifecycleAndPrivacy(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	id := f.start(t, f.member, f.product)
	t.Run("offline visitors queue and repeated starts reuse conversation", func(t *testing.T) {
		if f.detail(t, f.member, id)["status"] != "waiting" {
			t.Fatal("must wait without online admin")
		}
		if f.start(t, f.member, f.product) != id {
			t.Fatal("duplicate open conversation")
		}
	})
	t.Run("queued messages survive assignment", func(t *testing.T) {
		f.call(t, f.member, "POST", "/api/v1/chat/conversations/"+id+"/messages", map[string]any{"body": "想看这个商品", "client_id": "first"}, 200)
		f.online(t, f.first, true)
		if f.assigned(t, id) != f.first {
			t.Fatal("not assigned to eligible admin")
		}
		if f.detail(t, f.first, id)["unread"].(float64) != 2 {
			t.Fatal("product context and queued text must be unread")
		}
	})
	t.Run("pause excludes new inquiries without dropping active conversation", func(t *testing.T) {
		f.online(t, f.first, false)
		if f.assigned(t, id) != f.first {
			t.Fatal("pause dropped active conversation")
		}
		another := f.start(t, f.other, f.product)
		if f.assigned(t, another) != "" {
			t.Fatal("paused admin received new inquiry")
		}
		f.online(t, f.second, true)
		if f.assigned(t, another) != f.second {
			t.Fatal("waiting inquiry not dispatched")
		}
	})
	t.Run("membership and surface boundaries", func(t *testing.T) {
		for _, methodPath := range [][2]string{{"GET", ""}, {"GET", "/messages"}, {"POST", "/messages"}, {"POST", "/read"}} {
			var body any
			if methodPath[1] == "/messages" && methodPath[0] == "POST" {
				body = map[string]any{"body": "unauthorized", "client_id": "unauthorized"}
			}
			if methodPath[1] == "/read" {
				body = map[string]any{"through": "1"}
			}
			f.call(t, f.other, methodPath[0], "/api/v1/chat/conversations/"+id+methodPath[1], body, 404)
			f.call(t, f.second, methodPath[0], "/api/v1/admin/chat/conversations/"+id+methodPath[1], body, 404)
		}
		f.call(t, f.other, "POST", "/api/v1/chat/conversations/"+id+"/close", map[string]any{}, 409)
		statusIs(t, surfaceRequest(t, f.pub, "GET", "/api/v1/chat/conversations", "http://qingye.test", nil), 401)
		statusIs(t, surfaceRequest(t, f.adm, "GET", "/api/v1/admin/chat/conversations", "http://qingye-admin.test", nil, f.cookies[f.member]), 401)
		statusIs(t, surfaceRequest(t, f.pub, "GET", "/api/v1/admin/chat/conversations", "http://qingye.test", nil, f.cookies[f.member]), 404)
		statusIs(t, surfaceRequest(t, f.adm, "POST", "/api/v1/admin/chat/conversations/"+id+"/messages", "https://evil.example", map[string]any{"body": "cross", "client_id": "cross"}, f.cookies[f.first]), 403)
	})
	t.Run("message validation and server-owned identity", func(t *testing.T) {
		for _, body := range []map[string]any{{"body": " ", "client_id": "empty"}, {"body": strings.Repeat("字", 2001), "client_id": "long"}, {"body": "x", "client_id": "spoof", "sender_id": f.first}, {"body": "x", "client_id": "spoof", "kind": "product"}} {
			f.call(t, f.member, "POST", "/api/v1/chat/conversations/"+id+"/messages", body, 422)
		}
	})
	t.Run("same request retries once and different body conflicts", func(t *testing.T) {
		body := map[string]any{"body": "回复内容", "client_id": "retry"}
		first := memberData(t, f.call(t, f.first, "POST", "/api/v1/admin/chat/conversations/"+id+"/messages", body, 200))
		again := memberData(t, f.call(t, f.first, "POST", "/api/v1/admin/chat/conversations/"+id+"/messages", body, 200))
		if first["id"] != again["id"] {
			t.Fatal("retry duplicated")
		}
		body["body"] = "different"
		f.call(t, f.first, "POST", "/api/v1/admin/chat/conversations/"+id+"/messages", body, 409)
	})
	t.Run("read cursor does not consume later incoming messages", func(t *testing.T) {
		last := f.detail(t, f.member, id)["last_message_id"].(string)
		f.call(t, f.first, "POST", "/api/v1/admin/chat/conversations/"+id+"/messages", map[string]any{"body": "稍后到达", "client_id": "later"}, 200)
		f.call(t, f.member, "POST", "/api/v1/chat/conversations/"+id+"/read", map[string]any{"through": last}, 200)
		if f.detail(t, f.member, id)["unread"].(float64) != 1 {
			t.Fatal("read acknowledgement consumed unseen message")
		}
		f.call(t, f.member, "POST", "/api/v1/chat/conversations/"+id+"/read", map[string]any{"through": "1"}, 200)
		if f.detail(t, f.member, id)["unread"].(float64) != 1 {
			t.Fatal("read cursor moved backwards")
		}
	})
	t.Run("snapshots expose only display fields", func(t *testing.T) {
		w := f.call(t, f.member, "GET", "/api/v1/chat/conversations/"+id, nil, 200)
		if strings.Contains(w.Body.String(), "private-source-never-expose") || strings.Contains(w.Body.String(), "owner_id") {
			t.Fatal("private product metadata leaked")
		}
	})
	t.Run("offline admin redispatch and previous admin loses access", func(t *testing.T) {
		if _, e := f.a.DB.Exec(ctx, "UPDATE chat_presence SET last_seen=now()-interval '2 minutes' WHERE admin_id=$1", f.first); e != nil {
			t.Fatal(e)
		}
		if e := f.a.DispatchChats(ctx); e != nil {
			t.Fatal(e)
		}
		if f.assigned(t, id) != f.second {
			t.Fatal("offline conversation not reassigned")
		}
		f.call(t, f.first, "GET", "/api/v1/admin/chat/conversations/"+id+"/messages", nil, 404)
		f.call(t, f.second, "GET", "/api/v1/admin/chat/conversations/"+id+"/messages", nil, 200)
	})
	t.Run("closed chats retain history and need a new consultation", func(t *testing.T) {
		f.call(t, f.second, "POST", "/api/v1/admin/chat/conversations/"+id+"/close", map[string]any{}, 200)
		f.call(t, f.member, "POST", "/api/v1/chat/conversations/"+id+"/messages", map[string]any{"body": "closed", "client_id": "closed"}, 409)
		f.call(t, f.member, "GET", "/api/v1/chat/conversations/"+id+"/messages", nil, 200)
		if next := f.start(t, f.member, f.product); next == id {
			t.Fatal("closed conversation reused")
		}
	})
	t.Run("private or withdrawn products cannot start new inquiries", func(t *testing.T) {
		other := newID()
		f.addProduct(t, other)
		if _, e := f.a.DB.Exec(ctx, "UPDATE products SET status='draft' WHERE id=$1", other); e != nil {
			t.Fatal(e)
		}
		f.call(t, f.member, "POST", "/api/v1/chat/conversations", map[string]any{"product_id": other}, 404)
	})
}

func TestChatConcurrentAssignmentAndMessages(t *testing.T) {
	f := newChatFixture(t)
	f.online(t, f.first, true)
	f.online(t, f.second, true)
	actors := make([]string, 12)
	for i := range actors {
		actors[i] = f.actor(t, "member")
	}
	var wg sync.WaitGroup
	results := make(chan int, 12)
	for _, actor := range actors {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			w := surfaceRequest(t, f.pub, "POST", "/api/v1/chat/conversations", "http://qingye.test", map[string]any{"product_id": f.product}, f.cookies[id])
			results <- w.Code
		}(actor)
	}
	wg.Wait()
	close(results)
	for status := range results {
		if status != 200 {
			t.Fatalf("parallel start returned %d", status)
		}
	}
	t.Run("concurrent dispatch respects each admin capacity", func(t *testing.T) {
		for _, id := range []string{f.first, f.second} {
			var n int
			if e := f.a.DB.QueryRow(context.Background(), "SELECT count(*) FROM chat_conversations WHERE admin_id=$1 AND status='active'", id).Scan(&n); e != nil || n != chatCapacity {
				t.Fatalf("admin capacity got %d error %v", n, e)
			}
		}
		var waiting int
		_ = f.a.DB.QueryRow(context.Background(), "SELECT count(*) FROM chat_conversations WHERE status='waiting'").Scan(&waiting)
		if waiting != 6 {
			t.Fatalf("waiting %d expected 6", waiting)
		}
	})
	id := f.start(t, actors[0], f.product)
	t.Run("parallel retries produce one durable message", func(t *testing.T) {
		results := make(chan int, 20)
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				w := surfaceRequest(t, f.pub, "POST", "/api/v1/chat/conversations/"+id+"/messages", "http://qingye.test", map[string]any{"body": "并发重试", "client_id": "same-request"}, f.cookies[actors[0]])
				results <- w.Code
			}()
		}
		wg.Wait()
		close(results)
		for status := range results {
			if status != 200 {
				t.Fatalf("parallel send %d", status)
			}
		}
		var count int
		_ = f.a.DB.QueryRow(context.Background(), "SELECT count(*) FROM chat_messages WHERE conversation_id=$1 AND client_id='same-request'", id).Scan(&count)
		if count != 1 {
			t.Fatalf("message count %d", count)
		}
	})
	t.Run("ending a conversation releases a slot", func(t *testing.T) {
		var activeID, adminID string
		_ = f.a.DB.QueryRow(context.Background(), "SELECT id,admin_id FROM chat_conversations WHERE status='active' LIMIT 1").Scan(&activeID, &adminID)
		f.call(t, adminID, "POST", "/api/v1/admin/chat/conversations/"+activeID+"/close", map[string]any{}, 200)
		var waiting int
		_ = f.a.DB.QueryRow(context.Background(), "SELECT count(*) FROM chat_conversations WHERE status='waiting'").Scan(&waiting)
		if waiting != 5 {
			t.Fatalf("queue not drained %d", waiting)
		}
	})
}

func TestChatHistoryPaging(t *testing.T) {
	f := newChatFixture(t)
	id := f.start(t, f.member, f.product)
	ctx := context.Background()
	for i := 0; i < 125; i++ {
		if _, e := f.a.DB.Exec(ctx, "INSERT INTO chat_messages(conversation_id,sender_id,sender_role,client_id,body) VALUES($1,$2,'member',$3,$3)", id, f.member, fmt.Sprintf("history-%d", i)); e != nil {
			t.Fatal(e)
		}
	}
	t.Run("forward recovery and backward history have no missing or repeated rows", func(t *testing.T) {
		seen := map[string]bool{}
		before := ""
		for {
			path := "/api/v1/chat/conversations/" + id + "/messages"
			if before != "" {
				path += "?before=" + before
			}
			w := f.call(t, f.member, "GET", path, nil, 200)
			var page struct {
				Data   []map[string]any `json:"data"`
				Before *string          `json:"before"`
			}
			if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil {
				t.Fatal(e)
			}
			for _, m := range page.Data {
				mid := m["id"].(string)
				if seen[mid] {
					t.Fatal("duplicate history")
				}
				seen[mid] = true
			}
			if page.Before == nil {
				break
			}
			before = *page.Before
		}
		if len(seen) != 126 {
			t.Fatalf("history length %d", len(seen))
		}
		after := "0"
		count := 0
		for {
			w := f.call(t, f.member, "GET", "/api/v1/chat/conversations/"+id+"/messages?after="+after, nil, 200)
			var page struct {
				Data  []map[string]any `json:"data"`
				After *string          `json:"after"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &page)
			count += len(page.Data)
			if page.After == nil {
				break
			}
			after = *page.After
		}
		if count != 126 {
			t.Fatalf("forward length %d", count)
		}
		f.call(t, f.member, "GET", "/api/v1/chat/conversations/"+id+"/messages?after=1&before=2", nil, 422)
	})
}
