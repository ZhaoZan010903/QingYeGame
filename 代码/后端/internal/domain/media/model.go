package media

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"time"
)

const MaxImagePixels int64 = 16000000
const ThumbnailEdge = 720

type Actor struct{ ID, Role, Surface string }
type CreateInput struct {
	Filename       string `json:"filename"`
	MIME           string `json:"mime_type"`
	Size           int64  `json:"byte_size"`
	Purpose        string `json:"purpose,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
}
type Upload struct {
	ID, Owner, Key, MIME, State, TokenHash string
	Size                                   int64
	Valid                                  bool
	Public                                 bool
	Purpose                                string
}
type Processed struct {
	Key, MIME string
	Size      int64
}
type Staged interface {
	Install() error
	Discard()
}
type File struct {
	Reader   io.ReadSeekCloser
	MIME     string
	Modified time.Time
}

// Mutation 只暴露当前媒介的状态写入，事务连接不离开持久化层。
type Mutation interface {
	Uploaded(context.Context) error
	Failed(context.Context) error
	Ready(context.Context, Processed) error
}

func ValidUploadName(name, mime string) bool {
	if name == "" || len(name) > 240 || strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch mime {
	case "image/jpeg":
		return ext == ".jpg" || ext == ".jpeg"
	case "image/png":
		return ext == ".png"
	case "image/webp":
		return ext == ".webp"
	}
	return false
}
