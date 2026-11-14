package management

import (
	"qingye/backend/internal/domain/fault"
	"regexp"
	"strings"
	"unicode/utf8"
)

const PurgeProductLimit = 1000
const PurgeImageLimit = 100000

type PurgeProductInput struct {
	ExpectedVersion int    `json:"expected_version"`
	Confirmation    string `json:"confirmation"`
	Reason          string `json:"reason"`
	RequestID       string `json:"request_id"`
}

type PurgeTrashInput struct {
	Snapshot     string `json:"snapshot"`
	Confirmation string `json:"confirmation"`
	Reason       string `json:"reason"`
	RequestID    string `json:"request_id"`
}

var purgeRequestPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,79}$`)
var purgeSnapshotPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validatePurgeConfirmation(confirmation, expected, reason, requestID string) error {
	if confirmation != expected {
		return fault.New(fault.Invalid, "INVALID_PURGE_CONFIRMATION", "请完整输入确认文字后继续")
	}
	if !purgeRequestPattern.MatchString(requestID) || utf8.RuneCountInString(reason) > 500 || strings.ContainsRune(reason, '\x00') {
		return fault.New(fault.Invalid, "INVALID_PURGE_INPUT", "请求标识无效或原因超过500字")
	}
	return nil
}

func ValidatePurgeProduct(id string, in PurgeProductInput) error {
	if id == "" || len(id) > 100 || strings.ContainsRune(id, '\x00') || in.ExpectedVersion < 1 {
		return fault.New(fault.Invalid, "INVALID_PURGE_INPUT", "商品标识或版本无效")
	}
	return validatePurgeConfirmation(in.Confirmation, "永久删除", in.Reason, in.RequestID)
}

func ValidatePurgeTrash(in PurgeTrashInput) error {
	if !purgeSnapshotPattern.MatchString(in.Snapshot) {
		return fault.New(fault.Invalid, "INVALID_PURGE_INPUT", "请先刷新回收站预览")
	}
	return validatePurgeConfirmation(in.Confirmation, "清空回收站", in.Reason, in.RequestID)
}
