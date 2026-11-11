package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type chatTestSocket struct {
	server *httptest.Server
	a      *App
	f      *chatFixture
	prefix string
}

func newChatTestSocket(t *testing.T, f *chatFixture, a *App) *chatTestSocket {
	t.Helper()
	s := httptest.NewServer(a.Routes())
	t.Cleanup(s.Close)
	prefix := "/api/v1/chat"
	if a.surface() == "admin" {
		prefix = "/api/v1/admin/chat"
	}
	return &chatTestSocket{s, a, f, prefix}
}
func (s *chatTestSocket) ticket(t *testing.T, user string) string {
	t.Helper()
	data := memberData(t, s.f.call(t, user, "POST", s.prefix+"/transport-ticket", map[string]any{"transport": "websocket"}, 200))
	if data["url"] != s.prefix+"/socket" || data["certificate_sha256"] != "" {
		t.Fatal("unexpected socket configuration")
	}
	return data["ticket"].(string)
}
func (s *chatTestSocket) dial(t *testing.T, ticket, user, origin string, want int) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	headers := http.Header{"Origin": {origin}}
	if cookie := s.f.cookies[user]; cookie != nil {
		headers.Set("Cookie", cookie.String())
	}
	conn, response, err := websocket.Dial(ctx, s.server.URL+s.prefix+"/socket?ticket="+ticket, &websocket.DialOptions{HTTPHeader: headers})
	if response == nil || response.StatusCode != want {
		t.Fatalf("socket status response=%v err=%v want=%d", response, err, want)
	}
	if want == 101 && err != nil {
		t.Fatal(err)
	}
	if conn != nil {
		t.Cleanup(func() { _ = conn.CloseNow() })
		frame := socketRead(t, conn)
		if frame.Type != "ready" {
			t.Fatalf("not ready: %v", frame)
		}
	}
	return conn
}
func socketRead(t *testing.T, conn *websocket.Conn) chatFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var frame chatFrame
	if err = json.Unmarshal(raw, &frame); err != nil {
		t.Fatal(err)
	}
	return frame
}
func socketCommand(t *testing.T, conn *websocket.Conn, op, conversation, id string, payload any, want int) chatFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body, _ := json.Marshal(payload)
	raw, _ := json.Marshal(chatCommand{ID: id, Op: op, Conversation: conversation, Payload: body})
	if err := conn.Write(ctx, websocket.MessageText, append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		frame := socketRead(t, conn)
		if frame.ID == id {
			if frame.Status != want {
				t.Fatalf("%s status=%d body=%s", op, frame.Status, frame.Body)
			}
			return frame
		}
	}
	t.Fatal("missing socket response")
	return chatFrame{}
}

func TestChatWebSocketConcurrentDelivery(t *testing.T) {
	f := newChatFixture(t)
	s := newChatTestSocket(t, f, f.a)
	type client struct {
		conn         *websocket.Conn
		conversation string
	}
	clients := []client{}
	for _, user := range []string{f.member, f.other, f.actor(t, "member")} {
		clients = append(clients, client{s.dial(t, s.ticket(t, user), user, s.a.Config.AllowedOrigins[0], 101), f.start(t, user, f.product)})
	}
	var wg sync.WaitGroup
	failures := make(chan error, 3)
	started := time.Now()
	for _, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			for i := 0; i < 15; i++ {
				id := fmt.Sprintf("ws-parallel-%d", i)
				payload, _ := json.Marshal(map[string]any{"body": "代理兼容并发咨询", "client_id": id})
				raw, _ := json.Marshal(chatCommand{ID: id, Op: "send", Conversation: c.conversation, Payload: payload})
				if err := c.conn.Write(ctx, websocket.MessageText, append(raw, '\n')); err != nil {
					failures <- err
					return
				}
				for {
					_, data, err := c.conn.Read(ctx)
					if err != nil {
						failures <- err
						return
					}
					var frame chatFrame
					if err = json.Unmarshal(data, &frame); err != nil {
						failures <- err
						return
					}
					if frame.ID == id {
						if frame.Status != 200 {
							failures <- fmt.Errorf("ack %d", frame.Status)
							return
						}
						break
					}
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	var count int
	if err := f.a.DB.QueryRow(context.Background(), "SELECT count(*) FROM chat_messages WHERE kind='text'").Scan(&count); err != nil || count != 45 {
		t.Fatalf("durable rows=%d error=%v", count, err)
	}
	t.Logf("3 WebSocket connections / 45 durable messages, elapsed=%s", time.Since(started))
}

func TestChatWebSocketRoundTripAndSecurity(t *testing.T) {
	f := newChatFixture(t)
	s := newChatTestSocket(t, f, f.a)
	admin := newChatTestSocket(t, f, f.admin)
	origin, adminOrigin := s.a.Config.AllowedOrigins[0], admin.a.Config.AllowedOrigins[0]
	wt := newChatTestTransport(t, f.a)
	t.Run("origin cookie transport and audience bindings", func(t *testing.T) {
		ticket := s.ticket(t, f.member)
		s.dial(t, ticket, f.member, origin+".attacker.invalid", 403)
		s.dial(t, ticket, f.member, origin, 101)
		s.dial(t, ticket, f.member, origin, 401)
		s.dial(t, s.ticket(t, f.other), f.member, origin, 401)
		s.dial(t, s.ticket(t, f.other), "", origin, 401)
		s.dial(t, wt.ticket(t, f, f.other), f.other, origin, 401)
		wt.dial(t, s.ticket(t, f.other), origin, 401)
		admin.dial(t, s.ticket(t, f.member), f.first, adminOrigin, 401)
		f.call(t, f.member, "POST", s.prefix+"/transport-ticket", map[string]any{"transport": "unknown"}, 422)
	})
	member := s.dial(t, s.ticket(t, f.member), f.member, origin, 101)
	agent := admin.dial(t, admin.ticket(t, f.first), f.first, adminOrigin, 101)
	socketCommand(t, agent, "presence", "", "online", map[string]any{"accepting": true}, 200)
	start := socketCommand(t, member, "start", "", "start", map[string]any{"product_id": f.product}, 200)
	var response struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(start.Body, &response); err != nil || response.Data.ID == "" {
		t.Fatal("conversation missing", err)
	}
	id := response.Data.ID
	t.Run("duplex messages product context and cross-transport deduplication", func(t *testing.T) {
		payload := map[string]any{"body": "兼容连接咨询", "client_id": "cross-transport-key"}
		socketCommand(t, member, "send", id, "send", payload, 200)
		stream := chatTestStream(t, wt.dial(t, wt.ticket(t, f, f.member), origin, 200))
		chatTestCommand(t, stream, "send", id, "retry", payload, 200)
		socketCommand(t, agent, "send", id, "reply", map[string]any{"body": "客服已收到", "client_id": "admin-reply"}, 200)
		detail := socketCommand(t, member, "detail", id, "detail", map[string]any{}, 200)
		if !strings.Contains(string(detail.Body), "咨询商品") || strings.Contains(string(detail.Body), "private-source-never-expose") {
			t.Fatal("product context/privacy")
		}
		var count int
		if err := f.a.DB.QueryRow(context.Background(), "SELECT count(*) FROM chat_messages WHERE conversation_id=$1 AND kind='text'", id).Scan(&count); err != nil || count != 2 {
			t.Fatalf("dedup rows=%d err=%v", count, err)
		}
	})
	t.Run("other member and unassigned admin cannot access conversation", func(t *testing.T) {
		other := s.dial(t, s.ticket(t, f.other), f.other, origin, 101)
		socketCommand(t, other, "messages", id, "denied", map[string]any{}, 404)
		socketCommand(t, other, "send", id, "denied-send", map[string]any{"body": "x", "client_id": "bad"}, 404)
		second := admin.dial(t, admin.ticket(t, f.second), f.second, adminOrigin, 101)
		socketCommand(t, second, "detail", id, "denied-admin", map[string]any{}, 404)
		socketCommand(t, member, "presence", "", "forbidden-op", map[string]any{}, 422)
	})
}

func TestChatWebSocketLimitsAndRevocation(t *testing.T) {
	f := newChatFixture(t)
	s := newChatTestSocket(t, f, f.a)
	origin := s.a.Config.AllowedOrigins[0]
	wt := newChatTestTransport(t, f.a)
	t.Run("shared connection quota", func(t *testing.T) {
		user := f.actor(t, "member")
		chatTestStream(t, wt.dial(t, wt.ticket(t, f, user), origin, 200))
		for i := 0; i < 3; i++ {
			s.dial(t, s.ticket(t, user), user, origin, 101)
		}
		s.dial(t, s.ticket(t, user), user, origin, 429)
		wt.dial(t, wt.ticket(t, f, user), origin, 429)
	})
	for _, test := range []struct {
		name   string
		binary bool
		value  string
	}{
		{"oversized frame", false, strings.Repeat("x", chatFrameLimit+1)},
		{"binary frame", true, "binary"},
		{"unknown command fields", false, "{\"id\":\"bad\",\"op\":\"list\",\"identity\":\"admin\"}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			user := f.actor(t, "member")
			conn := s.dial(t, s.ticket(t, user), user, origin, 101)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			kind := websocket.MessageText
			if test.binary {
				kind = websocket.MessageBinary
			}
			_ = conn.Write(ctx, kind, []byte(test.value))
			_, _, err := conn.Read(ctx)
			if err == nil || ctx.Err() != nil {
				t.Fatalf("connection should reject promptly: %v", err)
			}
		})
	}
	t.Run("logout invalidates live connection", func(t *testing.T) {
		user := f.actor(t, "member")
		conn := s.dial(t, s.ticket(t, user), user, origin, 101)
		if _, err := f.a.DB.Exec(context.Background(), "DELETE FROM sessions WHERE user_id=$1", user); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Write(ctx, websocket.MessageText, []byte("{\"id\":\"after-logout\",\"op\":\"list\",\"payload\":{}}\n"))
		_, _, err := conn.Read(ctx)
		if err == nil || ctx.Err() != nil {
			t.Fatalf("logged-out session must close: %v", err)
		}
	})
}
