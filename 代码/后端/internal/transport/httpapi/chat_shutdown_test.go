package httpapi

import (
	"context"
	"net"
	"net/http/httptest"
	"testing"
	"time"
)

func TestChatAdmissionStopsBeforeWaiting(t *testing.T) {
	a := &App{}
	if !a.beginChatSession() {
		t.Fatal("initial admission rejected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.WaitForChatSessions(ctx); err == nil {
		t.Fatal("wait ended while a session was active")
	}
	if a.beginChatSession() {
		t.Fatal("admitted a session after shutdown started")
	}
	a.chatSessions.Done()
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := a.WaitForChatSessions(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestWebSocketServerCancellationReleasesLease(t *testing.T) {
	f := newChatFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewUnstartedServer(f.a.Routes())
	server.Config.BaseContext = func(net.Listener) context.Context { return ctx }
	server.Start()
	defer server.Close()
	socket := &chatTestSocket{server: server, a: f.a, f: f, prefix: "/api/v1/chat"}
	conn := socket.dial(t, socket.ticket(t, f.member), f.member, f.a.Config.AllowedOrigins[0], 101)
	defer conn.CloseNow()
	cancel()
	waiting, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err := f.a.WaitForChatSessions(waiting); err != nil {
		t.Fatalf("websocket ignored server cancellation: %v", err)
	}
	count, err := f.a.Redis.ZCard(waiting, "qingye:chat:streams:public:"+f.member).Result()
	if err != nil || count != 0 {
		t.Fatalf("chat lease not released: count=%d err=%v", count, err)
	}
}
