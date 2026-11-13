package security

import (
	"context"
	"golang.org/x/crypto/bcrypt"
	application "qingye/backend/internal/application/identity"
)

var ErrPasswordBusy = application.ErrVerificationBusy

// 密码校验消耗CPU；限制并行数量，避免分散账号的登录请求耗尽进程。
type PasswordVerifier struct{ slots chan struct{} }

func NewPasswordVerifier(parallel int) *PasswordVerifier {
	if parallel < 1 {
		parallel = 2
	}
	return &PasswordVerifier{slots: make(chan struct{}, parallel)}
}

func (v *PasswordVerifier) Compare(ctx context.Context, hash, password string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case v.slots <- struct{}{}:
		defer func() { <-v.slots }()
	default:
		return ErrPasswordBusy
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if e := ctx.Err(); e != nil {
		return e
	}
	return err
}

func (v *PasswordVerifier) Hash(ctx context.Context, password string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	select {
	case v.slots <- struct{}{}:
		defer func() { <-v.slots }()
	default:
		return "", ErrPasswordBusy
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if e := ctx.Err(); e != nil {
		return "", e
	}
	return string(hash), err
}
