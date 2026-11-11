package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/webtransport-go"
)

type chatTestTransport struct {
	a      *App
	client *webtransport.Transport
	origin string
}

func TestChatWebTransportConcurrentDelivery(t *testing.T) {
	f := newChatFixture(t)
	server := newChatTestTransport(t, f.a)
	type client struct {
		stream       *webtransport.Stream
		conversation string
	}
	clients := []client{}
	for _, actor := range []string{f.member, f.other, f.actor(t, "member")} {
		session := server.dial(t, server.ticket(t, f, actor), server.origin, 200)
		clients = append(clients, client{chatTestStream(t, session), f.start(t, actor, f.product)})
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	times := []float64{}
	failures := make(chan error, 3)
	started := time.Now()
	for _, client := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decoder := json.NewDecoder(client.stream)
			for i := 0; i < 15; i++ {
				id := fmt.Sprintf("parallel-%d", i)
				payload, _ := json.Marshal(map[string]any{"body": "并发咨询", "client_id": id})
				start := time.Now()
				_ = client.stream.SetDeadline(time.Now().Add(5 * time.Second))
				if e := json.NewEncoder(client.stream).Encode(chatCommand{ID: id, Op: "send", Conversation: client.conversation, Payload: payload}); e != nil {
					failures <- e
					return
				}
				for {
					var frame chatFrame
					if e := decoder.Decode(&frame); e != nil {
						failures <- e
						return
					}
					if frame.ID == id {
						if frame.Status != 200 {
							failures <- fmt.Errorf("send status %d", frame.Status)
							return
						}
						break
					}
				}
				mu.Lock()
				times = append(times, float64(time.Since(start).Microseconds())/1000)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		t.Error(e)
	}
	if len(times) != 45 {
		t.Fatalf("only %d acknowledged messages", len(times))
	}
	var count int
	if e := f.a.DB.QueryRow(context.Background(), "SELECT count(*) FROM chat_messages WHERE kind='text'").Scan(&count); e != nil || count != 45 {
		t.Fatalf("durable rows %d: %v", count, e)
	}
	slices.Sort(times)
	t.Logf("3 connections / 45 durable messages: elapsed=%s ack_p95_ms=%.2f ack_max_ms=%.2f", time.Since(started), times[42], times[44])
}

func newChatTestTransport(t *testing.T, a *App) *chatTestTransport {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "qingye-local-test"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(24 * time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	keyDER, e := x509.MarshalECPrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if e = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); e != nil {
		t.Fatal(e)
	}
	socket, e := net.ListenPacket("udp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	a.Config.ChatWTAddr = socket.LocalAddr().String()
	a.Config.ChatWTURL = "https://" + socket.LocalAddr().String() + "/chat"
	a.Config.ChatWTCert, a.Config.ChatWTKey, a.Config.ChatWTPinCertificate = certFile, keyFile, true
	server, e := a.NewChatTransport()
	if e != nil {
		t.Fatal(e)
	}
	go func() { _ = server.Serve(socket) }()
	cert, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	client := &webtransport.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, NextProtos: []string{"h3"}, MinVersion: tls.VersionTLS13}}
	t.Cleanup(func() { _ = client.Close(); _ = server.Close(); _ = socket.Close() })
	return &chatTestTransport{a: a, client: client, origin: a.Config.AllowedOrigins[0]}
}
func (s *chatTestTransport) ticket(t *testing.T, f *chatFixture, user string) string {
	t.Helper()
	prefix := "/api/v1/chat"
	if s.a.surface() == "admin" {
		prefix = "/api/v1/admin/chat"
	}
	data := memberData(t, f.call(t, user, "POST", prefix+"/transport-ticket", map[string]any{}, 200))
	if len(data["certificate_sha256"].(string)) != 64 {
		t.Fatal("missing development pin")
	}
	return data["ticket"].(string)
}
func (s *chatTestTransport) dial(t *testing.T, ticket, origin string, want int) *webtransport.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, session, e := s.client.Dial(ctx, s.a.Config.ChatWTURL+"?ticket="+ticket, http.Header{"Origin": {origin}})
	if response == nil || response.StatusCode != want {
		t.Fatalf("transport status: response=%v error=%v want=%d", response, e, want)
	}
	if want == 200 && e != nil {
		t.Fatal(e)
	}
	if session != nil {
		t.Cleanup(func() { _ = session.CloseWithError(0, "test finished") })
	}
	return session
}
func chatTestStream(t *testing.T, session *webtransport.Session) *webtransport.Stream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	stream, e := session.OpenStreamSync(ctx)
	if e != nil {
		t.Fatal(e)
	}
	_ = stream.SetDeadline(time.Now().Add(5 * time.Second))
	if _, e = stream.Write([]byte("\n")); e != nil {
		t.Fatal(e)
	}
	var f chatFrame
	if e = json.NewDecoder(stream).Decode(&f); e != nil || f.Type != "ready" {
		t.Fatalf("ready frame %v %v", f, e)
	}
	return stream
}
func chatTestCommand(t *testing.T, stream *webtransport.Stream, op, conversation, client string, payload any, want int) chatFrame {
	t.Helper()
	body, _ := json.Marshal(payload)
	_ = stream.SetDeadline(time.Now().Add(5 * time.Second))
	if e := json.NewEncoder(stream).Encode(chatCommand{ID: client, Op: op, Conversation: conversation, Payload: body}); e != nil {
		t.Fatal(e)
	}
	decoder := json.NewDecoder(stream)
	for i := 0; i < 30; i++ {
		var frame chatFrame
		if e := decoder.Decode(&frame); e != nil {
			t.Fatal(e)
		}
		if frame.ID == client {
			if frame.Status != want {
				t.Fatalf("%s status %d: %s", op, frame.Status, frame.Body)
			}
			return frame
		}
	}
	t.Fatal("missing command response")
	return chatFrame{}
}

func TestChatWebTransportRoundTripAndSecurity(t *testing.T) {
	f := newChatFixture(t)
	public := newChatTestTransport(t, f.a)
	admin := newChatTestTransport(t, f.admin)
	ticket := public.ticket(t, f, f.member)
	t.Run("origin is exact and tickets cannot cross audiences", func(t *testing.T) {
		public.dial(t, ticket, "http://qingye.test.attacker.invalid", 403)
		admin.dial(t, ticket, admin.origin, 401)
	})
	session := public.dial(t, ticket, public.origin, 200)
	member := chatTestStream(t, session)
	t.Run("ticket is consumed once", func(t *testing.T) { public.dial(t, ticket, public.origin, 401) })
	adminSession := admin.dial(t, admin.ticket(t, f, f.first), admin.origin, 200)
	agent := chatTestStream(t, adminSession)
	chatTestCommand(t, agent, "presence", "", "online", map[string]any{"accepting": true}, 200)
	var start struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	frame := chatTestCommand(t, member, "start", "", "start", map[string]any{"product_id": f.product}, 200)
	if e := json.Unmarshal(frame.Body, &start); e != nil || start.Data.ID == "" {
		t.Fatal("missing conversation", e)
	}
	id := start.Data.ID
	if f.assigned(t, id) != f.first {
		t.Fatal("online admin not assigned")
	}
	t.Run("duplex delivery durable ack and idempotent retry", func(t *testing.T) {
		payload := map[string]any{"body": "你好，咨询这个账号 <script>纯文本</script>", "client_id": "durable-retry"}
		first := chatTestCommand(t, member, "send", id, "send-1", payload, 200)
		second := chatTestCommand(t, member, "send", id, "send-2", payload, 200)
		var one, two map[string]any
		_ = json.Unmarshal(first.Body, &one)
		_ = json.Unmarshal(second.Body, &two)
		if one["data"].(map[string]any)["id"] != two["data"].(map[string]any)["id"] {
			t.Fatal("retry duplicated")
		}
		var count int
		_ = f.a.DB.QueryRow(context.Background(), "SELECT count(*) FROM chat_messages WHERE conversation_id=$1 AND client_id='durable-retry'", id).Scan(&count)
		if count != 1 {
			t.Fatal("ack without one durable row")
		}
		chatTestCommand(t, agent, "send", id, "reply", map[string]any{"body": "您好，已为您查看商品。", "client_id": "reply"}, 200)
		messages := chatTestCommand(t, member, "messages", id, "read-messages", map[string]any{}, 200)
		if !strings.Contains(string(messages.Body), "已为您查看商品") {
			t.Fatal("reply not returned over WebTransport")
		}
		chatTestCommand(t, member, "detail", id, "read-detail", map[string]any{}, 200)
		chatTestCommand(t, member, "list", "", "read-list", map[string]any{"offset": 0}, 200)
		response := f.call(t, f.member, "GET", "/api/v1/chat/conversations/"+id+"/messages", nil, 200)
		if !strings.Contains(response.Body.String(), "已为您查看商品") || !strings.Contains(response.Body.String(), "product") {
			t.Fatal("missing reply or product context")
		}
	})
	t.Run("only owner can operate and client cannot select identity", func(t *testing.T) {
		otherID := f.start(t, f.other, f.product)
		chatTestCommand(t, member, "messages", otherID, "forbidden-read", map[string]any{}, 404)
		chatTestCommand(t, member, "send", otherID, "forbidden", map[string]any{"body": "越权", "client_id": "bad"}, 404)
		chatTestCommand(t, member, "presence", "", "wrong-role", map[string]any{}, 422)
		chatTestCommand(t, member, "send", id, "spoof", map[string]any{"body": "x", "client_id": "spoof", "sender_id": f.first}, 422)
	})
	t.Run("redis notifications arrive on the reliable stream", func(t *testing.T) {
		f.a.notifyChat(context.Background(), f.member)
		_ = member.SetReadDeadline(time.Now().Add(3 * time.Second))
		decoder := json.NewDecoder(member)
		for {
			var frame chatFrame
			if e := decoder.Decode(&frame); e != nil {
				t.Fatal(e)
			}
			if frame.Type == "update" {
				break
			}
		}
	})
	t.Run("logout revokes an established transport", func(t *testing.T) {
		_, e := f.a.DB.Exec(context.Background(), "DELETE FROM sessions WHERE token_hash=$1", tokenHash(f.cookies[f.member].Value))
		if e != nil {
			t.Fatal(e)
		}
		f.a.notifyChat(context.Background(), f.member)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		select {
		case <-session.Context().Done():
		case <-ctx.Done():
			t.Fatal("revoked transport remained open")
		}
	})
}
func TestChatWebTransportLimits(t *testing.T) {
	f := newChatFixture(t)
	server := newChatTestTransport(t, f.a)
	t.Run("expired tickets rejected", func(t *testing.T) {
		ticket := server.ticket(t, f, f.other)
		_ = f.a.Redis.Del(context.Background(), "qingye:chat:ticket:public:"+tokenHash(ticket)).Err()
		server.dial(t, ticket, server.origin, 401)
	})
	t.Run("four connections per account across tabs", func(t *testing.T) {
		for i := 0; i < 5; i++ {
			want := 200
			if i == 4 {
				want = 429
			}
			s := server.dial(t, server.ticket(t, f, f.member), server.origin, want)
			if s != nil {
				chatTestStream(t, s)
			}
		}
	})
	t.Run("oversized frames close the stream", func(t *testing.T) {
		session := server.dial(t, server.ticket(t, f, f.other), server.origin, 200)
		stream := chatTestStream(t, session)
		_, _ = stream.Write([]byte(strings.Repeat("x", chatFrameLimit+1) + "\n"))
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		select {
		case <-session.Context().Done():
		case <-ctx.Done():
			t.Fatal("oversized frame accepted")
		}
	})
}
