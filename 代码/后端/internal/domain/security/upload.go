package security

import (
	"qingye/backend/internal/domain/fault"
	"strings"
	"unicode/utf8"
)

type UploadRestriction struct {
	Blocked bool   `json:"blocked"`
	Version int64  `json:"version"`
	Reason  string `json:"reason"`
}
type RestrictInput struct {
	Blocked         bool   `json:"blocked"`
	ExpectedVersion int64  `json:"expected_version"`
	Reason          string `json:"reason"`
	PurgeStaging    bool   `json:"purge_staging"`
}

func ValidateRestriction(role string, in RestrictInput) error {
	if role != "admin" {
		return fault.New(fault.Forbidden, "FORBIDDEN", "需要管理员权限")
	}
	if in.ExpectedVersion < 0 || strings.TrimSpace(in.Reason) == "" || utf8.RuneCountInString(in.Reason) > 200 || in.PurgeStaging && !in.Blocked {
		return fault.New(fault.Invalid, "INVALID_RESTRICTION", "请填写处理原因；清理暂存图片必须同时停用上传")
	}
	return nil
}
