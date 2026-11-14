package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/infrastructure/cache"
	"qingye/backend/internal/infrastructure/responsecrypto"
	"strconv"
	"strings"
	"sync"
	"time"
)

const responseInitPath = "/api/v1/response-crypto/init"

type responseRequestKey struct{}
type responseRequest struct {
	ContextID  string
	Key        responsecrypto.Key
	RequestID  string
	Completion *responseCompletion
	LocalImage *responseLocalImage
}

type responseCompletion struct{ callbacks []func(int64) }
type responseLocalImage struct{ allowed bool }

// Only a trusted file adapter may opt a successful persistent-local image out of AES.
// It does not remove session, media permission, request admission or download-budget checks.
func markServerLocalImage(r *http.Request) bool {
	if (r.Method != http.MethodGet && r.Method != http.MethodHead) || !strings.HasPrefix(r.URL.Path, "/api/v1/media/") {
		return false
	}
	binding, ok := r.Context().Value(responseRequestKey{}).(responseRequest)
	if !ok || binding.LocalImage == nil {
		return false
	}
	binding.LocalImage.allowed = true
	return true
}

// Media leases must survive capture/encryption and remain held through the wire write.
func afterResponseWrite(r *http.Request, finish func(int64)) bool {
	binding, ok := r.Context().Value(responseRequestKey{}).(responseRequest)
	if !ok || binding.Completion == nil {
		return false
	}
	binding.Completion.callbacks = append(binding.Completion.callbacks, finish)
	return true
}

func (a *App) responseKeys() responsecrypto.Store {
	if a.ResponseKeys != nil {
		return a.ResponseKeys
	}
	return &responsecrypto.RedisStore{Client: a.Redis, Namespace: "qingye:r24:response:" + a.surface() + ":"}
}

func (a *App) responseMAC(value string) string {
	m := hmac.New(sha256.New, []byte(a.Config.SessionSecret))
	m.Write([]byte("qy-r24-response:" + a.surface() + ":" + value))
	return hex.EncodeToString(m.Sum(nil))
}

// A context is a non-reversible reference, never the real authentication cookie.
func (a *App) responseContext(w http.ResponseWriter, r *http.Request, create bool) (string, error) {
	if session := a.requestSessionHash(r); session != "" {
		if _, err := a.currentUser(r); err == nil {
			return a.responseMAC("session:" + session), nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}
	name := "qingye_response_" + a.surface()
	if c, err := r.Cookie(name); err == nil && len(c.Value) < 160 {
		parts := strings.Split(c.Value, ".")
		if len(parts) == 3 && responsecrypto.ValidID(parts[0], 32, 32) {
			created, e := strconv.ParseInt(parts[1], 10, 64)
			if e == nil && created <= time.Now().Unix()+30 && created > time.Now().Add(-24*time.Hour).Unix() && hmac.Equal([]byte(parts[2]), []byte(a.responseMAC("visitor:"+parts[0]+"."+parts[1]))) {
				return a.responseMAC("visitor:" + parts[0]), nil
			}
		}
	}
	if !create {
		return "", responsecrypto.ErrKey
	}
	id := responsecrypto.RandomID()
	value := id + "." + strconv.FormatInt(time.Now().Unix(), 10)
	http.SetCookie(w, &http.Cookie{Name: name, Value: value + "." + a.responseMAC("visitor:"+value), Path: "/api/v1/", HttpOnly: true, Secure: a.Config.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: 86400})
	return a.responseMAC("visitor:" + id), nil
}

func responseProtocolError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-QY-Protocol-Error", "1")
	fail(w, status, code, "响应连接暂不可用，请刷新后重试")
}

func (a *App) responseInit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if r.Header.Get("X-QY-Init") != "1" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		responseProtocolError(w, 403, "RESPONSE_INIT_REQUIRED")
		return
	}
	// The outer origin middleware validates exact origins; a custom header also excludes embedded image/form requests.
	if !a.Config.ResponseCrypto.Enabled {
		writeJSON(w, 200, map[string]any{"data": map[string]any{"enabled": false, "protocol": responsecrypto.Protocol, "surface": a.surface(), "server_time": time.Now().Unix(), "media_access_plan": true}})
		return
	}
	if a.Redis == nil && a.ResponseKeys == nil {
		responseProtocolError(w, 503, "RESPONSE_KEYS_UNAVAILABLE")
		return
	}
	if a.Redis != nil {
		if err := (cache.Limiter{Client: a.Redis}).Allow(r.Context(), "qy:r24:init:"+a.responseMAC(requestIP(r)), 120, time.Minute); err != nil {
			if known, ok := fault.As(err); ok && known.Kind == fault.RateLimited {
				responseProtocolError(w, 429, "RESPONSE_INIT_LIMITED")
			} else {
				responseProtocolError(w, 503, "RESPONSE_KEYS_UNAVAILABLE")
			}
			return
		}
	}
	id := r.URL.Query().Get("key_id")
	if len(r.URL.RawQuery) > 128 || (id != "" && !responsecrypto.ValidID(id, 34, 64)) {
		responseProtocolError(w, 400, "RESPONSE_KEY_INVALID")
		return
	}
	ctxID, err := a.responseContext(w, r, id == "")
	if err != nil {
		if errors.Is(err, responsecrypto.ErrKey) {
			responseProtocolError(w, 409, "RESPONSE_CONTEXT_CHANGED")
		} else {
			responseProtocolError(w, 503, "RESPONSE_KEYS_UNAVAILABLE")
		}
		return
	}
	var key responsecrypto.Key
	if id == "" {
		key, err = a.responseKeys().Current(r.Context(), ctxID)
	} else {
		key, err = a.responseKeys().Lookup(r.Context(), ctxID, id)
	}
	if err != nil {
		if errors.Is(err, responsecrypto.ErrKey) {
			responseProtocolError(w, 410, "RESPONSE_KEY_EXPIRED")
		} else {
			responseProtocolError(w, 503, "RESPONSE_KEYS_UNAVAILABLE")
		}
		return
	}
	writeJSON(w, 200, map[string]any{"data": map[string]any{"enabled": true, "protocol": responsecrypto.Protocol, "surface": a.surface(), "context_id": ctxID, "key_id": key.ID, "key": key.Material, "server_time": time.Now().Unix(), "rotate_at": key.RotateAt, "retain_until": key.RetainUntil, "media_access_plan": true}})
}

type responseBudget struct {
	mu   sync.Mutex
	used int64
}

func (b *responseBudget) take(n int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used+n > 128<<20 {
		return false
	}
	b.used += n
	return true
}
func (b *responseBudget) release(n int64) { b.mu.Lock(); b.used -= n; b.mu.Unlock() }

type responseCapture struct {
	header         http.Header
	status         int
	body           []byte
	limit          int
	err            error
	budget         *responseBudget
	wire           http.ResponseWriter
	binding        responseRequest
	streaming      bool
	wireBytes      int64
	requestContext context.Context
}

func (w *responseCapture) release() {
	if w.budget != nil {
		w.budget.release(int64(cap(w.body)))
	}
	w.body = nil
}
func (w *responseCapture) Header() http.Header { return w.header }
func (w *responseCapture) WriteHeader(status int) {
	if w.status == 0 && status >= 200 {
		w.status = status
		contentType := w.header.Get("Content-Type")
		if status == http.StatusOK && w.wire != nil && w.binding.LocalImage != nil && w.binding.LocalImage.allowed && strings.HasPrefix(contentType, "image/") && !strings.ContainsAny(contentType, "\r\n") {
			w.streaming = true
			responseHeaders(w.wire, w.header, w.binding, contentType, false)
			w.wire.WriteHeader(status)
		}
	}
}
func (w *responseCapture) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if w.streaming {
		if w.requestContext != nil && w.requestContext.Err() != nil {
			return 0, w.requestContext.Err()
		}
		n, err := w.wire.Write(p)
		w.wireBytes += int64(n)
		return n, err
	}
	if w.err != nil {
		return 0, w.err
	}
	if len(p) > w.limit-len(w.body) {
		w.err = errors.New("response exceeds envelope limit")
		return 0, w.err
	}
	need := len(w.body) + len(p)
	if cap(w.body) < need+16 {
		n := max(4096, max(need+16, cap(w.body)*2))
		n = min(n, w.limit+16)
		if cap(w.body) == 0 {
			if size, err := strconv.Atoi(w.header.Get("Content-Length")); err == nil && size >= need && size <= w.limit {
				n = size + 16
			}
		}
		// Both allocations count during growth; no concurrent request can hide the temporary copy.
		if w.budget != nil && !w.budget.take(int64(n)) {
			w.err = errors.New("response buffer capacity reached")
			return 0, w.err
		}
		body := make([]byte, len(w.body), n)
		copy(body, w.body)
		if w.budget != nil {
			w.budget.release(int64(cap(w.body)))
		}
		w.body = body
	}
	w.body = append(w.body, p...)
	return len(p), nil
}

func (a *App) responseCryptoMiddleware(next http.Handler) http.Handler {
	budget := &responseBudget{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.Config.ResponseCrypto.Enabled || !strings.HasPrefix(r.URL.Path, "/api/v1/") || r.URL.Path == responseInitPath || r.URL.Path == "/api/v1/chat/socket" || r.URL.Path == "/api/v1/admin/chat/socket" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("X-QY-Protocol") != responsecrypto.Protocol || !responsecrypto.ValidID(r.Header.Get("X-QY-Request-Id"), 16, 80) {
			responseProtocolError(w, 428, "RESPONSE_PROTOCOL_REQUIRED")
			return
		}
		if a.ResponseKeys == nil && a.Redis == nil {
			responseProtocolError(w, 503, "RESPONSE_KEYS_UNAVAILABLE")
			return
		}
		ctxID, err := a.responseContext(w, r, false)
		if err != nil && !errors.Is(err, responsecrypto.ErrKey) {
			responseProtocolError(w, 503, "RESPONSE_KEYS_UNAVAILABLE")
			return
		}
		if err != nil || ctxID != r.Header.Get("X-QY-Context") {
			responseProtocolError(w, 409, "RESPONSE_CONTEXT_CHANGED")
			return
		}
		key, err := a.responseKeys().Lookup(r.Context(), ctxID, r.Header.Get("X-QY-Key-Id"))
		if err == nil {
			err = responsecrypto.ValidateKey(key)
		}
		if err != nil {
			if errors.Is(err, responsecrypto.ErrKey) {
				responseProtocolError(w, 410, "RESPONSE_KEY_EXPIRED")
			} else {
				responseProtocolError(w, 503, "RESPONSE_KEYS_UNAVAILABLE")
			}
			return
		}
		completion := &responseCompletion{}
		var sent int64
		defer func() {
			for _, finish := range completion.callbacks {
				func() { defer func() { _ = recover() }(); finish(sent) }()
			}
		}()
		binding := responseRequest{ContextID: ctxID, Key: key, RequestID: r.Header.Get("X-QY-Request-Id"), Completion: completion, LocalImage: &responseLocalImage{}}
		limit := 4 << 20
		if strings.HasPrefix(r.URL.Path, "/api/v1/media/") && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			limit = 64 << 20
		}
		capture := &responseCapture{header: w.Header().Clone(), limit: limit, budget: budget, wire: w, binding: binding, requestContext: r.Context()}
		defer capture.release()
		{
			request := r.Clone(context.WithValue(r.Context(), responseRequestKey{}, binding))
			// An authenticated envelope covers the complete object, never a cached or partial body.
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				for _, name := range []string{"Range", "If-Range", "If-Modified-Since", "If-Unmodified-Since", "If-None-Match", "If-Match"} {
					request.Header.Del(name)
				}
			}
			func() {
				defer func() {
					if recover() != nil {
						// A partially transmitted image cannot be replaced with JSON or a new envelope.
						if capture.streaming {
							return
						}
						capture.release()
						capture.header.Del("Content-Length")
						capture.status = 0
						capture.err = nil
						fail(capture, 500, "INTERNAL_ERROR", "服务暂时不可用")
					}
				}()
				next.ServeHTTP(capture, request)
			}()
		}
		if capture.streaming {
			sent = capture.wireBytes
			return
		}
		if capture.err != nil {
			capture.release()
			capture.header.Del("Content-Length")
			capture.status = 0
			capture.err = nil
			fail(capture, 503, "RESPONSE_TOO_LARGE", "返回数据暂不可用，请稍后核对结果")
		}
		if capture.err != nil {
			responseProtocolError(w, 503, "RESPONSE_ENCODING_FAILED")
			return
		}
		if capture.status == 0 {
			capture.status = 200
		}
		if r.Method != http.MethodHead && capture.status != 204 && capture.status != 304 && cap(capture.body) < len(capture.body)+16 {
			if _, err := capture.Write(nil); err != nil {
				responseProtocolError(w, 503, "RESPONSE_ENCODING_FAILED")
				return
			}
		}
		written := a.writeEncryptedResponse(w, r, binding, capture)
		if strings.HasPrefix(capture.header.Get("Content-Type"), "image/") {
			sent = written
		}
	})
}

func (a *App) writeEncryptedResponse(w http.ResponseWriter, r *http.Request, binding responseRequest, capture *responseCapture) int64 {
	contentType := capture.header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if strings.ContainsAny(contentType, "\r\n") {
		responseProtocolError(w, 503, "RESPONSE_ENCODING_FAILED")
		return 0
	}
	responseHeaders(w, capture.header, binding, contentType, true)
	if r.Method == http.MethodHead || capture.status == 204 || capture.status == 304 {
		w.WriteHeader(capture.status)
		return 0
	}
	encoding := a.compressPublicResponse(r, capture, contentType)
	w.Header().Set("X-QY-Content-Encoding", encoding)
	aad := responsecrypto.HTTPAAD(a.surface(), binding.ContextID, binding.Key.ID, binding.RequestID, r.Method, r.URL.RequestURI(), capture.status, contentType, encoding)
	sealed, nonce, err := responsecrypto.Seal(binding.Key, capture.body, aad)
	if err != nil {
		responseProtocolError(w, 503, "RESPONSE_ENCODING_FAILED")
		return 0
	}
	w.Header().Set("X-QY-Nonce", nonce)
	w.Header().Set("Content-Length", strconv.Itoa(len(sealed)))
	if r.Context().Err() != nil {
		return 0
	}
	w.WriteHeader(capture.status)
	n, _ := w.Write(sealed)
	return int64(n)
}

func responseHeaders(w http.ResponseWriter, source http.Header, binding responseRequest, contentType string, encrypted bool) {
	for name := range w.Header() {
		w.Header().Del(name)
	}
	for name, values := range source {
		w.Header()[name] = append([]string(nil), values...)
	}
	for _, name := range []string{"Content-Encoding", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified", "Location", "X-QY-Nonce", "X-QY-Response-Mode"} {
		w.Header().Del(name)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-QY-Protocol", responsecrypto.Protocol)
	w.Header().Set("X-QY-Context", binding.ContextID)
	w.Header().Set("X-QY-Key-Id", binding.Key.ID)
	w.Header().Set("X-QY-Request-Id", binding.RequestID)
	w.Header().Set("X-QY-Content-Type", contentType)
	w.Header().Set("X-QY-Content-Encoding", "identity")
	if encrypted {
		w.Header().Del("Content-Length")
		w.Header().Del("Content-Disposition")
		w.Header().Set("Content-Type", responsecrypto.ContentType)
	} else {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-QY-Response-Mode", "local-image")
	}
}

var _ io.Writer = (*responseCapture)(nil)
