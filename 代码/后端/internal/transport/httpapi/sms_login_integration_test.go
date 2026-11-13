package httpapi

import (
	"context"
	"encoding/json"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	domain "qingye/backend/internal/domain/registration"
	"sync"
	"sync/atomic"
	"testing"
)

func smsLoginFixture(t *testing.T) (*App, *registrationFakeProvider, domain.Subject) {
	a, _, who := registrationFixture(t)
	p := &registrationFakeProvider{}
	a.Config.Registration.LoginEnabled = true
	a.Registration.LoginProvider = p
	return a, p, who
}
func loginProof(t *testing.T, a *App, who domain.Subject) domain.Input {
	t.Helper()
	c, err := a.Registration.CreatePurpose(context.Background(), who, "13800000000", "login")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Registration.SendPurpose(context.Background(), who, c.Phone, c.ID, "verified-human", "login"); err != nil {
		t.Fatal(err)
	}
	in := registrationInput(c.ID)
	in.Purpose = "login"
	in.PasswordConfirm = in.Password
	return in
}
func addSMSMember(t *testing.T, a *App, role string, enabled bool) {
	t.Helper()
	_, err := a.DB.Exec(context.Background(), `INSERT INTO users(id,phone,nickname,password_hash,role,enabled) VALUES('sms-user','13800000000','会员','unchanged-password-hash',$1,$2)`, role, enabled)
	if err != nil {
		t.Fatal(err)
	}
}
func TestR21SMSLoginExistingMemberPreservesAccount(t *testing.T) {
	a, p, who := smsLoginFixture(t)
	addSMSMember(t, a, "member", true)
	in := loginProof(t, a, who)
	first, pending, err := a.Registration.Login(context.Background(), who, in)
	if err != nil || pending || first.User.ID != "sms-user" || first.Token == "" {
		t.Fatal("login failed", err)
	}
	second, pending, err := a.Registration.Login(context.Background(), who, in)
	if err != nil || pending || second.User.ID != first.User.ID || p.checked.Load() != 1 {
		t.Fatal("lost response retry", err)
	}
	var password string
	var consents int
	a.DB.QueryRow(context.Background(), `SELECT password_hash,(SELECT count(*) FROM registration_consents) FROM users WHERE id='sms-user'`).Scan(&password, &consents)
	if password != "unchanged-password-hash" || consents != 0 {
		t.Fatal("existing account mutated")
	}
	if _, err = a.Registration.Register(context.Background(), who, in); err == nil {
		t.Fatal("existing account password replaced")
	}
}
func TestR21SMSLoginNewPhoneRequiresExplicitRegistration(t *testing.T) {
	a, p, who := smsLoginFixture(t)
	in := loginProof(t, a, who)
	ctx := context.Background()
	if _, err := a.Registration.Register(ctx, who, in); err == nil {
		t.Fatal("registration before verification")
	}
	result, pending, err := a.Registration.Login(ctx, who, in)
	if err != nil || !pending || result.Token != "" {
		t.Fatal("new phone did not wait", err)
	}
	var users, sessions int
	a.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM users),(SELECT count(*) FROM sessions)`).Scan(&users, &sessions)
	if users != 0 || sessions != 0 {
		t.Fatal("implicit registration")
	}
	bad := in
	bad.PasswordConfirm = "different-password"
	if _, err = a.Registration.Register(ctx, who, bad); err == nil {
		t.Fatal("mismatched passwords")
	}
	bad = in
	bad.Agreement.Accepted = false
	if _, err = a.Registration.Register(ctx, who, bad); err == nil {
		t.Fatal("missing consent")
	}
	created, err := a.Registration.Register(ctx, who, in)
	if err != nil || created.Token == "" || created.User.Role != "member" {
		t.Fatal("registration failed", err)
	}
	if p.checked.Load() != 1 {
		t.Fatal("code reverified during registration")
	}
	var hash string
	if err = a.DB.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1`, created.User.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		t.Fatal("password hash invalid")
	}
	if _, err = a.Registration.Register(ctx, who, in); err != nil {
		t.Fatal("lost registration response", err)
	}
	bad = in
	bad.Password = "changed-password"
	bad.PasswordConfirm = bad.Password
	if _, err = a.Registration.Register(ctx, who, bad); err == nil {
		t.Fatal("completion replay changed password")
	}
}
func TestR21SMSLoginRejectsOtherPurposeBrowserCodeAndExpiredProof(t *testing.T) {
	for _, kind := range []string{"register-proof", "wrong-browser", "wrong-code", "expired", "no-code"} {
		t.Run(kind, func(t *testing.T) {
			a, p, who := smsLoginFixture(t)
			in := loginProof(t, a, who)
			ctx := context.Background()
			if kind == "register-proof" {
				_, e := a.DB.Exec(ctx, `UPDATE sms_challenges SET purpose='register' WHERE id=$1`, in.ChallengeID)
				if e != nil {
					t.Fatal(e)
				}
			}
			if kind == "wrong-browser" {
				who.Binding = "other"
			}
			if kind == "wrong-code" {
				p.wrong = true
			}
			if kind == "expired" {
				_, e := a.DB.Exec(ctx, `UPDATE sms_challenges SET expires_at=now()-interval '1 second' WHERE id=$1`, in.ChallengeID)
				if e != nil {
					t.Fatal(e)
				}
			}
			if kind == "no-code" {
				in.Code = ""
			}
			if _, pending, e := a.Registration.Login(ctx, who, in); e == nil || pending {
				t.Fatal("invalid proof accepted")
			}
		})
	}
}
func TestR21SMSLoginRejectsAdminAndDisabledAccounts(t *testing.T) {
	for _, role := range []string{"admin", "member"} {
		t.Run(role, func(t *testing.T) {
			a, _, who := smsLoginFixture(t)
			addSMSMember(t, a, role, role == "admin")
			in := loginProof(t, a, who)
			if _, pending, e := a.Registration.Login(context.Background(), who, in); e == nil || pending {
				t.Fatal("privileged/disabled account bypass")
			}
			var n int
			a.DB.QueryRow(context.Background(), `SELECT count(*) FROM sessions`).Scan(&n)
			if n != 0 {
				t.Fatal("session granted")
			}
		})
	}
}
func TestR21SMSLoginProofRecheckedAtCompletion(t *testing.T) {
	for _, kind := range []string{"browser", "code", "purpose", "expired", "terms", "race-account"} {
		t.Run(kind, func(t *testing.T) {
			a, _, who := smsLoginFixture(t)
			in := loginProof(t, a, who)
			ctx := context.Background()
			if _, pending, e := a.Registration.Login(ctx, who, in); e != nil || !pending {
				t.Fatal(e)
			}
			switch kind {
			case "browser":
				who.Binding = "other"
			case "code":
				in.Code = "654321"
			case "purpose":
				in.Purpose = "register"
			case "expired":
				if _, e := a.DB.Exec(ctx, `UPDATE sms_challenges SET expires_at=now()-interval '1 second' WHERE id=$1`, in.ChallengeID); e != nil {
					t.Fatal(e)
				}
			case "terms":
				if _, e := a.DB.Exec(ctx, `UPDATE content_pages SET version=2 WHERE slug='terms'`); e != nil {
					t.Fatal(e)
				}
			case "race-account":
				addSMSMember(t, a, "member", true)
			}
			if _, e := a.Registration.Register(ctx, who, in); e == nil {
				t.Fatal("completion bypass")
			}
			var n int
			a.DB.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&n)
			if n != 0 {
				t.Fatal("partial session")
			}
			if kind == "terms" {
				in.Agreement.Terms = 2
				if _, e := a.Registration.Register(ctx, who, in); e != nil {
					t.Fatal("cannot accept updated terms", e)
				}
			}
		})
	}
}
func TestR21SMSLoginConcurrentCheckAndCompletion(t *testing.T) {
	a, p, who := smsLoginFixture(t)
	in := loginProof(t, a, who)
	ctx := context.Background()
	var wg sync.WaitGroup
	var accepted atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, pending, e := a.Registration.Login(ctx, who, in); e == nil && pending {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() < 1 || p.checked.Load() != 1 {
		t.Fatal("verification race")
	}
	accepted.Store(0)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := a.Registration.Register(ctx, who, in); e == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	var n int
	a.DB.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	if n != 1 || accepted.Load() < 1 {
		t.Fatal("duplicate account")
	}
}
func TestR21SMSLoginHTTPFlowAndGates(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[existing], func(t *testing.T) {
			a, _, _ := smsLoginFixture(t)
			if existing {
				addSMSMember(t, a, "member", true)
			}
			h := memberHTTP{t: t, handler: a.Routes(), ip: "192.0.2.85"}
			r := h.call("POST", "/api/v1/auth/sms/challenges", map[string]any{"phone": "13800000000", "purpose": "login"}, nil, 201)
			var env struct {
				Data struct {
					ID string `json:"challenge_id"`
				}
			}
			if e := json.Unmarshal(r.Body.Bytes(), &env); e != nil {
				t.Fatal(e)
			}
			var binding *http.Cookie
			for _, c := range r.Result().Cookies() {
				if c.Name == "qingye_registration" {
					binding = c
				}
			}
			if binding == nil {
				t.Fatal("missing binding")
			}
			send := map[string]any{"phone": "13800000000", "purpose": "login", "challenge_id": env.Data.ID, "token": "verified-human"}
			h.call("POST", "/api/v1/auth/sms", send, binding, 200)
			in := map[string]any{"method": "sms", "phone": "13800000000", "code": "123456", "challenge_id": env.Data.ID}
			h.call("POST", "/api/v1/auth/login", in, nil, 422)
			bad := h
			bad.origin = "https://evil.example"
			bad.call("POST", "/api/v1/auth/login", in, binding, 403)
			r = h.call("POST", "/api/v1/auth/login", in, binding, 200)
			var result struct {
				Data struct {
					Status string `json:"status"`
				}
			}
			json.Unmarshal(r.Body.Bytes(), &result)
			if !existing {
				if result.Data.Status != "registration_required" || len(r.Result().Cookies()) != 0 {
					t.Fatal("implicit auth")
				}
				a.Config.Registration.Enabled = false
				h.call("POST", "/api/v1/auth/login", in, binding, 503)
				complete := registrationInput(env.Data.ID)
				complete.Purpose = "login"
				complete.PasswordConfirm = complete.Password
				h.call("POST", "/api/v1/auth/register", complete, binding, 503)
				a.Config.Registration.Enabled = true
				r = h.call("POST", "/api/v1/auth/register", complete, binding, 200)
			} else if result.Data.Status != "authenticated" {
				t.Fatal("existing member not authenticated")
			}
			var session *http.Cookie
			for _, c := range r.Result().Cookies() {
				if c.Name == sessionCookie {
					session = c
				}
			}
			if session == nil || !session.HttpOnly {
				t.Fatal("missing public session")
			}
			h.call("GET", "/api/v1/me", nil, session, 200)
			a.Config.Registration.LoginEnabled = false
			h.call("POST", "/api/v1/auth/login", in, binding, 503)
			a.Config.Surface = "admin"
			h.call("POST", "/api/v1/auth/login", in, binding, 503)
		})
	}
}
