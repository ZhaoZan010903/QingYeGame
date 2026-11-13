package security

import (
	"errors"
	"time"
)

const MediaReadAction = "media_read"

// SMS发送将来使用独立场景；图片通行状态不能授权短信请求。
const SMSSendAction = "sms_send"

var ErrChallenge = errors.New("verification challenge invalid or consumed")
var ErrBusy = errors.New("verification already in progress")
var ErrUnavailable = errors.New("media protection unavailable")

type ReadPolicy struct {
	Burst          int
	PerSecond      float64
	HardBurst      int
	HardPerSecond  float64
	IPBurst        int
	IPPerSecond    float64
	ByteBurst      int64
	BytesPerSecond int64
	MaxSubjects    int
}

func DefaultReadPolicy() ReadPolicy {
	return ReadPolicy{120, 2, 600, 10, 1200, 20, 128 << 20, 2 << 20, 10000}
}

type ReadSubject struct {
	Key     string
	Binding string
	IPKey   string
	Surface string
	Kind    string
}
type ReadDecision struct {
	State      string `json:"state"`
	Code       string `json:"code,omitempty"`
	RetryAfter int    `json:"retry_after,omitempty"`
	Until      int64  `json:"until,omitempty"`
}

func AllowedRead() ReadDecision { return ReadDecision{State: "allowed"} }

type ReadRecord struct {
	Subject   string `json:"subject"`
	Surface   string `json:"surface"`
	Kind      string `json:"kind"`
	Reason    string `json:"reason"`
	Until     int64  `json:"until"`
	Version   string `json:"version"`
	Requests  int64  `json:"requests"`
	BytesSent int64  `json:"bytes_sent"`
}
type HumanChallenge struct {
	ID        string    `json:"id"`
	Action    string    `json:"action"`
	Hostname  string    `json:"-"`
	Binding   string    `json:"-"`
	Subject   string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	Attempt   string    `json:"-"`
}
