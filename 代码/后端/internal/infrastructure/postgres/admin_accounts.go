package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	domain "qingye/backend/internal/domain/adminaccounts"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
	"qingye/backend/internal/infrastructure/security"
)

type AdminAccountStore struct {
	DB            *pgxpool.Pool
	NewID         func() string
	CredentialKey string
}

const adminAccountProjection = `jsonb_build_object('id',u.id,'login',u.phone,'nickname',u.nickname,'avatar_id',coalesce(u.avatar_media_id,''),'avatar_url',coalesce('/api/v1/media/'||u.avatar_media_id||'/file?variant=thumb',''),'enabled',u.enabled,'version',u.admin_profile_version,'created_at',u.created_at,'updated_at',u.updated_at)`

type lockedAccount struct {
	ID, Role, AvatarID string
	Enabled            bool
	Version            int64
}

// Account writes and chat assignment share the advisory lock. User rows are then
// locked in ID order; the actor is rechecked after waiting, never only in HTTP.
func lockAccountMutation(ctx context.Context, tx pgx.Tx, actor, target string) (lockedAccount, error) {
	var result lockedAccount
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, chatAssignmentLock); err != nil {
		return result, err
	}
	rows, err := tx.Query(ctx, `SELECT id,role,enabled,admin_profile_version,coalesce(avatar_media_id,'') FROM users WHERE id=$1 OR id=$2 ORDER BY id FOR UPDATE`, actor, target)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	actorOK := false
	found := target == ""
	for rows.Next() {
		var item lockedAccount
		if err = rows.Scan(&item.ID, &item.Role, &item.Enabled, &item.Version, &item.AvatarID); err != nil {
			return result, err
		}
		if item.ID == actor {
			actorOK = item.Enabled && item.Role == "admin"
		}
		if item.ID == target {
			result = item
			found = true
		}
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	if !actorOK {
		return result, fault.New(fault.Forbidden, "FORBIDDEN", "当前管理员已停用或无管理权限")
	}
	if !found {
		return result, fault.New(fault.NotFound, "NOT_FOUND", "账号不存在")
	}
	return result, nil
}
func checkAdminVersion(target lockedAccount, version int64) error {
	if target.Role != "admin" {
		return fault.New(fault.NotFound, "NOT_FOUND", "管理员不存在")
	}
	if target.Version != version {
		return fault.New(fault.Conflict, "VERSION_CONFLICT", "管理员资料已变化，请刷新后重试")
	}
	return nil
}
func checkAdminAvatar(ctx context.Context, tx pgx.Tx, id string) error {
	if id == "" {
		return nil
	}
	var found string
	// The users reference guard also locks media FOR UPDATE. Acquire it up front
	// so two accounts selecting the same avatar never both upgrade SHARE locks.
	err := tx.QueryRow(ctx, `SELECT m.id FROM avatar_library a JOIN media_files m ON m.id=a.media_id WHERE m.id=$1 AND a.enabled AND m.purpose='avatar' AND m.status='ready' AND m.deleted_at IS NULL FOR SHARE OF a FOR UPDATE OF m`, id).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.New(fault.Invalid, "INVALID_AVATAR", "请选择头像库中可用的头像")
	}
	return err
}
func (s AdminAccountStore) List(ctx context.Context, q string, limit, offset int) (map[string]any, error) {
	var data []map[string]any
	var total int
	err := s.DB.QueryRow(ctx, `WITH matching AS MATERIALIZED (SELECT * FROM users WHERE role='admin' AND (nickname ILIKE '%'||$1||'%' OR phone ILIKE '%'||$1||'%')), page AS (SELECT `+adminAccountProjection+` AS item,u.created_at,u.id FROM matching u ORDER BY u.created_at DESC,u.id LIMIT $2 OFFSET $3) SELECT coalesce((SELECT jsonb_agg(item ORDER BY created_at DESC,id) FROM page),'[]'::jsonb),(SELECT count(*) FROM matching)`, q, limit, offset).Scan(&data, &total)
	if err != nil {
		return nil, managementStoreError(err)
	}
	return map[string]any{"data": data, "total": total, "next_cursor": managementCursor(offset, limit, total)}, nil
}
func (s AdminAccountStore) fingerprint(actor, operation, target string, input any) (string, error) {
	key, err := security.CredentialKey(s.CredentialKey)
	if err != nil {
		return "", fault.New(fault.Unavailable, "ACCOUNT_MANAGEMENT_UNAVAILABLE", "管理员账号服务暂不可用")
	}
	raw, err := json.Marshal(struct {
		Actor, Operation, Target string
		Input                    any
	}{actor, operation, target, input})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("qingye:admin-account-request:v1\x00"))
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil)), nil
}
func adminAccountReplay(ctx context.Context, tx pgx.Tx, actor, operation, request, hash string) (map[string]any, error) {
	var old string
	var response map[string]any
	err := tx.QueryRow(ctx, `SELECT request_hash,response FROM admin_account_requests WHERE actor_id=$1 AND operation=$2 AND request_id=$3`, actor, operation, request).Scan(&old, &response)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !hmac.Equal([]byte(old), []byte(hash)) {
		return nil, fault.New(fault.Conflict, "IDEMPOTENCY_CONFLICT", "该请求标识已用于不同内容，请核对后重试")
	}
	return response, nil
}
func saveAdminAccountReplay(ctx context.Context, tx pgx.Tx, actor, operation, request, hash string, response map[string]any) error {
	_, err := tx.Exec(ctx, `INSERT INTO admin_account_requests(actor_id,operation,request_id,request_hash,response) VALUES($1,$2,$3,$4,$5)`, actor, operation, request, hash, response)
	return err
}
func adminAccountData(ctx context.Context, tx pgx.Tx, id string) (map[string]any, error) {
	var data map[string]any
	err := tx.QueryRow(ctx, `SELECT `+adminAccountProjection+` FROM users u WHERE u.id=$1 AND u.role='admin'`, id).Scan(&data)
	return map[string]any{"data": data}, err
}
func (s AdminAccountStore) audit(ctx context.Context, tx pgx.Tx, actor, id, action string, detail any) error {
	_, err := tx.Exec(ctx, `INSERT INTO admin_logs(id,user_id,action,resource_id,detail) VALUES($1,$2,$3,$4,$5)`, s.NewID(), actor, action, id, detail)
	return err
}
func (s AdminAccountStore) Create(ctx context.Context, actor management.Actor, in domain.CreateInput, passwordHash string) (map[string]any, error) {
	fingerprint, err := s.fingerprint(actor.ID, "create", "", in)
	if err != nil {
		return nil, managementStoreError(err)
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, managementStoreError(err)
	}
	defer tx.Rollback(ctx)
	if _, err = lockAccountMutation(ctx, tx, actor.ID, ""); err != nil {
		return nil, managementStoreError(err)
	}
	replay, err := adminAccountReplay(ctx, tx, actor.ID, "create", in.RequestID, fingerprint)
	if err != nil || replay != nil {
		return replay, managementStoreError(err)
	}
	if err = checkAdminAvatar(ctx, tx, in.AvatarID); err != nil {
		return nil, managementStoreError(err)
	}
	id := s.NewID()
	_, err = tx.Exec(ctx, `INSERT INTO users(id,phone,nickname,password_hash,role,avatar_media_id) VALUES($1,$2,$3,$4,'admin',NULLIF($5,''))`, id, in.Login, in.Nickname, passwordHash, in.AvatarID)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" && pe.ConstraintName == "users_phone_key" {
		return nil, fault.New(fault.Conflict, "LOGIN_EXISTS", "登录账号已存在")
	}
	if err != nil {
		return nil, managementStoreError(err)
	}
	if err = s.audit(ctx, tx, actor.ID, id, "administrator.create", map[string]any{"version": 1}); err != nil {
		return nil, managementStoreError(err)
	}
	out, err := adminAccountData(ctx, tx, id)
	if err != nil {
		return nil, managementStoreError(err)
	}
	if err = saveAdminAccountReplay(ctx, tx, actor.ID, "create", in.RequestID, fingerprint, out); err == nil {
		err = tx.Commit(ctx)
	}
	return out, managementStoreError(err)
}
func (s AdminAccountStore) Update(ctx context.Context, actor management.Actor, id string, in domain.UpdateInput) (map[string]any, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, managementStoreError(err)
	}
	defer tx.Rollback(ctx)
	target, err := lockAccountMutation(ctx, tx, actor.ID, id)
	if err != nil {
		return nil, managementStoreError(err)
	}
	if err = checkAdminVersion(target, in.ExpectedVersion); err != nil {
		return nil, err
	}
	if in.Enabled != nil && !*in.Enabled {
		if id == actor.ID {
			return nil, fault.New(fault.Invalid, "SELF_DISABLE", "不能停用当前管理员")
		}
		var others int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE role='admin' AND enabled AND id<>$1`, id).Scan(&others); err != nil {
			return nil, managementStoreError(err)
		}
		if others == 0 {
			return nil, fault.New(fault.Conflict, "LAST_ADMIN", "需保留至少一位可用管理员")
		}
	}
	if in.AvatarID != nil && *in.AvatarID != target.AvatarID {
		if err = checkAdminAvatar(ctx, tx, *in.AvatarID); err != nil {
			return nil, managementStoreError(err)
		}
	}
	_, err = tx.Exec(ctx, `UPDATE users SET nickname=coalesce($2,nickname),avatar_media_id=CASE WHEN $3::text IS NULL THEN avatar_media_id ELSE NULLIF($3,'') END,enabled=coalesce($4,enabled),admin_profile_version=admin_profile_version+1,updated_at=now() WHERE id=$1`, id, in.Nickname, in.AvatarID, in.Enabled)
	if err == nil && in.Enabled != nil && !*in.Enabled {
		_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, id)
	}
	if err == nil {
		err = s.audit(ctx, tx, actor.ID, id, "administrator.update", map[string]any{"version": target.Version + 1, "nickname_changed": in.Nickname != nil, "avatar_changed": in.AvatarID != nil, "enabled": in.Enabled})
	}
	if err != nil {
		return nil, managementStoreError(err)
	}
	out, err := adminAccountData(ctx, tx, id)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return out, managementStoreError(err)
}
func (s AdminAccountStore) ResetPassword(ctx context.Context, actor management.Actor, id string, in domain.PasswordInput, passwordHash string) (map[string]any, error) {
	fingerprint, err := s.fingerprint(actor.ID, "password", id, in)
	if err != nil {
		return nil, managementStoreError(err)
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, managementStoreError(err)
	}
	defer tx.Rollback(ctx)
	target, err := lockAccountMutation(ctx, tx, actor.ID, id)
	if err != nil {
		return nil, managementStoreError(err)
	}
	replay, err := adminAccountReplay(ctx, tx, actor.ID, "password", in.RequestID, fingerprint)
	if err != nil || replay != nil {
		return replay, managementStoreError(err)
	}
	if err = checkAdminVersion(target, in.ExpectedVersion); err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE users SET password_hash=$2,admin_profile_version=admin_profile_version+1,updated_at=now() WHERE id=$1`, id, passwordHash)
	if err == nil {
		_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, id)
	}
	if err == nil {
		err = s.audit(ctx, tx, actor.ID, id, "administrator.password", map[string]any{"version": target.Version + 1, "sessions_revoked": true})
	}
	out := map[string]any{"data": map[string]any{"id": id, "version": target.Version + 1, "reauthenticate": id == actor.ID}}
	if err == nil {
		err = saveAdminAccountReplay(ctx, tx, actor.ID, "password", in.RequestID, fingerprint, out)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return out, managementStoreError(err)
}
