package security

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Turnstile struct {
	Secret string
	Client *http.Client
}

// 供应商故障时也不允许无限堆积外部验证请求。
var turnstileCalls = make(chan struct{}, 8)

func NewTurnstile(secret string) *Turnstile {
	return &Turnstile{Secret: secret, Client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect forbidden") }}}
}
func (v *Turnstile) Verify(ctx context.Context, token, action, hostname, nonce, ip string) (bool, error) {
	select {
	case turnstileCalls <- struct{}{}:
		defer func() { <-turnstileCalls }()
	default:
		return false, errors.New("verification capacity unavailable")
	}
	form := url.Values{"secret": {v.Secret}, "response": {token}, "idempotency_key": {nonce}}
	if ip != "" {
		form.Set("remoteip", ip)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://challenges.cloudflare.com/turnstile/v0/siteverify", strings.NewReader(form.Encode()))
	if err != nil {
		return false, errors.New("verification unavailable")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := v.Client.Do(req)
	if err != nil {
		return false, errors.New("verification unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return false, errors.New("verification unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 16385))
	if err != nil || len(body) > 16384 {
		return false, errors.New("invalid verification response")
	}
	var result struct {
		Success     bool      `json:"success"`
		Action      string    `json:"action"`
		Hostname    string    `json:"hostname"`
		CData       string    `json:"cdata"`
		ChallengeTS time.Time `json:"challenge_ts"`
	}
	if json.Unmarshal(body, &result) != nil {
		return false, errors.New("invalid verification response")
	}
	age := time.Since(result.ChallengeTS)
	return result.Success && result.Action == action && result.Hostname == hostname && result.CData == nonce && age >= -30*time.Second && age <= 5*time.Minute, nil
}
