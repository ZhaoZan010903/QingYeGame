package chat

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestConversationCursorValidation(t *testing.T) {
	want := ConversationCursor{UpdatedAt: time.Date(2026, 11, 12, 1, 2, 3, 123456000, time.UTC), ID: "chat-中文", Position: 50}
	got, err := DecodeConversationCursor(EncodeConversationCursor(want))
	if err != nil || !got.UpdatedAt.Equal(want.UpdatedAt) || got.ID != want.ID || got.Position != 50 {
		t.Fatalf("roundtrip: %v %v", got, err)
	}
	for _, raw := range []string{"", "%%%", strings.Repeat("x", 513), base64.RawURLEncoding.EncodeToString([]byte(`{"at":"invalid","id":"x"}`)), EncodeConversationCursor(ConversationCursor{UpdatedAt: want.UpdatedAt, ID: "bad\x00id"}), EncodeConversationCursor(ConversationCursor{UpdatedAt: want.UpdatedAt, ID: "x", Position: -1}), EncodeConversationCursor(ConversationCursor{ID: "x"})} {
		if _, err := DecodeConversationCursor(raw); err == nil {
			t.Fatal("invalid cursor accepted")
		}
	}
}
