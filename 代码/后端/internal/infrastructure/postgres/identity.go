package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	application "qingye/backend/internal/application/identity"
	"qingye/backend/internal/domain/identity"
	"qingye/backend/internal/infrastructure/security"
	"time"
)

type IdentityStore struct{ DB *pgxpool.Pool }

func identityError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ErrNotFound
	}
	return err
}

func (s IdentityStore) Credentials(ctx context.Context, phone string) (identity.User, string, error) {
	var u identity.User
	var hash string
	err := s.DB.QueryRow(ctx, `SELECT id,phone,nickname,role,enabled,password_hash FROM users WHERE phone=$1`, phone).Scan(&u.ID, &u.Phone, &u.Nickname, &u.Role, &u.Enabled, &hash)
	return u, hash, identityError(err)
}
func (s IdentityStore) Session(ctx context.Context, hash, surface, role string) (*identity.User, error) {
	var u identity.User
	// audience与角色在数据库查询中同时约束，跨入口Cookie不能提升权限。
	err := s.DB.QueryRow(ctx, `SELECT u.id,u.phone,u.nickname,u.role,u.enabled FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.audience=$2 AND s.expires_at>now() AND u.enabled AND u.role=$3`, hash, surface, role).Scan(&u.ID, &u.Phone, &u.Nickname, &u.Role, &u.Enabled)
	if err != nil {
		return nil, identityError(err)
	}
	return &u, nil
}
func (s IdentityStore) CreateSession(ctx context.Context, hash string, verified identity.User, verifiedHash, surface string, expires time.Time) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current identity.User
	var currentHash string
	err = tx.QueryRow(ctx, `SELECT id,role,enabled,password_hash FROM users WHERE id=$1 FOR UPDATE`, verified.ID).Scan(&current.ID, &current.Role, &current.Enabled, &currentHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ErrCredentialsChanged
	}
	if err != nil {
		return err
	}
	// Serialize session issuance with disable/password reset. Verification outside
	// this transaction cannot authorize an insertion after credentials changed.
	if !current.CanAccess(surface) || current.Role != verified.Role || currentHash != verifiedHash {
		return application.ErrCredentialsChanged
	}
	_, err = tx.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at,audience) VALUES($1,$2,$3,$4)`, hash, verified.ID, expires, surface)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return err
}
func (s IdentityStore) DeleteSession(ctx context.Context, hash, surface string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1 AND audience=$2`, hash, surface)
	return err
}
func (s IdentityStore) UpdateNickname(ctx context.Context, id, nickname string) error {
	return (ProfileStore{DB: s.DB, NewID: security.NewID}).RequestNickname(ctx, id, nickname)
}
