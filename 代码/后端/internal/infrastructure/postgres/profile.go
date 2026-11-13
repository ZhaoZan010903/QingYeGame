package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/profile"
)

type ProfileStore struct {
	DB    *pgxpool.Pool
	NewID func() string
}

func (s ProfileStore) Profile(ctx context.Context, id string) (map[string]any, error) {
	var data map[string]any
	err := s.DB.QueryRow(ctx, `SELECT jsonb_build_object('nickname',u.nickname,
 'nickname_request',(SELECT jsonb_build_object('id',n.id,'nickname',n.nickname,'status',n.status,'review_note',n.review_note,'created_at',n.created_at,'reviewed_at',n.reviewed_at) FROM nickname_requests n WHERE n.user_id=u.id ORDER BY n.created_at DESC,n.id DESC LIMIT 1),
 'next_nickname_change_at',u.nickname_changed_at+interval '30 days',
 'can_request_nickname',(u.nickname_changed_at IS NULL OR u.nickname_changed_at+interval '30 days'<=now()) AND NOT EXISTS(SELECT 1 FROM nickname_requests n WHERE n.user_id=u.id AND n.status='pending')) FROM users u WHERE id=$1`, id).Scan(&data)
	return map[string]any{"data": data}, mediaDBError(err)
}
func (s ProfileStore) RequestNickname(ctx context.Context, id, nickname string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return mediaDBError(err)
	}
	defer tx.Rollback(ctx)
	var old string
	var allowed bool
	// 与审核保持 user -> request 的锁顺序；冷却时间只使用数据库时钟。
	err = tx.QueryRow(ctx, `SELECT nickname,nickname_changed_at IS NULL OR nickname_changed_at+interval '30 days'<=now() FROM users WHERE id=$1 AND enabled AND role='member' FOR UPDATE`, id).Scan(&old, &allowed)
	if err != nil {
		return mediaDBError(err)
	}
	if !allowed {
		return fault.New(fault.Conflict, "NICKNAME_COOLDOWN", "昵称修改通过后需满30天才能再次申请")
	}
	if old == nickname {
		return fault.New(fault.Invalid, "NICKNAME_UNCHANGED", "新昵称与当前昵称相同")
	}
	var pending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM nickname_requests WHERE user_id=$1 AND status='pending')`, id).Scan(&pending); err != nil {
		return mediaDBError(err)
	}
	if pending {
		return fault.New(fault.Conflict, "NICKNAME_PENDING", "已有昵称申请待审核")
	}
	_, err = tx.Exec(ctx, `INSERT INTO nickname_requests(id,user_id,previous_nickname,nickname) VALUES($1,$2,$3,$4)`, s.NewID(), id, old, nickname)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return mediaDBError(err)
}
func (s ProfileStore) NicknameRequests(ctx context.Context, status string, page, limit int) (map[string]any, error) {
	// 总数与页内数据共享一个快照，避免并发审核造成分页不一致。
	var data map[string]any
	err := s.DB.QueryRow(ctx, `WITH selected AS MATERIALIZED (SELECT id,user_id,previous_nickname,nickname,status,review_note,created_at,reviewed_at FROM nickname_requests WHERE status=$1 ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3), page AS (SELECT n.*,u.nickname AS current_nickname,left(u.phone,3)||'****'||right(u.phone,4) AS phone_masked FROM selected n JOIN users u ON u.id=n.user_id) SELECT jsonb_build_object('data',coalesce((SELECT jsonb_agg(to_jsonb(page) ORDER BY created_at DESC,id DESC) FROM page),'[]'::jsonb),'pagination',jsonb_build_object('page',$4::int,'limit',$2::int,'total',(SELECT count(*) FROM nickname_requests WHERE status=$1)))`, status, limit, (page-1)*limit, page).Scan(&data)
	return data, mediaDBError(err)
}
func (s ProfileStore) ReviewNickname(ctx context.Context, actor, id string, in profile.ReviewInput) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return mediaDBError(err)
	}
	defer tx.Rollback(ctx)
	var user, old, current, nickname, status string
	var enabled, allowed bool
	if err = tx.QueryRow(ctx, `SELECT user_id FROM nickname_requests WHERE id=$1`, id).Scan(&user); err != nil {
		return mediaDBError(err)
	}
	if err = tx.QueryRow(ctx, `SELECT nickname,enabled AND role='member',nickname_changed_at IS NULL OR nickname_changed_at+interval '30 days'<=now() FROM users WHERE id=$1 FOR UPDATE`, user).Scan(&current, &enabled, &allowed); err != nil {
		return mediaDBError(err)
	}
	if err = tx.QueryRow(ctx, `SELECT previous_nickname,nickname,status FROM nickname_requests WHERE id=$1 FOR UPDATE`, id).Scan(&old, &nickname, &status); err != nil {
		return mediaDBError(err)
	}
	if status != "pending" {
		return fault.New(fault.Conflict, "REVIEW_CONFLICT", "此昵称申请已处理，请刷新列表")
	}
	if in.Decision == "approved" {
		if !enabled || !allowed || old != current {
			return fault.New(fault.Conflict, "PROFILE_CONFLICT", "会员状态或昵称已变化，不能通过此申请")
		}
		if _, err = tx.Exec(ctx, `UPDATE users SET nickname=$2,nickname_changed_at=now(),updated_at=now() WHERE id=$1`, user, nickname); err != nil {
			return mediaDBError(err)
		}
	}
	_, err = tx.Exec(ctx, `UPDATE nickname_requests SET status=$2,review_note=$3,reviewed_by=$4,reviewed_at=now() WHERE id=$1`, id, in.Decision, in.Note, actor)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO admin_logs(id,user_id,action,resource_id,detail) VALUES($1,$2,'nickname.review',$3,$4)`, s.NewID(), actor, id, map[string]any{"decision": in.Decision, "user_id": user})
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return mediaDBError(err)
}

const avatarProjection = `jsonb_build_object('id',a.id,'name',a.name,'media_id',a.media_id,'url','/api/v1/media/'||a.media_id||'/file?variant=thumb','sort_order',a.sort_order,'enabled',a.enabled,'version',a.version)`

func (s ProfileStore) Avatars(ctx context.Context, all bool) (map[string]any, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+avatarProjection+` FROM avatar_library a JOIN media_files m ON m.id=a.media_id WHERE ($1 OR a.enabled) AND m.status='ready' AND m.deleted_at IS NULL ORDER BY a.sort_order,a.id`, all)
	if err != nil {
		return nil, mediaDBError(err)
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var data map[string]any
		if err = rows.Scan(&data); err != nil {
			return nil, mediaDBError(err)
		}
		items = append(items, data)
	}
	return map[string]any{"data": items}, mediaDBError(rows.Err())
}
func (s ProfileStore) SaveAvatar(ctx context.Context, actor, id string, in profile.AvatarInput) (map[string]any, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, mediaDBError(err)
	}
	defer tx.Rollback(ctx)
	var adminID string
	// 先锁操作者，避免本人同时选头像时形成 user/library 的反向等待。
	if err = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 AND enabled AND role='admin' FOR SHARE`, actor).Scan(&adminID); err != nil {
		return nil, mediaDBError(err)
	}
	action := "avatar.update"
	if id == "" {
		// GC及取消上传同样锁media；登记库引用与完整图片检查必须同事务。
		var mediaID string
		err = tx.QueryRow(ctx, `SELECT id FROM media_files WHERE id=$1 AND owner_id=$2 AND purpose='avatar' AND status='ready' AND deleted_at IS NULL FOR UPDATE`, in.MediaID, actor).Scan(&mediaID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fault.New(fault.Invalid, "INVALID_AVATAR", "请选择本人上传且处理完成的头像")
		}
		if err != nil {
			return nil, mediaDBError(err)
		}
		var duplicate bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM avatar_library WHERE media_id=$1)`, mediaID).Scan(&duplicate); err != nil {
			return nil, mediaDBError(err)
		}
		if duplicate {
			return nil, fault.New(fault.Conflict, "AVATAR_EXISTS", "此图片已在头像库中")
		}
		id = s.NewID()
		action = "avatar.create"
		_, err = tx.Exec(ctx, `INSERT INTO avatar_library(id,name,media_id,sort_order,enabled,created_by) VALUES($1,$2,$3,$4,$5,$6)`, id, in.Name, mediaID, in.SortOrder, *in.Enabled, actor)
	} else {
		tag, e := tx.Exec(ctx, `UPDATE avatar_library SET name=$2,sort_order=$3,enabled=$4,version=version+1,updated_at=now() WHERE id=$1 AND version=$5`, id, in.Name, in.SortOrder, *in.Enabled, in.Version)
		err = e
		if err == nil && tag.RowsAffected() != 1 {
			return nil, fault.New(fault.Conflict, "AVATAR_CONFLICT", "头像库已被其他管理员修改，请刷新后重试")
		}
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO admin_logs(id,user_id,action,resource_id,detail) VALUES($1,$2,$3,$4,$5)`, s.NewID(), actor, action, id, map[string]any{"name": in.Name, "sort_order": in.SortOrder, "enabled": *in.Enabled})
	}
	var data map[string]any
	if err == nil {
		err = tx.QueryRow(ctx, `SELECT `+avatarProjection+` FROM avatar_library a WHERE a.id=$1`, id).Scan(&data)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return map[string]any{"data": data}, mediaDBError(err)
}
