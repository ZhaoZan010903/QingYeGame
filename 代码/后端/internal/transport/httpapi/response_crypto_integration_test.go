package httpapi

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"github.com/coder/websocket"
	"golang.org/x/crypto/bcrypt"
	"io"
	"net/http"
	"net/http/httptest"
	"qingye/backend/internal/infrastructure/responsecrypto"
	"strings"
	"testing"
	"time"
)

func integrationResponseInit(t *testing.T, a *App, h http.Handler, cookies ...*http.Cookie) (responsecrypto.Key, []*http.Cookie) {
	t.Helper()
	r := httptest.NewRequest("GET", responseInitPath, nil)
	r.Header.Set("X-QY-Init", "1")
	r.Header.Set("Origin", a.Config.AllowedOrigins[0])
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("response init status %d", w.Code)
	}
	var out struct {
		Data responsecrypto.Key `json:"data"`
	}
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Data.ID == "" {
		t.Fatal("missing response key")
	}
	return out.Data, w.Result().Cookies()
}

func cleanupResponseKeys(t *testing.T, a *App, namespace string) {
	t.Helper()
	t.Cleanup(func() {
		var cursor uint64
		ctx := context.Background()
		for {
			keys, next, e := a.Redis.Scan(ctx, cursor, namespace+"*", 100).Result()
			if e != nil {
				return
			}
			if len(keys) > 0 {
				_ = a.Redis.Del(ctx, keys...).Err()
			}
			cursor = next
			if cursor == 0 {
				return
			}
		}
	})
}
func integrationEncryptedCall(t *testing.T, a *App, h http.Handler, key responsecrypto.Key, method, path string, body any, cookies ...*http.Cookie) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	r := responseRequestFor(key, &http.Cookie{Name: "unused", Value: "unused"}, method, path)
	r.Header.Del("Cookie")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	r.Header.Set("Origin", a.Config.AllowedOrigins[0])
	if body != nil {
		raw, _ := json.Marshal(body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Header().Get("X-QY-Protocol-Error") == "1" {
		return w, nil
	}
	return w, decryptResponse(t, a, key, r, w)
}

func TestResponseCryptoLoginLogoutAndOldContext(t *testing.T) {
	a := integrationMemberApp(t)
	a.Config.SessionSecret = randomToken()
	a.Config.ResponseCrypto.Enabled = true
	namespace := "test:r24:http:" + newID() + ":"
	a.ResponseKeys = &responsecrypto.RedisStore{Client: a.Redis, Namespace: namespace}
	cleanupResponseKeys(t, a, namespace)
	h := a.Routes()
	anon, cookies := integrationResponseInit(t, a, h)
	password := "R24-test-password-1"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.DB.Exec(context.Background(), `INSERT INTO users(id,phone,nickname,password_hash,role) VALUES($1,'19900006688','响应协议测试',$2,'member')`, newID(), string(hash))
	if err != nil {
		t.Fatal(err)
	}
	w, plain := integrationEncryptedCall(t, a, h, anon, "POST", "/api/v1/auth/login", map[string]any{"phone": "19900006688", "password": password}, cookies...)
	if w.Code != 200 || !bytes.Contains(plain, []byte("响应协议测试")) {
		t.Fatalf("login encrypted result: %d", w.Code)
	}
	var auth *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			auth = c
		}
	}
	if auth == nil {
		t.Fatal("login cookie lost during encryption")
	}
	cookies = append(cookies, auth)
	w, _ = integrationEncryptedCall(t, a, h, anon, "GET", "/api/v1/me", nil, cookies...)
	if w.Code != 409 {
		t.Fatal("anonymous context survived login")
	}
	member, _ := integrationResponseInit(t, a, h, cookies...)
	if member.ContextID == anon.ContextID {
		t.Fatal("login did not change context")
	}
	w, plain = integrationEncryptedCall(t, a, h, member, "GET", "/api/v1/me", nil, cookies...)
	if w.Code != 200 || !bytes.Contains(plain, []byte("响应协议测试")) {
		t.Fatal("member context not usable")
	}
	w, plain = integrationEncryptedCall(t, a, h, member, "POST", "/api/v1/auth/logout", map[string]any{}, cookies...)
	if w.Code != 200 || !bytes.Contains(plain, []byte(`"ok":true`)) {
		t.Fatal("logout response cannot be decoded with request key")
	}
	w, _ = integrationEncryptedCall(t, a, h, member, "GET", "/api/v1/me", nil, cookies...)
	if w.Code != 409 {
		t.Fatal("revoked session retained response context")
	}
	r := httptest.NewRequest("GET", responseInitPath+"?key_id="+member.ID, nil)
	r.Header.Set("X-QY-Init", "1")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 410 {
		t.Fatalf("logged out key retrieved: %d", w.Code)
	}
}

func TestResponseCryptoWebSocketEncryptedDownstream(t *testing.T) {
	f := newChatFixture(t)
	a := f.a
	a.Config.SessionSecret = randomToken()
	a.Config.ResponseCrypto.Enabled = true
	namespace := "test:r24:ws:" + newID() + ":"
	a.ResponseKeys = &responsecrypto.RedisStore{Client: a.Redis, Namespace: namespace}
	cleanupResponseKeys(t, a, namespace)
	h := a.Routes()
	key, _ := integrationResponseInit(t, a, h, f.cookies[f.member])
	w, plain := integrationEncryptedCall(t, a, h, key, "POST", "/api/v1/chat/transport-ticket", map[string]any{"transport": "websocket"}, f.cookies[f.member])
	if w.Code != 200 {
		t.Fatalf("ticket status %d", w.Code)
	}
	var out struct {
		Data struct {
			Ticket string `json:"ticket"`
			Crypto struct {
				Enabled   bool   `json:"enabled"`
				ContextID string `json:"context_id"`
				ChannelID string `json:"channel_id"`
			} `json:"response_crypto"`
		} `json:"data"`
	}
	if json.Unmarshal(plain, &out) != nil || !out.Data.Crypto.Enabled {
		t.Fatal("encrypted chat binding missing")
	}
	s := httptest.NewServer(h)
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, s.URL+"/api/v1/chat/socket?ticket="+out.Data.Ticket, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {a.Config.AllowedOrigins[0]}, "Cookie": {f.cookies[f.member].String()}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(2 << 20)
	read := func() chatFrame {
		_, data, e := conn.Read(ctx)
		if e != nil {
			t.Fatal(e)
		}
		var envelope responsecrypto.ChatEnvelope
		if json.Unmarshal(data, &envelope) != nil || envelope.Type != "encrypted" || envelope.ContextID != key.ContextID || envelope.ChannelID != out.Data.Crypto.ChannelID {
			t.Fatal("clear or mismatched chat frame")
		}
		k, e := a.responseKeys().Lookup(ctx, key.ContextID, envelope.KeyID)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := base64.RawURLEncoding.DecodeString(k.Material)
		block, _ := aes.NewCipher(raw)
		gcm, _ := cipher.NewGCM(block)
		nonce, _ := base64.RawURLEncoding.DecodeString(envelope.Nonce)
		sealed, _ := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
		aad := []byte(strings.Join([]string{"QY-R24-CHAT", "1", "public", key.ContextID, k.ID, out.Data.Crypto.ChannelID, envelope.FrameID}, "\n"))
		decoded, e := gcm.Open(nil, nonce, sealed, aad)
		if e != nil {
			t.Fatal(e)
		}
		var frame chatFrame
		if json.Unmarshal(decoded, &frame) != nil {
			t.Fatal("bad inner frame")
		}
		return frame
	}
	if read().Type != "ready" {
		t.Fatal("missing encrypted ready")
	}
	if err = conn.Write(ctx, websocket.MessageText, []byte("{\"id\":\"r24-list\",\"op\":\"list\",\"payload\":{}}\n")); err != nil {
		t.Fatal(err)
	}
	if frame := read(); frame.Type != "result" || frame.ID != "r24-list" || frame.Status != 200 {
		t.Fatal("encrypted command response missing")
	}
	// Consumed connection tickets remain single-use even with a valid encryption context.
	other, response, e := websocket.Dial(ctx, s.URL+"/api/v1/chat/socket?ticket="+out.Data.Ticket, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {a.Config.AllowedOrigins[0]}, "Cookie": {f.cookies[f.member].String()}}})
	if other != nil {
		other.CloseNow()
	}
	if e == nil || response == nil || response.StatusCode != 401 {
		t.Fatal("connection ticket reused")
	}
}
