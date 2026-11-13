package registration

import (
	"qingye/backend/internal/domain/fault"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const Action = "sms_send"

var phonePattern = regexp.MustCompile(`^1[3-9][0-9]{9}$`)
var codePattern = regexp.MustCompile(`^[0-9]{6}$`)
var idPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var ErrChallenge = fault.New(fault.Invalid, "SMS_CHALLENGE_INVALID", "验证码凭据已失效，请重新获取")
var ErrBusy = fault.New(fault.Conflict, "SMS_BUSY", "请求正在处理，请稍候再试")
var ErrUnavailable = fault.New(fault.Unavailable, "SMS_UNAVAILABLE", "短信服务暂不可用，请稍后重试")
var ErrCode = fault.New(fault.Invalid, "SMS_CODE_INVALID", "验证码不正确或已过期")
var ErrLimited = fault.New(fault.RateLimited, "SMS_RATE_LIMITED", "获取次数较多，请稍后再试")

type Subject struct{ Binding, IPKey, PhoneKey, Hostname, IP string }
type Challenge struct {
	ID, Phone, Binding, Hostname, Stage, Attempt, RequestHash, UserID, BizID, ProviderRequestID string
	Expires                                                                                     time.Time
	Attempts                                                                                    int
}
type Agreement struct {
	Accepted bool `json:"accepted"`
	Terms    int  `json:"terms_version"`
	Privacy  int  `json:"privacy_version"`
}
type Input struct {
	Phone       string    `json:"phone"`
	Code        string    `json:"code"`
	ChallengeID string    `json:"challenge_id"`
	Password    string    `json:"password"`
	Agreement   Agreement `json:"agreement"`
}
type Receipt struct{ BizID, RequestID string }

func ValidPhone(phone string) bool { return phonePattern.MatchString(phone) }
func ValidID(id string) bool       { return idPattern.MatchString(id) }
func Validate(in Input) error {
	if !ValidPhone(in.Phone) || !ValidID(in.ChallengeID) || !codePattern.MatchString(in.Code) {
		return ErrCode
	}
	if !utf8.ValidString(in.Password) || utf8.RuneCountInString(in.Password) < 8 || len(in.Password) > 72 || strings.ContainsRune(in.Password, 0) {
		return fault.New(fault.Invalid, "INVALID_PASSWORD", "密码需为8位以上，且不超过72字节")
	}
	if !in.Agreement.Accepted || in.Agreement.Terms < 1 || in.Agreement.Privacy < 1 {
		return fault.New(fault.Invalid, "AGREEMENT_REQUIRED", "请阅读并同意当前服务协议与隐私说明")
	}
	return nil
}
