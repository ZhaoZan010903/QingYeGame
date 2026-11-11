package httpapi

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
	"io"
	"log/slog"
	"net/http"
	"net/url"
	domain "qingye/backend/internal/domain/chat"
	"qingye/backend/internal/domain/fault"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
)

const chatFrameLimit = 16 << 10

type chatTicket struct{ User, Session, Surface, Origin, Transport string }
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
	var in struct {
		Transport string `json:"transport"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Transport == "" {
		in.Transport = "webtransport"
	}
	if in.Transport != "webtransport" && in.Transport != "websocket" {
		fail(w, 422, "INVALID_TRANSPORT", "不支持的连接方式")
		return
	}
	if in.Transport == "webtransport" && a.Config.ChatWTAddr == "" {
		fail(w, 503, "CHAT_NOT_CONFIGURED", "在线咨询尚未启用，请稍后重试")
		return
	}
	if !a.memberLimit(w, r, "chat-ticket:"+u.ID, 12, time.Minute) {
		return
	}
	endpoint, pin := a.Config.ChatWTURL, ""
	if in.Transport == "webtransport" {
		var e error
		_, pin, e = a.chatTLS()
		if e != nil {
			fail(w, 503, "CHAT_TLS_UNAVAILABLE", "在线咨询连接暂不可用")
			return
		}
	} else {
		endpoint = "/api/v1/chat/socket"
		if a.surface() == "admin" {
			endpoint = "/api/v1/admin/chat/socket"
		}
	}
	ticket := randomToken()
	data, _ := json.Marshal(chatTicket{u.ID, a.requestSessionHash(r), a.surface(), r.Header.Get("Origin"), in.Transport})
	if e := a.Redis.Set(r.Context(), "qingye:chat:ticket:"+a.surface()+":"+tokenHash(ticket), data, 30*time.Second).Err(); e != nil {
		fail(w, 503, "CHAT_UNAVAILABLE", "在线咨询暂不可用")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"data": map[string]any{"url": endpoint, "ticket": ticket, "certificate_sha256": pin, "expires_in": 30}})
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
		if !a.beginChatSession() {
			fail(w, 503, "CHAT_STOPPING", "服务正在重启，请稍后重连")
			return
		}
		defer a.chatSessions.Done()
		binding, key, lease, ok := a.acceptChatTicket(w, r, "webtransport")
		if !ok {
			return
		}
		defer a.releaseChatLease(key, lease)
		session, err := s.Upgrade(w, r)
		if err != nil {
			return
		}
		a.serveChatSession(session, binding, key, lease, r.RemoteAddr)
	})
	return s, nil
}

// Both transports share exact-origin checks, single-use tickets and one connection quota.
func (a *App) acceptChatTicket(w http.ResponseWriter, r *http.Request, transport string) (chatTicket, string, string, bool) {
	if !a.chatOrigin(r.Header.Get("Origin")) {
		fail(w, 403, "ORIGIN_REJECTED", "请求来源不允许")
		return chatTicket{}, "", "", false
	}
	ticket := r.URL.Query().Get("ticket")
	if len(ticket) != 64 {
		fail(w, 401, "INVALID_TICKET", "请重新连接")
		return chatTicket{}, "", "", false
	}
	// Only a short-lived, one-use random ticket is ever carried in the URL; no access logging here.
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	raw, err := a.Redis.GetDel(ctx, "qingye:chat:ticket:"+a.surface()+":"+tokenHash(ticket)).Bytes()
	var binding chatTicket
	if err != nil || json.Unmarshal(raw, &binding) != nil || binding.Surface != a.surface() || binding.Origin != r.Header.Get("Origin") || binding.Transport != transport {
		cancel()
		fail(w, 401, "INVALID_TICKET", "连接凭据已过期，请重新连接")
		return chatTicket{}, "", "", false
	}
	if transport == "websocket" && a.requestSessionHash(r) != binding.Session {
		cancel()
		fail(w, 401, "SESSION_MISMATCH", "请重新连接")
		return chatTicket{}, "", "", false
	}
	u, err := a.currentUserBySession(ctx, binding.Session)
	if err != nil || u.ID != binding.User {
		cancel()
		fail(w, 401, "SESSION_EXPIRED", "请重新登录")
		return chatTicket{}, "", "", false
	}
	key, lease := "qingye:chat:streams:"+a.surface()+":"+u.ID, newID()
	n, err := a.Redis.Eval(ctx, `redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',ARGV[1]-50);if redis.call('ZCARD',KEYS[1])>=4 then return 0 end;redis.call('ZADD',KEYS[1],ARGV[1],ARGV[2]);redis.call('EXPIRE',KEYS[1],70);return 1`, []string{key}, time.Now().Unix(), lease).Int()
	cancel()
	if err != nil {
		fail(w, 503, "CHAT_UNAVAILABLE", "连接暂不可用")
		return chatTicket{}, "", "", false
	}
	if n != 1 {
		fail(w, 429, "CHAT_LIMIT", "同时打开的咨询页面较多，请关闭多余页面")
		return chatTicket{}, "", "", false
	}
	return binding, key, lease, true
}

func (a *App) releaseChatLease(key, lease string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = a.Redis.ZRem(ctx, key, lease).Err()
}

func (a *App) chatWebSocket(w http.ResponseWriter, r *http.Request) {
	if !a.beginChatSession() {
		fail(w, 503, "CHAT_STOPPING", "服务正在重启，请稍后重连")
		return
	}
	defer a.chatSessions.Done()
	binding, key, lease, ok := a.acceptChatTicket(w, r, "websocket")
	if !ok {
		return
	}
	defer a.releaseChatLease(key, lease)
	// Exact allow-list validation already ran above, including the originating login session.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{binding.Origin}, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	stream := websocket.NetConn(ctx, conn, websocket.MessageText)
	// NetConn disables the library's default limit; restore the protocol limit explicitly.
	conn.SetReadLimit(chatFrameLimit)
	defer func() { _ = conn.CloseNow(); _ = stream.Close() }()
	a.serveChatStream(ctx, cancel, stream, binding, key, lease, r.RemoteAddr)
}

type chatDuplexStream interface {
	io.Reader
	io.Writer
	SetWriteDeadline(time.Time) error
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
	a.serveChatStream(ctx, cancel, stream, binding, key, lease, remote)
}

// One JSON-lines protocol and the same business handlers on QUIC and TCP.
func (a *App) serveChatStream(ctx context.Context, cancel context.CancelFunc, stream chatDuplexStream, binding chatTicket, key, lease, remote string) {
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
	valid := func() bool {
		c, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		u, e := a.currentUserBySession(c, binding.Session)
		return e == nil && u.ID == binding.User
	}
	sub := a.Redis.Subscribe(ctx, chatChannel(binding.User))
	defer sub.Close()
	subCtx, stop := context.WithTimeout(ctx, 3*time.Second)
	_, e := sub.Receive(subCtx)
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
			requestCtx, stop := context.WithTimeout(ctx, 8*time.Second)
			// 每条命令重新认证，退出或停用账户后不继续使用连接时的身份。
			u, err := a.currentUserBySession(requestCtx, binding.Session)
			if err != nil || u.ID != binding.User {
				stop()
				return
			}
			actor := domain.Actor{ID: u.ID, Role: u.Role, Surface: a.surface(), SessionHash: binding.Session}
			response := a.executeChatCommand(requestCtx, actor, command)
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

func decodeChatPayload(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return fault.New(fault.Invalid, "INVALID_BODY", "请求字段或格式无效")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fault.New(fault.Invalid, "INVALID_BODY", "只允许一个JSON对象")
	}
	return nil
}
func (a *App) executeChatCommand(ctx context.Context, actor domain.Actor, c chatCommand) chatFrame {
	svc := a.chatService()
	if err := svc.LimitCommand(ctx, actor); err != nil {
		return chatFailure(c.ID, err)
	}
	var out map[string]any
	var err error
	switch c.Op {
	case "list":
		var in struct {
			Offset int `json:"offset"`
		}
		err = decodeChatPayload(c.Payload, &in)
		if err == nil {
			out, err = svc.List(ctx, actor, in.Offset)
		}
	case "detail":
		out, err = svc.Detail(ctx, actor, c.Conversation)
	case "messages":
		var in struct {
			Before string `json:"before"`
			After  string `json:"after"`
		}
		err = decodeChatPayload(c.Payload, &in)
		if err == nil {
			out, err = svc.Messages(ctx, actor, c.Conversation, in.Before, in.After, in.After != "")
		}
	case "send":
		var in domain.SendInput
		err = decodeChatPayload(c.Payload, &in)
		if err == nil {
			out, err = svc.Send(ctx, actor, c.Conversation, in)
		}
	case "read":
		var in domain.ReadInput
		err = decodeChatPayload(c.Payload, &in)
		if err == nil {
			out, err = svc.Read(ctx, actor, c.Conversation, in)
		}
	case "close":
		out, err = svc.Close(ctx, actor, c.Conversation)
	case "start":
		if actor.Surface != "public" {
			err = invalidChatOperation()
			break
		}
		var in domain.StartInput
		err = decodeChatPayload(c.Payload, &in)
		if err == nil {
			out, err = svc.Start(ctx, actor, in)
		}
	case "presence":
		if actor.Surface != "admin" {
			err = invalidChatOperation()
			break
		}
		var in domain.PresenceInput
		err = decodeChatPayload(c.Payload, &in)
		if err == nil {
			out, err = svc.Presence(ctx, actor, &in)
		}
	default:
		err = invalidChatOperation()
	}
	if err != nil {
		return chatFailure(c.ID, err)
	}
	body, err := json.Marshal(out)
	if err != nil || len(body) > 1<<20 {
		return chatFailure(c.ID, fault.New(fault.Internal, "INTERNAL_ERROR", "服务暂时不可用"))
	}
	return chatFrame{Type: "result", ID: c.ID, Status: 200, Body: body}
}
func invalidChatOperation() error {
	return fault.New(fault.Invalid, "INVALID_OPERATION", "不支持的咨询操作")
}
func chatFailure(id string, err error) chatFrame {
	known, ok := fault.As(err)
	if !ok {
		known = fault.New(fault.Internal, "INTERNAL_ERROR", "服务暂时不可用")
	}
	statuses := map[fault.Kind]int{fault.Invalid: 422, fault.Unauthenticated: 401, fault.Forbidden: 403, fault.NotFound: 404, fault.Conflict: 409, fault.RateLimited: 429, fault.Unavailable: 503, fault.Internal: 500}
	status, ok := statuses[known.Kind]
	if !ok {
		status = 500
	}
	if known.Cause != nil {
		slog.Error("chat command failed", "code", known.Code, "error", known.Cause)
	}
	body, _ := json.Marshal(map[string]string{"code": known.Code, "message": known.Message})
	return chatFrame{Type: "result", ID: id, Status: status, Body: body}
}

func (a *App) beginChatSession() bool {
	a.chatMu.Lock()
	defer a.chatMu.Unlock()
	if a.chatClosing {
		return false
	}
	a.chatSessions.Add(1)
	return true
}

// 先关闭准入再等待，避免新增会话与Wait并发；资源池须保持到租约清理结束。
func (a *App) WaitForChatSessions(ctx context.Context) error {
	a.chatMu.Lock()
	a.chatClosing = true
	a.chatMu.Unlock()
	done := make(chan struct{})
	go func() { a.chatSessions.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
