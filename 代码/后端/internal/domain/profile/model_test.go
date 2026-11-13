package profile

import (
	"strings"
	"testing"
)

func TestNicknameValidation(t *testing.T) {
	for _, value := range []string{"", "  ", "昵称\n伪造", "昵称\x00", string([]byte{0xff}), strings.Repeat("叶", 31)} {
		if ValidNickname(value) {
			t.Fatalf("accepted invalid nickname %q", value)
		}
	}
	for _, value := range []string{"清叶会员1234", "一叶知秋🌱", strings.Repeat("叶", 30)} {
		if !ValidNickname(value) {
			t.Fatalf("rejected valid nickname %q", value)
		}
	}
}
