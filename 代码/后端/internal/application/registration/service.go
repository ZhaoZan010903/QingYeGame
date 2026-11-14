package registration

import (
	"context"
	"encoding/json"
	securityapp "qingye/backend/internal/application/security"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/identity"
	domain "qingye/backend/internal/domain/registration"
	"strings"
	"time"
)

type Repository interface {
	Create(context.Context, domain.Challenge) error
	ClaimSend(context.Context, string, string, string, string, string) (domain.Challenge, error)
	Mark(context.Context, domain.Challenge, string, domain.Receipt) error
	ClaimCheck(context.Context, domain.Input, string, string, string) (domain.Challenge, error)
	CheckResult(context.Context, domain.Challenge, bool) error
	Register(context.Context, domain.Input, string, string, string, string, string, string, time.Time) (identity.User, error)
	Login(context.Context, domain.Input, string, string, string, time.Time) (identity.User, bool, error)
	PendingRegistration(context.Context, domain.Input, string, string, string) error
}
type Provider interface {
	Send(context.Context, string, string) (domain.Receipt, error)
	Check(context.Context, string, string, string) (bool, error)
}
type Limiter interface {
	Challenge(context.Context, domain.Subject) error
	Send(context.Context, domain.Subject) error
	Check(context.Context, domain.Subject) error
}
type Service struct {
	Store           Repository
	Provider        Provider
	LoginProvider   Provider
	Human           securityapp.HumanVerifier
	Limits          Limiter
	NewID, NewToken func() string
	Hash, MAC       func(string) string
	PasswordHash    func(context.Context, string) (string, error)
	AllowedPhones   []string
	// PublicAccess is only set after production configuration validation.
	PublicAccess bool
}
type Session struct {
	User    identity.User
	Token   string
	Expires time.Time
}

func (s *Service) Create(ctx context.Context, who domain.Subject, phone string) (domain.Challenge, error) {
	return s.CreatePurpose(ctx, who, phone, "register")
}
func (s *Service) CreatePurpose(ctx context.Context, who domain.Subject, phone, purpose string) (domain.Challenge, error) {
	if purpose != "register" && purpose != "login" {
		return domain.Challenge{}, domain.ErrChallenge
	}
	if !domain.ValidPhone(phone) {
		return domain.Challenge{}, fault.New(fault.Invalid, "INVALID_PHONE", "请输入有效的11位手机号")
	}
	allowed := s.PublicAccess
	for _, p := range s.AllowedPhones {
		if p == phone {
			allowed = true
		}
	}
	if !allowed {
		return domain.Challenge{}, fault.New(fault.Forbidden, "SMS_TEST_NUMBER_REQUIRED", "当前短信处于测试阶段，请使用已登记的测试手机号")
	}
	if err := s.Limits.Challenge(ctx, who); err != nil {
		return domain.Challenge{}, err
	}
	c := domain.Challenge{ID: s.NewID(), Phone: phone, Binding: who.Binding, Hostname: who.Hostname, Stage: "challenge", Purpose: purpose}
	return c, s.Store.Create(ctx, c)
}
func (s *Service) Send(ctx context.Context, who domain.Subject, phone, id, token string) error {
	return s.SendPurpose(ctx, who, phone, id, token, "register")
}
func (s *Service) SendPurpose(ctx context.Context, who domain.Subject, phone, id, token, purpose string) error {
	provider := s.provider(purpose)
	if provider == nil {
		return domain.ErrUnavailable
	}
	if !domain.ValidPhone(phone) || !domain.ValidID(id) || len(token) < 1 || len(token) > 2048 || strings.TrimSpace(token) != token {
		return domain.ErrChallenge
	}
	c, err := s.Store.ClaimSend(ctx, id, phone, who.Binding, s.NewID(), purpose)
	if err != nil {
		return err
	}
	if c.Stage == "sent" {
		return nil
	}
	finish := func(stage string, r domain.Receipt) error {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		return s.Store.Mark(cctx, c, stage, r)
	}
	ok, err := s.Human.Verify(ctx, token, domain.Action, c.Hostname, c.ID, who.IP)
	if err != nil || !ok {
		_ = finish("failed", domain.Receipt{})
		return fault.New(fault.Invalid, "HUMAN_VERIFICATION_FAILED", "安全验证未完成，请重新获取")
	}
	if err = s.Limits.Send(ctx, who); err != nil {
		_ = finish("failed", domain.Receipt{})
		return err
	}
	// Persist intent before the external effect. Unknown results must never trigger automatic resend.
	if err = s.Store.Mark(ctx, c, "sending", domain.Receipt{}); err != nil {
		return err
	}
	receipt, err := provider.Send(ctx, phone, c.ID)
	if err != nil {
		_ = finish("unknown", domain.Receipt{})
		return fault.New(fault.Unavailable, "SMS_SEND_UNCERTAIN", "发送结果暂未确认，请留意手机短信，稍后再试")
	}
	return finish("sent", receipt)
}
func (s *Service) Register(ctx context.Context, who domain.Subject, in domain.Input) (Session, error) {
	if err := domain.Validate(in); err != nil {
		return Session{}, err
	}
	if err := s.Limits.Check(ctx, who); err != nil {
		return Session{}, err
	}
	raw, _ := json.Marshal(in)
	fingerprint := s.MAC(string(raw))
	proof := fingerprint
	if domain.Purpose(in.Purpose) == "login" {
		proof = s.loginFingerprint(in)
		if err := s.Store.PendingRegistration(ctx, in, who.Binding, proof, fingerprint); err != nil {
			return Session{}, err
		}
	} else if err := s.check(ctx, who, in, fingerprint); err != nil {
		return Session{}, err
	}
	hash, err := s.PasswordHash(ctx, in.Password)
	if err != nil {
		return Session{}, err
	}
	token := s.NewToken()
	expires := time.Now().Add(7 * 24 * time.Hour)
	user, err := s.Store.Register(ctx, in, who.Binding, fingerprint, proof, hash, s.NewID(), s.Hash(token), expires)
	if err != nil {
		return Session{}, err
	}
	return Session{User: user, Token: token, Expires: expires}, nil
}

func (s *Service) check(ctx context.Context, who domain.Subject, in domain.Input, fingerprint string) error {
	provider := s.provider(domain.Purpose(in.Purpose))
	if provider == nil {
		return domain.ErrUnavailable
	}
	c, err := s.Store.ClaimCheck(ctx, in, who.Binding, fingerprint, s.NewID())
	if err != nil {
		return err
	}
	if c.Stage == "checking" {
		ok, providerErr := provider.Check(ctx, in.Phone, in.Code, c.ID)
		done, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		err = s.Store.CheckResult(done, c, providerErr == nil && ok)
		cancel()
		if err != nil {
			return err
		}
		if providerErr != nil {
			return domain.ErrUnavailable
		}
		if !ok {
			return domain.ErrCode
		}
	}
	return nil
}
