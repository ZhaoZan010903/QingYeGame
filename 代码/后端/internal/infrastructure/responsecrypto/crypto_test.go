package responsecrypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"strings"
	"testing"
)

func TestResponseEnvelopeGCMAuthenticatesMetadataAndBody(t *testing.T) {
	raw := bytes.Repeat([]byte{7}, 32)
	key := Key{ContextID: strings.Repeat("a", 64), ID: "500000-" + strings.Repeat("b", 32), Material: base64.RawURLEncoding.EncodeToString(raw)}
	aad := HTTPAAD("public", key.ContextID, key.ID, "request-1234567890", "GET", "/api/v1/products?q=%E6%B8%85&x=1", 200, "application/json; charset=utf-8", "identity")
	want := []byte(`{"data":{"title":"清叶"}}`)
	plain := append(make([]byte, 0, len(want)+16), want...)
	sealed, nonce, err := Seal(key, plain, aad)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(raw)
	gcm, _ := cipher.NewGCM(block)
	n, _ := base64.RawURLEncoding.DecodeString(nonce)
	got, err := gcm.Open(nil, n, sealed, aad)
	if err != nil || !bytes.Equal(got, want) || len(sealed) != len(want)+16 {
		t.Fatalf("GCM envelope invalid: %v", err)
	}
	for _, bad := range [][]byte{bytes.Replace(aad, []byte("public"), []byte("admin"), 1), bytes.Replace(aad, []byte("identity"), []byte("gzip"), 1), append(append([]byte{}, aad...), byte('x'))} {
		if _, e := gcm.Open(nil, n, sealed, bad); e == nil {
			t.Fatal("changed context accepted")
		}
	}
	sealed[0] ^= 1
	if _, e := gcm.Open(nil, n, sealed, aad); e == nil {
		t.Fatal("changed body accepted")
	}
}

func TestResponseEnvelopeRandomNonceAndChatBinding(t *testing.T) {
	key := Key{ContextID: strings.Repeat("a", 64), ID: "500000-" + strings.Repeat("b", 32), Material: RandomKey()}
	a, err := SealChat(key, "public", "channel-one", []byte(`{"type":"ready"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := SealChat(key, "public", "channel-one", []byte(`{"type":"ready"}`))
	if err != nil {
		t.Fatal(err)
	}
	if a.Nonce == b.Nonce || a.FrameID == b.FrameID || a.Ciphertext == b.Ciphertext {
		t.Fatal("chat envelopes reused randomness")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(key.Material)
	block, _ := aes.NewCipher(raw)
	gcm, _ := cipher.NewGCM(block)
	n, _ := base64.RawURLEncoding.DecodeString(a.Nonce)
	body, _ := base64.RawURLEncoding.DecodeString(a.Ciphertext)
	if _, err = gcm.Open(nil, n, body, ChatAAD("public", key.ContextID, key.ID, "channel-two", a.FrameID)); err == nil {
		t.Fatal("cross connection frame accepted")
	}
}
