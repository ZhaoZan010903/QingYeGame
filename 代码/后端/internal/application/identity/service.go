// Package identity implements session use cases without HTTP or database types.
package identity

import (
	"context"
	"errors"
	"qingye/backend/internal/domain/fault"
	domain "qingye/backend/internal/domain/identity"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrNotFound = errors.New("identity not found")
var ErrVerificationBusy = errors.New("password verification capacity exhausted")

// 与建号成本一致的固定散列，使不存在的账号也经过密码比较。
const DummyPasswordHash = "$2a$12$TlUW/qGz0irw3rm5.GkCDeUp7g/NPfVdsplL993IqESbWNVDbtelK"

type Repository interface {
	Credentials(context.Context, string) (domain.User, string, error)
	Session(context.Context, string, string, string) (*domain.User, error)
	CreateSession(context.Context, string, string, string, time.Time) error
	DeleteSession(context.Context, string, string) error
	UpdateNickname(context.Context, string, string) error
}
type Limiter interface {
	Allow(context.Context, string, int, time.Duration) error
}
type Verifier interface {
	Compare(context.Context, string, string) error
}

type Service struct {
	store    Repository
	limiter  Limiter
	verifier Verifier
	surface  string
	token    func() string
	hash     func(string) string
}

func New(store Repository, limiter Limiter, verifier Verifier, surface string, token func() string, hash func(string) string) *Service {
	return &Service{store: store, limiter: limiter, verifier: verifier, surface: surface, token: token, hash: hash}
}

type LoginInput struct{ Phone, Password, Method, IP string }
type Session struct {
	User    domain.User
	Token   string
	Expires time.Time
}

func (s *Service) Current(ctx context.Context, hash string) (*domain.User, error) {
	if hash == "" {
		return nil, ErrNotFound
	}
	return s.store.Session(ctx, hash, s.surface, domain.RoleFor(s.surface))
}

func (s *Service) Login(ctx context.Context, in LoginInput) (Session, error) {
	var result Session
	if in.Method != "" && in.Method != "password" {
		return result, fault.New(fault.Unavailable, "SMS_NOT_CONFIGURED", "短信服务尚未配置")
	}
	in.Phone = domain.NormalizeLogin(in.Phone)
	invalid := func() error {
		return fault.New(fault.Unauthenticated, "INVALID_CREDENTIALS", "手机号或密码不正确")
	}
	if len(in.Phone) > 32 || !utf8.ValidString(in.Phone) || strings.ContainsRune(in.Phone, 0) || len(in.Password) > 72 || len(in.Password) < 1 {
		return result, invalid()
	}
	if err := s.limiter.Allow(ctx, "login-ip:"+s.surface+":"+s.hash(in.IP), 30, 15*time.Minute); err != nil {
		return result, err
	}
	if err := s.limiter.Allow(ctx, "login-phone:"+s.surface+":"+s.hash(in.Phone), 12, 15*time.Minute); err != nil {
		return result, err
	}
	u, hash, err := s.store.Credentials(ctx, in.Phone)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return result, fault.Wrap(fault.Unavailable, "DATABASE_UNAVAILABLE", "登录服务暂不可用", err)
	}
	if hash == "" {
		hash = DummyPasswordHash
	}
	passwordErr := s.verifier.Compare(ctx, hash, in.Password)
	if errors.Is(passwordErr, ErrVerificationBusy) {
		return result, fault.New(fault.Unavailable, "LOGIN_BUSY", "登录服务繁忙，请稍后重试")
	}
	if ctx.Err() != nil {
		return result, fault.Wrap(fault.Unavailable, "SESSION_UNAVAILABLE", "登录服务暂不可用", ctx.Err())
	}
	if err != nil || passwordErr != nil || !u.CanAccess(s.surface) {
		return result, invalid()
	}
	result = Session{User: u, Token: s.token(), Expires: time.Now().Add(7 * 24 * time.Hour)}
	if err = s.store.CreateSession(ctx, s.hash(result.Token), u.ID, s.surface, result.Expires); err != nil {
		return Session{}, fault.Wrap(fault.Unavailable, "SESSION_UNAVAILABLE", "登录服务暂不可用", err)
	}
	return result, nil
}

func (s *Service) Logout(ctx context.Context, hash string) error {
	if hash == "" {
		return nil
	}
	if err := s.store.DeleteSession(ctx, hash, s.surface); err != nil {
		return fault.Wrap(fault.Unavailable, "DATABASE_UNAVAILABLE", "退出失败，请重试", err)
	}
	return nil
}

func (s *Service) UpdateNickname(ctx context.Context, u domain.User, nickname string) (domain.User, error) {
	nickname = strings.TrimSpace(nickname)
	if n := utf8.RuneCountInString(nickname); n < 1 || n > 30 || !utf8.ValidString(nickname) || strings.ContainsRune(nickname, 0) {
		return u, fault.New(fault.Invalid, "INVALID_NICKNAME", "昵称需为1至30个有效字符")
	}
	if !u.CanAccess(s.surface) {
		return u, fault.New(fault.Forbidden, "FORBIDDEN", "无权修改此账号")
	}
	if err := s.store.UpdateNickname(ctx, u.ID, nickname); err != nil {
		return u, fault.Wrap(fault.Unavailable, "DATABASE_UNAVAILABLE", "保存失败", err)
	}
	u.Nickname = nickname
	return u, nil
}
