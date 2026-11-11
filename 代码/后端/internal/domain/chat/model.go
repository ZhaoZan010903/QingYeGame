// Package chat defines transport-independent chat inputs and identity boundaries.
package chat

import (
	"strings"
	"unicode/utf8"
)

const Capacity = 3

// Actor is produced by server-side session authentication, never bound from a payload.
type Actor struct{ ID, Role, Surface, SessionHash string }
type StartInput struct {
	ProductID string `json:"product_id"`
}
type SendInput struct {
	Body     string `json:"body"`
	ClientID string `json:"client_id"`
}
type ReadInput struct {
	Through string `json:"through"`
}
type PresenceInput struct {
	Accepting *bool `json:"accepting"`
}
type MessagePage struct {
	Before, After int64
	Forward       bool
}

func ValidID(value string, maxBytes int) bool {
	return len(value) > 0 && len(value) <= maxBytes && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}
