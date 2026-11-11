package security

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestProductCredentialEnvelope(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	data := []byte("sensitive-test-password")
	a, e := SealCredentials(key, "product-a", data)
	if e != nil {
		t.Fatal(e)
	}
	b, e := SealCredentials(key, "product-a", data)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Equal(a, b) || bytes.Contains(a, data) {
		t.Fatal("random nonce/encryption missing")
	}
	out, e := OpenCredentials(key, "product-a", a)
	if e != nil || !bytes.Equal(out, data) {
		t.Fatal("roundtrip failed")
	}
	if _, e = OpenCredentials(key, "product-b", a); e == nil {
		t.Fatal("ciphertext accepted for another product")
	}
	a[len(a)-1] ^= 1
	if _, e = OpenCredentials(key, "product-a", a); e == nil {
		t.Fatal("tampering accepted")
	}
	if _, e = CredentialKey("short"); e == nil {
		t.Fatal("invalid key accepted")
	}
}
