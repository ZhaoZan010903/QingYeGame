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
	ClaimSend(context.Context, string, string, string, string) (domain.Challenge, error)
	Mark(context.Context, domain.Challenge, string, domain.Receipt) error
	ClaimCheck(context.Context, domain.Input, string, string, string) (domain.Challenge, error)
	CheckResult(context.Context, domain.Challenge, bool) error
	Register(context.Context, domain.Input, string, string, string, string, string, time.Time) (identity.User, error)
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
	Human           securityapp.HumanVerifier
	Limits          Limiter
	NewID, NewToken func() string
	Hash, MAC       func(string) string
	PasswordHash    func(context.Context, string) (string, error)
	AllowedPhones   []string
}
type Session struct {
	User    identity.User
	Token   string
	Expires time.Time
}

func (s *Service) Create(ctx context.Context, who domain.Subject, phone string) (domain.Challenge, error) {
	if !domain.ValidPhone(phone) {
		return domain.Challenge{}, fault.New(fault.Invalid, "INVALID_PHONE", "请输入有效的11位手机号")
	}
	allowed := false
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
	c := domain.Challenge{ID: s.NewID(), Phone: phone, Binding: who.Binding, Hostname: who.Hostname, Stage: "challenge"}
	return c, s.Store.Create(ctx, c)
}
func (s *Service) Send(ctx context.Context, who domain.Subject, phone, id, token string) error {
	if !domain.ValidPhone(phone) || !domain.ValidID(id) || len(token) < 1 || len(token) > 2048 || strings.TrimSpace(token) != token {
		return domain.ErrChallenge
	}
	c, err := s.Store.ClaimSend(ctx, id, phone, who.Binding, s.NewID())
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
	receipt, err := s.Provider.Send(ctx, phone, c.ID)
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
	c, err := s.Store.ClaimCheck(ctx, in, who.Binding, fingerprint, s.NewID())
	if err != nil {
		return Session{}, err
	}
	if c.Stage == "checking" {
		ok, providerErr := s.Provider.Check(ctx, in.Phone, in.Code, c.ID)
		done, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		err = s.Store.CheckResult(done, c, providerErr == nil && ok)
		cancel()
		if err != nil {
			return Session{}, err
		}
		if providerErr != nil {
			return Session{}, domain.ErrUnavailable
		}
		if !ok {
			return Session{}, domain.ErrCode
		}
	}
	hash, err := s.PasswordHash(ctx, in.Password)
	if err != nil {
		return Session{}, err
	}
	token := s.NewToken()
	expires := time.Now().Add(7 * 24 * time.Hour)
	user, err := s.Store.Register(ctx, in, who.Binding, fingerprint, hash, s.NewID(), s.Hash(token), expires)
	if err != nil {
		return Session{}, err
	}
	return Session{User: user, Token: token, Expires: expires}, nil
}
