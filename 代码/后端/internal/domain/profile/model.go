package profile

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type ReviewInput struct {
	Decision string `json:"decision"`
	Note     string `json:"note"`
}
type AvatarInput struct {
	Name      string `json:"name"`
	MediaID   string `json:"media_id"`
	SortOrder int    `json:"sort_order"`
	Enabled   *bool  `json:"enabled"`
	Version   int    `json:"version"`
}

func ValidText(value string, max int) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > max {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func ValidNickname(value string) bool {
	return strings.TrimSpace(value) != "" && ValidText(value, 30)
}
