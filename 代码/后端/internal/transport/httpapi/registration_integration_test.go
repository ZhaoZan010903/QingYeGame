package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	app "qingye/backend/internal/application/registration"
	domain "qingye/backend/internal/domain/registration"
	"qingye/backend/internal/infrastructure/cache"
	"qingye/backend/internal/infrastructure/postgres"
	"qingye/backend/internal/infrastructure/security"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type registrationFakeProvider struct {
	sent      atomic.Int32
	checked   atomic.Int32
	uncertain bool
	wrong     bool
}

func (p *registrationFakeProvider) Send(context.Context, string, string) (domain.Receipt, error) {
	p.sent.Add(1)
	if p.uncertain {
		return domain.Receipt{}, errors.New("uncertain")
	}
	return domain.Receipt{BizID: "fake-biz", RequestID: "fake-request"}, nil
}
func (p *registrationFakeProvider) Check(context.Context, string, string, string) (bool, error) {
	p.checked.Add(1)
	return !p.wrong, nil
}

type registrationHuman struct {
	pass bool
	t    *testing.T
}

func (h registrationHuman) Verify(_ context.Context, token, action, host, nonce, ip string) (bool, error) {
	if action != "sms_send" || host != "qingye.test" || !domain.ValidID(nonce) {
		h.t.Error("incorrect verification binding")
	}
	return h.pass && token == "verified-human", nil
}
func registrationFixture(t *testing.T) (*App, *registrationFakeProvider, domain.Subject) {
	a := integrationMemberApp(t)
	a.Config.SessionSecret = strings.Repeat("test-secret-", 4)
	a.Config.MediaSafety.Hostnames = []string{"qingye.test"}
	a.Config.MediaSafety.SiteKey = "test-only"
	a.Config.Registration.Enabled = true
	p := &registrationFakeProvider{}
	prefix := newID() + ":"
	a.Registration = &app.Service{Store: postgres.RegistrationStore{DB: a.DB}, Provider: p, Human: registrationHuman{true, t}, Limits: cache.RegistrationLimiter{Client: a.Redis, DailyLimit: 20, Prefix: prefix}, NewID: newID, NewToken: randomToken, Hash: tokenHash, MAC: security.RegistrationMAC(a.Config.SessionSecret), PasswordHash: func(_ context.Context, p string) (string, error) {
		b, e := bcrypt.GenerateFromPassword([]byte(p), 4)
		return string(b), e
	}, AllowedPhones: []string{"13800000000", "13800000001"}}
	t.Cleanup(func() {
		keys, _ := a.Redis.Keys(context.Background(), "qingye:r21:"+prefix+"*").Result()
		if len(keys) > 0 {
			a.Redis.Del(context.Background(), keys...)
		}
	})
	_, e := a.DB.Exec(context.Background(), `INSERT INTO content_pages(slug,title,body,published,version) VALUES('terms','terms','terms',true,1),('privacy','privacy','privacy',true,1) ON CONFLICT(slug) DO UPDATE SET published=true,version=1`)
	if e != nil {
		t.Fatal(e)
	}
	return a, p, domain.Subject{Binding: "browser", IPKey: "ip", PhoneKey: "phone", Hostname: "qingye.test", IP: "192.0.2.77"}
}
func registrationInput(id string) domain.Input {
	return domain.Input{Phone: "13800000000", Code: "123456", ChallengeID: id, Password: "test-password-21", Agreement: domain.Agreement{Accepted: true, Terms: 1, Privacy: 1}}
}
func issueRegistration(t *testing.T, a *App, s domain.Subject) string {
	t.Helper()
	c, e := a.Registration.Create(context.Background(), s, "13800000000")
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Registration.Send(context.Background(), s, "13800000000", c.ID, "verified-human"); e != nil {
		t.Fatal(e)
	}
	return c.ID
}
func TestR21RegistrationAtomicAccountAndRetry(t *testing.T) {
	a, p, s := registrationFixture(t)
	id := issueRegistration(t, a, s)
	in := registrationInput(id)
	ctx := context.Background()
	session, e := a.Registration.Register(ctx, s, in)
	if e != nil {
		t.Fatal(e)
	}
	if session.User.Role != "member" || !session.User.Enabled {
		t.Fatal("wrong role")
	}
	again, e := a.Registration.Register(ctx, s, in)
	if e != nil || again.User.ID != session.User.ID || again.Token == session.Token {
		t.Fatal("lost response recovery", e)
	}
	if p.checked.Load() != 1 || p.sent.Load() != 1 {
		t.Fatal("replayed provider effect")
	}
	var users, consents, sessions int
	a.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM users WHERE phone='13800000000'),(SELECT count(*) FROM registration_consents),(SELECT count(*) FROM sessions)`).Scan(&users, &consents, &sessions)
	if users != 1 || consents != 1 || sessions != 2 {
		t.Fatal(users, consents, sessions)
	}
	in.Password = "changed-password"
	if _, e = a.Registration.Register(ctx, s, in); e == nil {
		t.Fatal("changed payload replay")
	}
	in = registrationInput(id)
	other := s
	other.Binding = "other"
	if _, e = a.Registration.Register(ctx, other, in); e == nil {
		t.Fatal("cross browser")
	}
	_, e = a.DB.Exec(ctx, `UPDATE users SET enabled=false WHERE id=$1`, session.User.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Registration.Register(ctx, s, in); e == nil {
		t.Fatal("disabled user replay")
	}
}
func TestR21RegistrationParallelSendAndUnknown(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "unknown"}[uncertain], func(t *testing.T) {
			a, p, s := registrationFixture(t)
			p.uncertain = uncertain
			c, e := a.Registration.Create(context.Background(), s, "13800000000")
			if e != nil {
				t.Fatal(e)
			}
			var wg sync.WaitGroup
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_ = a.Registration.Send(context.Background(), s, "13800000000", c.ID, "verified-human")
				}()
			}
			wg.Wait()
			if p.sent.Load() != 1 {
				t.Fatal("duplicate paid send", p.sent.Load())
			}
			if uncertain {
				if _, e = a.Registration.Register(context.Background(), s, registrationInput(c.ID)); e == nil {
					t.Fatal("unknown send accepted")
				}
			}
		})
	}
}
func TestR21RegistrationFailedVerificationAndCodeBudget(t *testing.T) {
	a, p, s := registrationFixture(t)
	a.Registration.Human = registrationHuman{false, t}
	c, e := a.Registration.Create(context.Background(), s, "13800000000")
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Registration.Send(context.Background(), s, "13800000000", c.ID, "verified-human"); e == nil || p.sent.Load() != 0 {
		t.Fatal("human gate bypass")
	}
	a.Registration.Human = registrationHuman{true, t}
	id := issueRegistration(t, a, s)
	p.wrong = true
	for i := 0; i < 7; i++ {
		if _, e = a.Registration.Register(context.Background(), s, registrationInput(id)); e == nil {
			t.Fatal("bad code accepted")
		}
	}
	if p.checked.Load() != 5 {
		t.Fatal("unbounded attempts", p.checked.Load())
	}
}
func TestR21RegistrationExistingAdminAndTermsFailClosed(t *testing.T) {
	for _, scenario := range []string{"admin", "terms", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			a, _, s := registrationFixture(t)
			id := issueRegistration(t, a, s)
			ctx := context.Background()
			var e error
			switch scenario {
			case "admin":
				_, e = a.DB.Exec(ctx, `INSERT INTO users(id,phone,nickname,password_hash,role) VALUES('admin','13800000000','admin','original-hash','admin')`)
			case "terms":
				_, e = a.DB.Exec(ctx, `UPDATE content_pages SET version=2 WHERE slug='terms'`)
			case "expired":
				_, e = a.DB.Exec(ctx, `UPDATE sms_challenges SET expires_at=now()-interval '1 second' WHERE id=$1`, id)
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e = a.Registration.Register(ctx, s, registrationInput(id)); e == nil {
				t.Fatal("unsafe registration")
			}
			var n int
			a.DB.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&n)
			if n != 0 {
				t.Fatal("partial session")
			}
			if scenario == "admin" {
				var hash, role string
				a.DB.QueryRow(ctx, `SELECT password_hash,role FROM users WHERE id='admin'`).Scan(&hash, &role)
				if hash != "original-hash" || role != "admin" {
					t.Fatal("admin overwritten")
				}
			}
		})
	}
}
func TestR21RegistrationHTTPBindingsAndSurface(t *testing.T) {
	a, p, _ := registrationFixture(t)
	h := memberHTTP{t: t, handler: a.Routes(), ip: "192.0.2.80"}
	r := h.call("POST", "/api/v1/auth/sms/challenges", map[string]any{"phone": "13800000000", "purpose": "register"}, nil, 201)
	var envelope struct {
		Data struct {
			ID string `json:"challenge_id"`
		}
	}
	if e := json.Unmarshal(r.Body.Bytes(), &envelope); e != nil {
		t.Fatal(e)
	}
	var cookie *http.Cookie
	for _, c := range r.Result().Cookies() {
		if c.Name == "qingye_registration" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly {
		t.Fatal("missing browser binding")
	}
	send := map[string]any{"phone": "13800000000", "purpose": "register", "challenge_id": envelope.Data.ID, "token": "verified-human"}
	h.call("POST", "/api/v1/auth/sms", send, nil, 422)
	bad := h
	bad.origin = "https://untrusted.example"
	bad.call("POST", "/api/v1/auth/sms", send, cookie, 403)
	h.call("POST", "/api/v1/auth/sms", send, cookie, 200)
	r = h.call("POST", "/api/v1/auth/register", registrationInput(envelope.Data.ID), cookie, 200)
	var sessionCookie *http.Cookie
	for _, c := range r.Result().Cookies() {
		if c.Name == "qingye_session" {
			sessionCookie = c
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly || len(sessionCookie.Value) != 64 {
		t.Fatal("no session")
	}
	h.call("GET", "/api/v1/me", nil, sessionCookie, 200)
	h.call("POST", "/api/v1/auth/logout", map[string]any{}, sessionCookie, 200)
	h.call("GET", "/api/v1/me", nil, sessionCookie, 401)
	login := h.call("POST", "/api/v1/auth/login", map[string]any{"method": "password", "phone": "13800000000", "password": "test-password-21"}, nil, 200)
	var loginCookie *http.Cookie
	for _, c := range login.Result().Cookies() {
		if c.Name == "qingye_session" {
			loginCookie = c
		}
	}
	if loginCookie == nil {
		t.Fatal("registered password cannot establish session")
	}
	h.call("GET", "/api/v1/me", nil, loginCookie, 200)
	h.call("POST", "/api/v1/auth/sms", map[string]any{"phone": "13800000000", "purpose": "login"}, cookie, 503)
	if p.sent.Load() != 1 {
		t.Fatal("unexpected provider effect")
	}
	admin := &App{DB: a.DB, Redis: a.Redis, Config: a.Config}
	admin.Config.Surface = "admin"
	adminH := memberHTTP{t: t, handler: admin.Routes(), ip: "192.0.2.81"}
	adminH.call("POST", "/api/v1/auth/register", registrationInput(envelope.Data.ID), cookie, 401)
}
func TestR21RegistrationRedisBudgetAtomic(t *testing.T) {
	a, _, s := registrationFixture(t)
	l := a.Registration.Limits
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Send(context.Background(), s) == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("cooldown race", accepted.Load())
	}
	// Denials must not consume the global budget; a second phone can still send.
	s.PhoneKey = "other-phone"
	if e := l.Send(context.Background(), s); e != nil {
		t.Fatal("partial quota debit", e)
	}
}
func TestR21RegistrationParallelRegister(t *testing.T) {
	a, p, s := registrationFixture(t)
	id := issueRegistration(t, a, s)
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := a.Registration.Register(context.Background(), s, registrationInput(id)); e == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	var count int
	a.DB.QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&count)
	if count != 1 || successes.Load() < 1 || p.checked.Load() != 1 {
		t.Fatal("registration race", count, successes.Load(), p.checked.Load())
	}
}
