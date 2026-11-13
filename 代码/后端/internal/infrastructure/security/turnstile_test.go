package security

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type turnstileTransport func(*http.Request) (*http.Response, error)

func (f turnstileTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTurnstileStrictSiteverify(t *testing.T) {
	for _, name := range []string{"valid", "unsuccessful", "wrong-action", "wrong-host", "wrong-nonce", "expired", "future", "oversized", "malformed", "timeout", "server-error"} {
		t.Run(name, func(t *testing.T) {
			result := map[string]any{"success": true, "action": "media_read", "hostname": "qingye.test", "cdata": "nonce", "challenge_ts": time.Now().UTC().Format(time.RFC3339)}
			switch name {
			case "unsuccessful":
				result["success"] = false
			case "wrong-action":
				result["action"] = "sms_send"
			case "wrong-host":
				result["hostname"] = "other.test"
			case "wrong-nonce":
				result["cdata"] = "other"
			case "expired":
				result["challenge_ts"] = time.Now().Add(-301 * time.Second)
			case "future":
				result["challenge_ts"] = time.Now().Add(time.Minute)
			}
			v := NewTurnstile("do-not-log-secret")
			v.Client.Transport = turnstileTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != "https://challenges.cloudflare.com/turnstile/v0/siteverify" || r.Method != "POST" {
					t.Fatal("unexpected provider destination")
				}
				raw, _ := io.ReadAll(r.Body)
				form, _ := url.ParseQuery(string(raw))
				if form.Get("secret") != v.Secret || form.Get("response") != "sensitive-token" || form.Get("idempotency_key") != "nonce" || form.Get("remoteip") != "192.0.2.1" {
					t.Fatal("provider request lost binding")
				}
				if name == "timeout" {
					return nil, errors.New("sensitive-token do-not-log-secret")
				}
				body, _ := json.Marshal(result)
				status := 200
				switch name {
				case "oversized":
					body = []byte(strings.Repeat("x", 16385))
				case "malformed":
					body = []byte("<html>error</html>")
				case "server-error":
					status = 500
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
			})
			ok, err := v.Verify(context.Background(), "sensitive-token", "media_read", "qingye.test", "nonce", "192.0.2.1")
			if ok != (name == "valid") {
				t.Fatalf("unexpected decision %v %v", ok, err)
			}
			if err != nil && (strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "sensitive-token")) {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestTurnstileBoundedCallsAndNoRedirect(t *testing.T) {
	for range cap(turnstileCalls) {
		turnstileCalls <- struct{}{}
	}
	v := NewTurnstile("private-secret")
	ok, err := v.Verify(context.Background(), "x", "media_read", "host", "nonce", "")
	for range cap(turnstileCalls) {
		<-turnstileCalls
	}
	if ok || err == nil {
		t.Fatal("unbounded external calls")
	}
	if v.Client.Timeout > 10*time.Second || v.Client.CheckRedirect(&http.Request{}, nil) == nil {
		t.Fatal("provider timeout/redirect boundary missing")
	}
}
