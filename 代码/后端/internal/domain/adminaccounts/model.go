package adminaccounts

import (
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/profile"
	"regexp"
	"strings"
)

var loginPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{5,19}$`)
var requestPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,100}$`)

type CreateInput struct {
	Login     string `json:"login"`
	Nickname  string `json:"nickname"`
	Password  string `json:"password"`
	AvatarID  string `json:"avatar_id"`
	RequestID string `json:"request_id"`
}
type UpdateInput struct {
	ExpectedVersion int64   `json:"expected_version"`
	Nickname        *string `json:"nickname"`
	AvatarID        *string `json:"avatar_id"`
	Enabled         *bool   `json:"enabled"`
}
type PasswordInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	Password        string `json:"password"`
	RequestID       string `json:"request_id"`
}

func ValidatePassword(password string) error {
	if len(password) < 12 || len(password) > 72 {
		return fault.New(fault.Invalid, "INVALID_PASSWORD", "密码需为12至72字节")
	}
	return nil
}
func ValidateRequest(id string) error {
	if !requestPattern.MatchString(id) {
		return fault.New(fault.Invalid, "INVALID_REQUEST", "请求标识需为8至100位字母、数字、下划线或短横线")
	}
	return nil
}
func ValidateCreate(in *CreateInput) error {
	in.Login = strings.TrimSpace(in.Login)
	in.Nickname = strings.TrimSpace(in.Nickname)
	if !loginPattern.MatchString(in.Login) {
		return fault.New(fault.Invalid, "INVALID_LOGIN", "登录账号需为6至20位字母、数字、点、下划线或短横线，且以字母或数字开头")
	}
	if !profile.ValidNickname(in.Nickname) {
		return fault.New(fault.Invalid, "INVALID_NICKNAME", "名称需为1至30个有效字符")
	}
	if len(in.AvatarID) > 100 || !profile.ValidText(in.AvatarID, 100) {
		return fault.New(fault.Invalid, "INVALID_AVATAR", "头像无效")
	}
	if err := ValidatePassword(in.Password); err != nil {
		return err
	}
	return ValidateRequest(in.RequestID)
}
func ValidateUpdate(actor, id string, in *UpdateInput) error {
	if in.ExpectedVersion < 1 {
		return fault.New(fault.Invalid, "INVALID_VERSION", "需提供当前管理员版本")
	}
	if in.Nickname == nil && in.AvatarID == nil && in.Enabled == nil {
		return fault.New(fault.Invalid, "INVALID_UPDATE", "请提供要修改的资料")
	}
	if in.Nickname != nil {
		v := strings.TrimSpace(*in.Nickname)
		in.Nickname = &v
		if !profile.ValidNickname(v) {
			return fault.New(fault.Invalid, "INVALID_NICKNAME", "名称需为1至30个有效字符")
		}
	}
	if in.AvatarID != nil && (len(*in.AvatarID) > 100 || !profile.ValidText(*in.AvatarID, 100)) {
		return fault.New(fault.Invalid, "INVALID_AVATAR", "头像无效")
	}
	if in.Enabled != nil && !*in.Enabled && actor == id {
		return fault.New(fault.Invalid, "SELF_DISABLE", "不能停用当前管理员")
	}
	return nil
}
func ValidateReset(in PasswordInput) error {
	if in.ExpectedVersion < 1 {
		return fault.New(fault.Invalid, "INVALID_VERSION", "需提供当前管理员版本")
	}
	if err := ValidatePassword(in.Password); err != nil {
		return err
	}
	return ValidateRequest(in.RequestID)
}
