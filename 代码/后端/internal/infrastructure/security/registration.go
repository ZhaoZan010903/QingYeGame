package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

func RegistrationMAC(secret string) func(string) string {
	return func(value string) string {
		m := hmac.New(sha256.New, []byte(secret))
		m.Write([]byte("registration-request-v1:" + value))
		return hex.EncodeToString(m.Sum(nil))
	}
}
