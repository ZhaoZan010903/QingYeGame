// Package responsecrypto implements the response envelope, not business authorization.
package responsecrypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

const Protocol = "qy-r24-aesgcm-v1"
const ContentType = "application/vnd.qingye.encrypted"
const RotationSeconds int64 = 3600
const RetentionSeconds int64 = 120

var ErrKey = errors.New("response key unavailable or expired")
var ErrCapacity = errors.New("response key capacity reached")

type Key struct {
	ContextID   string `json:"context_id"`
	ID          string `json:"key_id"`
	Material    string `json:"key"`
	RotateAt    int64  `json:"rotate_at"`
	RetainUntil int64  `json:"retain_until"`
}

type Store interface {
	Current(context.Context, string) (Key, error)
	Lookup(context.Context, string, string) (Key, error)
}

func ValidateKey(key Key) error {
	raw, err := base64.RawURLEncoding.DecodeString(key.Material)
	if err != nil || len(raw) != 32 || !ValidID(key.ContextID, 64, 64) || !ValidID(key.ID, 34, 64) {
		return ErrKey
	}
	return nil
}

func RandomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("secure random unavailable")
	}
	return hex.EncodeToString(b[:])
}

func RandomKey() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("secure random unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func ValidID(s string, min, max int) bool {
	if len(s) < min || len(s) > max {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func HTTPAAD(surface, contextID, keyID, requestID, method, target string, status int, contentType, contentEncoding string) []byte {
	return []byte(strings.Join([]string{"QY-R24-HTTP", "1", surface, contextID, keyID, requestID, method, target, strconv.Itoa(status), contentType, contentEncoding}, "\n"))
}

func ChatAAD(surface, contextID, keyID, channelID, frameID string) []byte {
	return []byte(strings.Join([]string{"QY-R24-CHAT", "1", surface, contextID, keyID, channelID, frameID}, "\n"))
}

// Seal reuses the bounded response buffer. The final 16 bytes are the GCM tag.
func Seal(key Key, plaintext, aad []byte) ([]byte, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(key.Material)
	if err != nil || len(raw) != 32 {
		return nil, "", ErrKey
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, "", err
	}
	var nonce [12]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, "", err
	}
	return aead.Seal(plaintext[:0], nonce[:], plaintext, aad), base64.RawURLEncoding.EncodeToString(nonce[:]), nil
}

type ChatEnvelope struct {
	Type       string `json:"type"`
	Version    int    `json:"v"`
	ContextID  string `json:"context_id"`
	KeyID      string `json:"key_id"`
	ChannelID  string `json:"channel_id"`
	FrameID    string `json:"frame_id"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func SealChat(key Key, surface, channelID string, plaintext []byte) (ChatEnvelope, error) {
	id := RandomID()
	sealed, nonce, err := Seal(key, plaintext, ChatAAD(surface, key.ContextID, key.ID, channelID, id))
	if err != nil {
		return ChatEnvelope{}, err
	}
	return ChatEnvelope{Type: "encrypted", Version: 1, ContextID: key.ContextID, KeyID: key.ID, ChannelID: channelID, FrameID: id, Nonce: nonce, Ciphertext: base64.RawURLEncoding.EncodeToString(sealed)}, nil
}
