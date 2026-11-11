package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
)

const chatFrameLimit = 16 << 10

type chatTicket struct{ User, Session, Surface, Origin string }
type chatCommand struct {
	ID           string          `json:"id"`
	Op           string          `json:"op"`
	Conversation string          `json:"conversation,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
}
type chatFrame struct {
	Type   string          `json:"type"`
	ID     string          `json:"id,omitempty"`
	Status int             `json:"status,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
}

func (a *App) chatOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	for _, allowed := range a.Config.AllowedOrigins {
		if origin == strings.TrimRight(allowed, "/") {
			return true
		}
	}
	return false
}

// Certificate pinning is a loopback development option, never an alternative to public TLS.
func (a *App) chatTLS() (*tls.Config, string, error) {
	c := a.Config
	endpoint, err := url.Parse(c.ChatWTURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.Port() == "" || endpoint.Path != "/chat" || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.User != nil {
		return nil, "", errors.New("CHAT_WT_URL must be https://host:port/chat")
	}
	if c.ChatWTAddr == "" || c.ChatWTCert == "" || c.ChatWTKey == "" {
		return nil, "", errors.New("WebTransport requires address, certificate and key")
	}
	cert, err := tls.LoadX509KeyPair(c.ChatWTCert, c.ChatWTKey)
	if err != nil {
		return nil, "", errors.New("cannot load WebTransport TLS certificate/key")
	}
	pin := ""
	if c.ChatWTPinCertificate {
		if h := endpoint.Hostname(); h != "localhost" && h != "127.0.0.1" && h != "::1" {
			return nil, "", errors.New("certificate pinning is restricted to loopback development")
		}
		leaf, e := x509.ParseCertificate(cert.Certificate[0])
		if e != nil {
			return nil, "", e
		}
		if leaf.NotAfter.Sub(leaf.NotBefore) > 14*24*time.Hour || time.Now().Before(leaf.NotBefore) || time.Now().After(leaf.NotAfter) || leaf.PublicKeyAlgorithm != x509.ECDSA {
			return nil, "", errors.New("development pin requires a valid ECDSA certificate lasting at most 14 days")
		}
		hash := sha256.Sum256(cert.Certificate[0])
		pin = hex.EncodeToString(hash[:])
	}
	return http3.ConfigureTLSConfig(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}), pin, nil
}

func (a *App) chatTransportTicket(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	if !a.chatOrigin(r.Header.Get("Origin")) {
		fail(w, 403, "ORIGIN_REQUIRED", "缺少有效请求来源")
		return
	}
	if a.Config.ChatWTAddr == "" {
		fail(w, 503, "CHAT_NOT_CONFIGURED", "在线咨询尚未启用，请稍后重试")
		return
	}
	if !a.memberLimit(w, r, "chat-ticket:"+u.ID, 12, time.Minute) {
		return
	}
	_, pin, e := a.chatTLS()
	if e != nil {
		fail(w, 503, "CHAT_TLS_UNAVAILABLE", "在线咨询连接暂不可用")
		return
	}
	ticket := randomToken()
	data, _ := json.Marshal(chatTicket{u.ID, a.requestSessionHash(r), a.surface(), r.Header.Get("Origin")})
	if e = a.Redis.Set(r.Context(), "qingye:chat:ticket:"+a.surface()+":"+tokenHash(ticket), data, 30*time.Second).Err(); e != nil {
		fail(w, 503, "CHAT_UNAVAILABLE", "在线咨询暂不可用")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"data": map[string]any{"url": a.Config.ChatWTURL, "ticket": ticket, "certificate_sha256": pin, "expires_in": 30}})
}

func (a *App) NewChatTransport() (*webtransport.Server, error) {
	tlsConfig, _, e := a.chatTLS()
	if e != nil {
		return nil, e
	}
	s := &webtransport.Server{H3: &http3.Server{Addr: a.Config.ChatWTAddr, TLSConfig: tlsConfig, MaxHeaderBytes: 16 << 10, IdleTimeout: 60 * time.Second, QUICConfig: &quic.Config{HandshakeIdleTimeout: 5 * time.Second, MaxIdleTimeout: 60 * time.Second, KeepAlivePeriod: 20 * time.Second, MaxIncomingStreams: 8, MaxIncomingUniStreams: 8, MaxStreamReceiveWindow: 64 << 10, MaxConnectionReceiveWindow: 256 << 10}}, Config: &webtransport.Config{MaxIncomingStreams: 1, MaxIncomingUniStreams: -1, MaxIncomingData: 64 << 10}, CheckOrigin: func(r *http.Request) bool { return a.chatOrigin(r.Header.Get("Origin")) }}
	s.H3.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.URL.Path != "/chat" {
			http.NotFound(w, r)
			return
		}
		if !a.chatOrigin(r.Header.Get("Origin")) {
			fail(w, 403, "ORIGIN_REJECTED", "请求来源不允许")
			return
		}
		ticket := r.URL.Query().Get("ticket")
		if len(ticket) != 64 {
			fail(w, 401, "INVALID_TICKET", "请重新连接")
			return
		}
		// Only a short-lived, one-use random ticket is ever carried in the URL; no access logging here.
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		raw, err := a.Redis.GetDel(ctx, "qingye:chat:ticket:"+a.surface()+":"+tokenHash(ticket)).Bytes()
		var binding chatTicket
		if err != nil || json.Unmarshal(raw, &binding) != nil || binding.Surface != a.surface() || binding.Origin != r.Header.Get("Origin") {
			cancel()
			fail(w, 401, "INVALID_TICKET", "连接凭据已过期，请重新连接")
			return
		}
		auth := r.WithContext(context.WithValue(ctx, chatSessionKey{}, binding.Session))
		u, err := a.currentUser(auth)
		if err != nil || u.ID != binding.User {
			cancel()
			fail(w, 401, "SESSION_EXPIRED", "请重新登录")
			return
		}
		key, lease := "qingye:chat:streams:"+a.surface()+":"+u.ID, newID()
		n, err := a.Redis.Eval(ctx, `redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',ARGV[1]-50);if redis.call('ZCARD',KEYS[1])>=4 then return 0 end;redis.call('ZADD',KEYS[1],ARGV[1],ARGV[2]);redis.call('EXPIRE',KEYS[1],70);return 1`, []string{key}, time.Now().Unix(), lease).Int()
		cancel()
		if err != nil {
			fail(w, 503, "CHAT_UNAVAILABLE", "连接暂不可用")
			return
		}
		if n != 1 {
			fail(w, 429, "CHAT_LIMIT", "同时打开的咨询页面较多，请关闭多余页面")
			return
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = a.Redis.ZRem(ctx, key, lease).Err()
		}()
		session, err := s.Upgrade(w, r)
		if err != nil {
			return
		}
		a.serveChatSession(session, binding, key, lease, r.RemoteAddr)
	})
	return s, nil
}

func (a *App) serveChatSession(session *webtransport.Session, binding chatTicket, key, lease, remote string) {
	defer session.CloseWithError(0, "reconnect")
	ctx, cancel := context.WithTimeout(session.Context(), 10*time.Minute)
	defer cancel()
	openCtx, stop := context.WithTimeout(ctx, 8*time.Second)
	stream, e := session.AcceptStream(openCtx)
	stop()
	if e != nil {
		return
	}
	defer stream.CancelRead(0)
	// A session owns exactly one reliable duplex JSON-lines stream.
	go func() {
		if extra, e := session.AcceptStream(ctx); e == nil {
			extra.CancelRead(1)
			extra.CancelWrite(1)
			_ = session.CloseWithError(1, "one stream only")
		}
	}()
	go func() {
		if extra, e := session.AcceptUniStream(ctx); e == nil {
			extra.CancelRead(1)
			_ = session.CloseWithError(1, "unexpected stream")
		}
	}()
	var writeMu sync.Mutex
	send := func(frame chatFrame) bool {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = stream.SetWriteDeadline(time.Now().Add(5 * time.Second))
		e := json.NewEncoder(stream).Encode(frame)
		if e != nil {
			cancel()
		}
		return e == nil
	}
	authRequest := func(ctx context.Context) *http.Request {
		r, _ := http.NewRequestWithContext(context.WithValue(ctx, chatSessionKey{}, binding.Session), "POST", "/", nil)
		r.Header.Set("Origin", binding.Origin)
		r.RemoteAddr = remote
		return r
	}
	valid := func() bool {
		c, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		u, e := a.currentUser(authRequest(c))
		return e == nil && u.ID == binding.User
	}
	sub := a.Redis.Subscribe(ctx, chatChannel(binding.User))
	defer sub.Close()
	subCtx, stop := context.WithTimeout(ctx, 3*time.Second)
	_, e = sub.Receive(subCtx)
	stop()
	if e != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		scan := bufio.NewScanner(stream)
		scan.Buffer(make([]byte, 4096), chatFrameLimit)
		firstFrame, windowStart, windowCount := true, time.Now(), 0
		for scan.Scan() {
			if len(scan.Bytes()) == 0 {
				if !firstFrame {
					return
				}
				firstFrame = false
				continue
			}
			firstFrame = false
			if time.Since(windowStart) >= time.Second {
				windowStart, windowCount = time.Now(), 0
			}
			windowCount++
			if windowCount > 20 {
				return
			}
			var command chatCommand
			decoder := json.NewDecoder(bytes.NewReader(scan.Bytes()))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&command) != nil || decoder.Decode(new(any)) != io.EOF || len(command.ID) == 0 || len(command.ID) > 80 || len(command.Conversation) > 100 {
				return
			}
			if !valid() {
				return
			}
			requestCtx, stop := context.WithTimeout(ctx, 8*time.Second)
			response := a.chatCommand(authRequest(requestCtx), command)
			stop()
			if !send(response) {
				return
			}
		}
	}()
	if !send(chatFrame{Type: "ready"}) {
		return
	}
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case _, ok := <-sub.Channel():
			if !ok || !valid() || !send(chatFrame{Type: "update"}) {
				return
			}
		case <-ticker.C:
			if !valid() {
				return
			}
			leaseCtx, stop := context.WithTimeout(ctx, 3*time.Second)
			n, e := a.Redis.Eval(leaseCtx, `if redis.call('ZSCORE',KEYS[1],ARGV[2])==false then return 0 end;redis.call('ZADD',KEYS[1],'XX',ARGV[1],ARGV[2]);redis.call('EXPIRE',KEYS[1],70);return 1`, []string{key}, time.Now().Unix(), lease).Int()
			stop()
			if e != nil || n != 1 || !send(chatFrame{Type: "update"}) {
				return
			}
		}
	}
}

// Reuse the same transactional business handlers and validation for both surfaces.
// The wire protocol never accepts a URL, HTTP header, identity or arbitrary method.
func (a *App) chatCommand(r *http.Request, c chatCommand) chatFrame {
	w := &chatResponse{header: make(http.Header), status: 200}
	r.Body = io.NopCloser(bytes.NewReader(c.Payload))
	r.ContentLength = int64(len(c.Payload))
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("id", c.Conversation)
	if !a.memberLimit(w, r, "chat-command:"+a.requestSessionHash(r), 360, time.Minute) {
		return w.frame(c.ID)
	}
	var handler http.HandlerFunc
	switch c.Op {
	case "list":
		var in struct {
			Offset int `json:"offset"`
		}
		if !readJSON(w, r, &in) {
			return w.frame(c.ID)
		}
		r.Method = "GET"
		r.URL.RawQuery = fmt.Sprintf("offset=%d", in.Offset)
		handler = a.chatList
	case "detail":
		r.Method = "GET"
		handler = a.chatDetail
	case "messages":
		var in struct {
			Before string `json:"before"`
			After  string `json:"after"`
		}
		if !readJSON(w, r, &in) {
			return w.frame(c.ID)
		}
		r.Method = "GET"
		q := url.Values{}
		if in.Before != "" {
			q.Set("before", in.Before)
		}
		if in.After != "" {
			q.Set("after", in.After)
		}
		r.URL.RawQuery = q.Encode()
		handler = a.chatMessages
	case "send":
		handler = a.chatSend
	case "read":
		handler = a.chatRead
	case "close":
		handler = a.chatClose
	case "start":
		if a.surface() == "public" {
			handler = a.chatStart
		}
	case "presence":
		if a.surface() == "admin" {
			handler = a.chatPresence
		}
	}
	if handler == nil {
		fail(w, 422, "INVALID_OPERATION", "不支持的咨询操作")
	} else {
		handler(w, r)
	}
	return w.frame(c.ID)
}

type chatResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *chatResponse) Header() http.Header  { return w.header }
func (w *chatResponse) WriteHeader(code int) { w.status = code }
func (w *chatResponse) Write(p []byte) (int, error) {
	if w.body.Len()+len(p) > 1<<20 {
		return 0, fmt.Errorf("chat response too large")
	}
	return w.body.Write(p)
}
func (w *chatResponse) frame(id string) chatFrame {
	return chatFrame{Type: "result", ID: id, Status: w.status, Body: w.body.Bytes()}
}
