package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/identity"
	domain "qingye/backend/internal/domain/registration"
	"time"
)

type RegistrationStore struct{ DB *pgxpool.Pool }

func (s RegistrationStore) Create(ctx context.Context, c domain.Challenge) error {
	// Bounded housekeeping; registered consent is retained in its own table.
	_, err := s.DB.Exec(ctx, `DELETE FROM sms_challenges WHERE id IN (SELECT id FROM sms_challenges WHERE provider='pnvs' AND expires_at<now()-interval '1 day' LIMIT 100)`)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO sms_challenges(id,phone,purpose,provider,binding_hash,hostname,stage,expires_at) VALUES($1,$2,$5,'pnvs',$3,$4,'challenge',now()+interval '5 minutes')`, c.ID, c.Phone, c.Binding, c.Hostname, domain.Purpose(c.Purpose))
	return err
}
func registrationRow(ctx context.Context, tx pgx.Tx, id, phone, binding, purpose string) (domain.Challenge, error) {
	var c domain.Challenge
	err := tx.QueryRow(ctx, `SELECT id,phone,binding_hash,hostname,stage,attempt_id,request_hash,coalesce(registered_user_id,''),attempts,expires_at,purpose,completion_hash FROM sms_challenges WHERE id=$1 AND phone=$2 AND binding_hash=$3 AND purpose=$4 AND provider='pnvs' AND expires_at>now() FOR UPDATE`, id, phone, binding, domain.Purpose(purpose)).Scan(&c.ID, &c.Phone, &c.Binding, &c.Hostname, &c.Stage, &c.Attempt, &c.RequestHash, &c.UserID, &c.Attempts, &c.Expires, &c.Purpose, &c.CompletionHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, domain.ErrChallenge
	}
	return c, err
}
func (s RegistrationStore) ClaimSend(ctx context.Context, id, phone, binding, attempt, purpose string) (domain.Challenge, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return domain.Challenge{}, err
	}
	defer tx.Rollback(ctx)
	c, err := registrationRow(ctx, tx, id, phone, binding, purpose)
	if err != nil {
		return c, err
	}
	if c.Stage == "sent" {
		return c, nil
	}
	if c.Stage != "challenge" {
		return c, domain.ErrBusy
	}
	_, err = tx.Exec(ctx, `UPDATE sms_challenges SET stage='verifying',attempt_id=$2 WHERE id=$1`, id, attempt)
	if err != nil {
		return c, err
	}
	c.Stage = "verifying"
	c.Attempt = attempt
	return c, tx.Commit(ctx)
}
func (s RegistrationStore) Mark(ctx context.Context, c domain.Challenge, stage string, r domain.Receipt) error {
	previous := "verifying"
	if stage == "sent" || stage == "unknown" {
		previous = "sending"
	}
	tag, err := s.DB.Exec(ctx, `UPDATE sms_challenges SET stage=$3,provider_biz_id=$4,provider_request_id=$5,
 expires_at=CASE WHEN $3='sent' THEN now()+interval '5 minutes' ELSE expires_at END
 WHERE id=$1 AND attempt_id=$2 AND stage=$6`, c.ID, c.Attempt, stage, r.BizID, r.RequestID, previous)
	if err == nil && tag.RowsAffected() != 1 {
		return domain.ErrChallenge
	}
	return err
}
func (s RegistrationStore) ClaimCheck(ctx context.Context, in domain.Input, binding, fingerprint, attempt string) (domain.Challenge, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return domain.Challenge{}, err
	}
	defer tx.Rollback(ctx)
	c, err := registrationRow(ctx, tx, in.ChallengeID, in.Phone, binding, in.Purpose)
	if err != nil {
		return c, err
	}
	if c.Stage == "verified" || c.Stage == "registered" || c.Stage == "registration_pending" || c.Stage == "logged_in" {
		if c.RequestHash != fingerprint {
			return c, domain.ErrChallenge
		}
		return c, nil
	}
	if c.Stage == "checking" {
		return c, domain.ErrBusy
	}
	if c.Stage != "sent" || c.Attempts >= 5 {
		return c, domain.ErrCode
	}
	_, err = tx.Exec(ctx, `UPDATE sms_challenges SET stage='checking',attempts=attempts+1,request_hash=$2,attempt_id=$3 WHERE id=$1`, c.ID, fingerprint, attempt)
	if err != nil {
		return c, err
	}
	c.Stage = "checking"
	c.Attempt = attempt
	c.RequestHash = fingerprint
	c.Attempts++
	return c, tx.Commit(ctx)
}
func (s RegistrationStore) CheckResult(ctx context.Context, c domain.Challenge, pass bool) error {
	stage := "sent"
	if pass {
		stage = "verified"
	} else if c.Attempts >= 5 {
		stage = "failed"
	}
	tag, err := s.DB.Exec(ctx, `UPDATE sms_challenges SET stage=$3,request_hash=CASE WHEN $3='verified' THEN request_hash ELSE '' END WHERE id=$1 AND attempt_id=$2 AND stage='checking'`, c.ID, c.Attempt, stage)
	if err == nil && tag.RowsAffected() != 1 {
		return domain.ErrChallenge
	}
	return err
}
func (s RegistrationStore) Register(ctx context.Context, in domain.Input, binding, fingerprint, proof, hash, id, sessionHash string, expires time.Time) (identity.User, error) {
	var u identity.User
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return u, err
	}
	defer tx.Rollback(ctx)
	c, err := registrationRow(ctx, tx, in.ChallengeID, in.Phone, binding, in.Purpose)
	if err != nil {
		return u, err
	}
	ready := "verified"
	if domain.Purpose(in.Purpose) == "login" {
		ready = "registration_pending"
	}
	if (c.Stage != ready && c.Stage != "registered") || c.RequestHash != proof || (c.Stage == "registered" && ready == "registration_pending" && c.CompletionHash != fingerprint) {
		return u, domain.ErrChallenge
	}
	if c.Stage == ready {
		// Lock the published agreement versions until account and consent commit together.
		rows, e := tx.Query(ctx, `SELECT slug,version FROM content_pages WHERE slug IN ('terms','privacy') AND published ORDER BY slug FOR SHARE`)
		if e != nil {
			return u, e
		}
		versions := map[string]int{}
		for rows.Next() {
			var slug string
			var v int
			if e = rows.Scan(&slug, &v); e != nil {
				rows.Close()
				return u, e
			}
			versions[slug] = v
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return u, e
		}
		if versions["terms"] != in.Agreement.Terms || versions["privacy"] != in.Agreement.Privacy {
			return u, fault.New(fault.Conflict, "AGREEMENT_CHANGED", "协议已更新，请重新阅读并同意")
		}
		tag, e := tx.Exec(ctx, `INSERT INTO users(id,phone,password_hash,nickname,role,enabled) VALUES($1,$2,$3,'清叶会员','member',true) ON CONFLICT(phone) DO NOTHING`, id, in.Phone, hash)
		if e != nil {
			return u, e
		}
		if tag.RowsAffected() != 1 {
			return u, fault.New(fault.Conflict, "ACCOUNT_EXISTS", "该手机号已有账号，请返回登录")
		}
		_, e = tx.Exec(ctx, `INSERT INTO registration_consents(user_id,terms_version,privacy_version) VALUES($1,$2,$3)`, id, in.Agreement.Terms, in.Agreement.Privacy)
		if e != nil {
			return u, e
		}
		_, e = tx.Exec(ctx, `UPDATE sms_challenges SET stage='registered',used_at=now(),registered_user_id=$2,completion_hash=$3 WHERE id=$1`, c.ID, id, fingerprint)
		if e != nil {
			return u, e
		}
		c.UserID = id
	}
	err = tx.QueryRow(ctx, `SELECT id,phone,nickname,role,enabled FROM users WHERE id=$1 AND phone=$2 AND role='member' AND enabled FOR SHARE`, c.UserID, in.Phone).Scan(&u.ID, &u.Phone, &u.Nickname, &u.Role, &u.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, domain.ErrChallenge
	}
	if err != nil {
		return u, err
	}
	// A lost HTTP response may retry the exact verified request, never a different password.
	_, err = tx.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at,audience) VALUES($1,$2,$3,'public')`, sessionHash, u.ID, expires)
	if err != nil {
		return u, err
	}
	return u, tx.Commit(ctx)
}
