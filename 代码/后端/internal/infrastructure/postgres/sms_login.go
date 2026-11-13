package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/identity"
	domain "qingye/backend/internal/domain/registration"
	"time"
)

func (s RegistrationStore) PendingRegistration(ctx context.Context, in domain.Input, binding, proof, completion string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	c, err := registrationRow(ctx, tx, in.ChallengeID, in.Phone, binding, "login")
	if err != nil {
		return err
	}
	if c.RequestHash != proof || (c.Stage != "registration_pending" && c.Stage != "registered") || (c.Stage == "registered" && c.CompletionHash != completion) {
		return domain.ErrChallenge
	}
	return nil
}

func (s RegistrationStore) Login(ctx context.Context, in domain.Input, binding, proof, sessionHash string, expires time.Time) (identity.User, bool, error) {
	var u identity.User
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return u, false, err
	}
	defer tx.Rollback(ctx)
	c, err := registrationRow(ctx, tx, in.ChallengeID, in.Phone, binding, "login")
	if err != nil {
		return u, false, err
	}
	if c.RequestHash != proof {
		return u, false, domain.ErrChallenge
	}
	if c.Stage == "registration_pending" {
		return u, true, nil
	}
	if c.Stage != "verified" && c.Stage != "logged_in" && c.Stage != "registered" {
		return u, false, domain.ErrChallenge
	}
	err = tx.QueryRow(ctx, `SELECT id,phone,nickname,role,enabled FROM users WHERE phone=$1 FOR SHARE`, in.Phone).Scan(&u.ID, &u.Phone, &u.Nickname, &u.Role, &u.Enabled)
	if errors.Is(err, pgx.ErrNoRows) && c.Stage == "verified" {
		_, err = tx.Exec(ctx, `UPDATE sms_challenges SET stage='registration_pending' WHERE id=$1`, c.ID)
		if err != nil {
			return u, false, err
		}
		return u, true, tx.Commit(ctx)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return u, false, err
	}
	// A public SMS proof can neither grant admin access nor reactivate an account.
	if err != nil || !u.Enabled || u.Role != "member" || (c.UserID != "" && c.UserID != u.ID) {
		return identity.User{}, false, fault.New(fault.Forbidden, "SMS_ACCOUNT_UNAVAILABLE", "该账号无法使用短信登录，请联系平台客服")
	}
	if c.Stage == "verified" {
		_, err = tx.Exec(ctx, `UPDATE sms_challenges SET stage='logged_in',used_at=now(),registered_user_id=$2 WHERE id=$1`, c.ID, u.ID)
		if err != nil {
			return u, false, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at,audience) VALUES($1,$2,$3,'public')`, sessionHash, u.ID, expires)
	if err != nil {
		return u, false, err
	}
	return u, false, tx.Commit(ctx)
}
