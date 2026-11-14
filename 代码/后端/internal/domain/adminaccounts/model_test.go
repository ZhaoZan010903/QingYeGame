package adminaccounts

import (
	"qingye/backend/internal/domain/fault"
	"strings"
	"testing"
)

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	f, ok := fault.As(err)
	if !ok || f.Code != code {
		t.Fatalf("wanted %s got %v", code, err)
	}
}
func TestAdministratorCreationRules(t *testing.T) {
	for _, login := range []string{"19900001234", "legacy.admin", "Admin_1", "operator-1"} {
		in := CreateInput{Login: login, Nickname: strings.Repeat("叶", 30), Password: "password-1234", RequestID: "request-1234"}
		if err := ValidateCreate(&in); err != nil {
			t.Fatal(err)
		}
	}
	for _, login := range []string{"admin", "管理员账号", "x@y.example", "-admin1", strings.Repeat("a", 21), "admin\x00"} {
		in := CreateInput{Login: login, Nickname: "管理员", Password: "password-1234", RequestID: "request-1234"}
		wantCode(t, ValidateCreate(&in), "INVALID_LOGIN")
	}
}
func TestAdministratorPasswordsUseBcryptByteBounds(t *testing.T) {
	for _, p := range []string{strings.Repeat("a", 12), strings.Repeat("叶", 24)} {
		if err := ValidatePassword(p); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{strings.Repeat("a", 11), strings.Repeat("叶", 25)} {
		wantCode(t, ValidatePassword(p), "INVALID_PASSWORD")
	}
}
func TestAdministratorPartialUpdateAndSelfProtection(t *testing.T) {
	no := false
	clear := ""
	name := " 管理员 "
	in := UpdateInput{ExpectedVersion: 1, Enabled: &no}
	wantCode(t, ValidateUpdate("same", "same", &in), "SELF_DISABLE")
	if err := ValidateUpdate("actor", "target", &in); err != nil {
		t.Fatal(err)
	}
	in = UpdateInput{ExpectedVersion: 1, AvatarID: &clear, Nickname: &name}
	if err := ValidateUpdate("same", "same", &in); err != nil || *in.Nickname != "管理员" {
		t.Fatalf("clear/name: %+v %v", in, err)
	}
	wantCode(t, ValidateUpdate("actor", "target", &UpdateInput{ExpectedVersion: 1}), "INVALID_UPDATE")
	wantCode(t, ValidateUpdate("actor", "target", &UpdateInput{Enabled: &no}), "INVALID_VERSION")
}
