package httpapi

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Intercept only ticket consumption; production authentication still queries the real pool.
type ticketResultHook struct {
	value string
	err   error
}

func TestChatStaleLeaseReclaimedAndTicketExpires(t *testing.T) {
	f := newChatFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	key := "qingye:chat:streams:public:" + f.member
	t.Cleanup(func() { _ = f.a.Redis.Del(context.Background(), key, "qingye:limit:chat-ticket:"+f.member).Err() })
	admit := func(want int) string {
		t.Helper()
		data := memberData(t, f.call(t, f.member, "POST", "/api/v1/chat/transport-ticket", map[string]any{"transport": "websocket"}, 200))
		ticket := data["ticket"].(string)
		ttl, err := f.a.Redis.PTTL(ctx, "qingye:chat:ticket:public:"+tokenHash(ticket)).Result()
		if err != nil || ttl <= 0 || ttl > 30*time.Second {
			t.Fatalf("ticket missing bounded TTL: %v %v", ttl, err)
		}
		r := httptest.NewRequest("GET", "/api/v1/chat/socket?ticket="+ticket, nil)
		r.Header.Set("Origin", f.a.Config.AllowedOrigins[0])
		r.AddCookie(f.cookies[f.member])
		w := httptest.NewRecorder()
		_, _, lease, ok := f.a.acceptChatTicket(w, r, "websocket")
		if want == 200 && !ok || want != 200 && (ok || w.Code != want) {
			t.Fatalf("lease admission ok=%v status=%d want=%d", ok, w.Code, want)
		}
		return lease
	}
	first := admit(200)
	for range 3 {
		admit(200)
	}
	admit(429)
	if err := f.a.Redis.ZAdd(ctx, key, redis.Z{Member: first, Score: float64(time.Now().Unix() - 51)}).Err(); err != nil {
		t.Fatal(err)
	}
	admit(200)
	if _, err := f.a.Redis.ZScore(ctx, key, first).Result(); err != redis.Nil {
		t.Fatalf("stale lease not removed: %v", err)
	}
	count, err := f.a.Redis.ZCard(ctx, key).Result()
	if err != nil || count != 4 {
		t.Fatalf("lease recovery exceeded quota: %d %v", count, err)
	}
	ttl, err := f.a.Redis.PTTL(ctx, key).Result()
	if err != nil || ttl <= 0 || ttl > 70*time.Second {
		t.Fatalf("lease set missing bounded TTL: %v %v", ttl, err)
	}
}

func (h ticketResultHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h ticketResultHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h ticketResultHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() != "getdel" {
			return next(ctx, cmd)
		}
		cmd.(*redis.StringCmd).SetVal(h.value)
		cmd.SetErr(h.err)
		return h.err
	}
}

func TestChatTicketInfrastructureFailureIsRetryable(t *testing.T) {
	for _, transport := range []string{"websocket", "webtransport"} {
		t.Run(transport, func(t *testing.T) {
			const origin = "http://qingye.test"
			const session = "test-session"
			request := func() *http.Request {
				r := httptest.NewRequest("GET", "/chat?ticket="+strings.Repeat("a", 64), nil)
				r.Header.Set("Origin", origin)
				r.AddCookie(&http.Cookie{Name: sessionCookie, Value: strings.Repeat("b", 64)})
				return r
			}
			t.Run("redis unavailable", func(t *testing.T) {
				client := redis.NewClient(&redis.Options{})
				_ = client.Close()
				a := &App{Redis: client, Config: Config{AllowedOrigins: []string{origin}}}
				w := httptest.NewRecorder()
				_, _, _, ok := a.acceptChatTicket(w, request(), transport)
				if ok || w.Code != 503 || !strings.Contains(w.Body.String(), "CHAT_UNAVAILABLE") {
					t.Fatalf("Redis failure treated as authentication failure: status=%d body=%s", w.Code, w.Body.String())
				}
			})
			t.Run("missing ticket remains unauthorized", func(t *testing.T) {
				client := redis.NewClient(&redis.Options{})
				defer client.Close()
				client.AddHook(ticketResultHook{err: redis.Nil})
				a := &App{Redis: client, Config: Config{AllowedOrigins: []string{origin}}}
				w := httptest.NewRecorder()
				_, _, _, ok := a.acceptChatTicket(w, request(), transport)
				if ok || w.Code != 401 || !strings.Contains(w.Body.String(), "INVALID_TICKET") {
					t.Fatalf("expired ticket must remain unauthorized: status=%d", w.Code)
				}
			})
			t.Run("database unavailable", func(t *testing.T) {
				pool, err := pgxpool.New(context.Background(), "postgres://unused@127.0.0.1:1/unused?sslmode=disable")
				if err != nil {
					t.Fatal(err)
				}
				pool.Close()
				client := redis.NewClient(&redis.Options{})
				defer client.Close()
				binding := chatTicket{User: "member", Session: session, Surface: "public", Origin: origin, Transport: transport}
				if transport == "websocket" {
					binding.Session = tokenHash(strings.Repeat("b", 64))
				}
				raw, err := json.Marshal(binding)
				if err != nil {
					t.Fatal(err)
				}
				client.AddHook(ticketResultHook{value: string(raw)})
				a := &App{DB: pool, Redis: client, Config: Config{AllowedOrigins: []string{origin}}}
				w := httptest.NewRecorder()
				_, _, _, ok := a.acceptChatTicket(w, request(), transport)
				if ok || w.Code != 503 || !strings.Contains(w.Body.String(), "SESSION_UNAVAILABLE") {
					t.Fatalf("database failure treated as expired session: status=%d body=%s", w.Code, w.Body.String())
				}
			})
		})
	}
}
