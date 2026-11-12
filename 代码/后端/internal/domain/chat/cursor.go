package chat

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

type ConversationCursor struct {
	UpdatedAt time.Time `json:"at"`
	ID        string    `json:"id"`
	Position  int       `json:"position"`
}

type ConversationPage struct {
	Offset int
	Cursor *ConversationCursor
}

func EncodeConversationCursor(cursor ConversationCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func DecodeConversationCursor(raw string) (*ConversationCursor, error) {
	invalid := errors.New("invalid conversation cursor")
	if len(raw) == 0 || len(raw) > 512 {
		return nil, invalid
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return nil, invalid
	}
	var cursor ConversationCursor
	if json.Unmarshal(decoded, &cursor) != nil || !ValidID(cursor.ID, 100) || cursor.UpdatedAt.IsZero() || cursor.UpdatedAt.Year() < 1 || cursor.UpdatedAt.Year() > 9999 || cursor.Position < 0 || cursor.Position > 1000000 {
		return nil, invalid
	}
	return &cursor, nil
}
