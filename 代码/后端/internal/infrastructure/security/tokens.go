// Package security contains cryptographic primitives, not authorization rules.
package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

func TokenHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func RandomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
