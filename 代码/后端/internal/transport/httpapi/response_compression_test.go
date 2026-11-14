package httpapi

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"qingye/backend/internal/infrastructure/responsecrypto"
	"strings"
	"testing"
)

const compressionProductID = "b169cb43-9455-4139-aa49-e4a6dd4c1321"

func TestResponseCompressionPublicEnvelopeRoundTrip(t *testing.T) {
	want := []byte(`{"data":{"title":"` + strings.Repeat("清叶商品", 1000) + `"}}`)
	for _, path := range []string{
		"/api/v1/categories", "/api/v1/home", "/api/v1/products?page_size=20",
		"/api/v1/products/" + compressionProductID,
		"/api/v1/products/" + compressionProductID + "/images?offset=12",
	} {
		t.Run(path, func(t *testing.T) {
			a, key, cookie := responseFixture(t)
			r := responseRequestFor(key, cookie, "GET", path)
			r.Header.Set("X-QY-Accept-Encoding", "gzip")
			w := httptest.NewRecorder()
			a.responseCryptoMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				_, _ = w.Write(want)
			})).ServeHTTP(w, r)
			if w.Code != 200 || w.Header().Get("X-QY-Content-Encoding") != "gzip" || w.Header().Get("Content-Encoding") != "" || w.Body.Len() >= len(want)+16 {
				t.Fatal("public JSON did not compress inside envelope")
			}
			z, err := gzip.NewReader(bytes.NewReader(decryptResponse(t, a, key, r, w)))
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(io.LimitReader(z, (4<<20)+1))
			_ = z.Close()
			if err != nil || !bytes.Equal(got, want) {
				t.Fatal("decompression changed JSON")
			}
			raw, _ := base64.RawURLEncoding.DecodeString(key.Material)
			block, _ := aes.NewCipher(raw)
			gcm, _ := cipher.NewGCM(block)
			nonce, _ := base64.RawURLEncoding.DecodeString(w.Header().Get("X-QY-Nonce"))
			badAAD := responsecrypto.HTTPAAD(a.surface(), key.ContextID, key.ID, r.Header.Get("X-QY-Request-Id"), r.Method, r.URL.RequestURI(), w.Code, w.Header().Get("X-QY-Content-Type"), "identity")
			if _, err := gcm.Open(nil, nonce, w.Body.Bytes(), badAAD); err == nil {
				t.Fatal("encoding metadata was not authenticated")
			}
		})
	}
}

func TestResponseCompressionExcludesSensitiveAndNonCanonicalRequests(t *testing.T) {
	for _, tt := range []struct {
		name, path, surface, method, mime, accept string
		status, size                              int
	}{
		{"private", "/api/v1/me", "public", "GET", "application/json", "gzip", 200, 4096},
		{"admin-path", "/api/v1/admin/products", "public", "GET", "application/json", "gzip", 200, 4096},
		{"admin-surface", "/api/v1/products", "admin", "GET", "application/json", "gzip", 200, 4096},
		{"auth", "/api/v1/auth/capabilities", "public", "GET", "application/json", "gzip", 200, 4096},
		{"sms", "/api/v1/auth/sms", "public", "GET", "application/json", "gzip", 200, 4096},
		{"init", responseInitPath, "public", "GET", "application/json", "gzip", 200, 4096},
		{"chat", "/api/v1/chat/ticket", "public", "GET", "application/json", "gzip", 200, 4096},
		{"post", "/api/v1/products", "public", "POST", "application/json", "gzip", 200, 4096},
		{"head", "/api/v1/products", "public", "HEAD", "application/json", "gzip", 200, 4096},
		{"error", "/api/v1/products", "public", "GET", "application/json", "gzip", 403, 4096},
		{"redirect", "/api/v1/products", "public", "GET", "application/json", "gzip", 302, 4096},
		{"image", "/api/v1/products", "public", "GET", "image/jpeg", "gzip", 200, 4096},
		{"bad-mime", "/api/v1/products", "public", "GET", "application/json;?", "gzip", 200, 4096},
		{"missing-capability", "/api/v1/products", "public", "GET", "application/json", "", 200, 4096},
		{"unsupported-capability", "/api/v1/products", "public", "GET", "application/json", "br", 200, 4096},
		{"small", "/api/v1/products", "public", "GET", "application/json", "gzip", 200, 1023},
		{"too-large", "/api/v1/products", "public", "GET", "application/json", "gzip", 200, (4 << 20) + 1},
		{"trailing-slash", "/api/v1/products/", "public", "GET", "application/json", "gzip", 200, 4096},
		{"encoded-alias", "/api/v1/%70roducts", "public", "GET", "application/json", "gzip", 200, 4096},
		{"invalid-id", "/api/v1/products/published", "public", "GET", "application/json", "gzip", 200, 4096},
		{"uppercase-id", "/api/v1/products/" + strings.ToUpper(compressionProductID), "public", "GET", "application/json", "gzip", 200, 4096},
		{"extra-path", "/api/v1/products/" + compressionProductID + "/images/extra", "public", "GET", "application/json", "gzip", 200, 4096},
		{"image-trailing-slash", "/api/v1/products/" + compressionProductID + "/images/", "public", "GET", "application/json", "gzip", 200, 4096},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := &App{Config: Config{Surface: tt.surface}}
			r := httptest.NewRequest(tt.method, tt.path, nil)
			r.Header.Set("X-QY-Accept-Encoding", tt.accept)
			body := bytes.Repeat([]byte("a"), tt.size)
			capture := &responseCapture{body: append([]byte(nil), body...), status: tt.status}
			if encoding := a.compressPublicResponse(r, capture, tt.mime); encoding != "identity" || !bytes.Equal(capture.body, body) {
				t.Fatal("excluded response compressed or changed")
			}
		})
	}
}

func TestResponseCompressionFallbackAndBudgetRelease(t *testing.T) {
	a := &App{}
	r := httptest.NewRequest("GET", "/api/v1/products", nil)
	r.Header.Set("X-QY-Accept-Encoding", "gzip")
	for _, mode := range []string{"success", "no-gain", "workspace-full", "output-full"} {
		t.Run(mode, func(t *testing.T) {
			body := bytes.Repeat([]byte("a"), 4096)
			if mode == "no-gain" {
				if _, err := rand.Read(body); err != nil {
					t.Fatal(err)
				}
			}
			budget := &responseBudget{}
			capture := &responseCapture{header: http.Header{}, limit: 4 << 20, budget: budget}
			if _, err := capture.Write(body); err != nil {
				t.Fatal(err)
			}
			var reserved int64
			if mode == "workspace-full" {
				reserved = (128 << 20) - budget.used
			} else if mode == "output-full" {
				reserved = (128 << 20) - (2 << 20) - 1 - budget.used
			}
			if !budget.take(reserved) {
				t.Fatal("budget setup failed")
			}
			encoding := a.compressPublicResponse(r, capture, "application/json")
			if (encoding == "gzip") != (mode == "success") {
				t.Fatalf("unexpected encoding %s", encoding)
			}
			if mode != "success" && !bytes.Equal(capture.body, body) {
				t.Fatal("failed compression changed plaintext")
			}
			if budget.used != reserved+int64(cap(capture.body)) {
				t.Fatal("compression leaked or undercounted buffer budget")
			}
			capture.release()
			budget.release(reserved)
			if budget.used != 0 {
				t.Fatal("response completion leaked budget")
			}
		})
	}
}
