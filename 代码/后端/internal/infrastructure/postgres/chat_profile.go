package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"qingye/backend/internal/domain/chat"
	"qingye/backend/internal/domain/fault"
)

func (s *ChatStore) Profile(ctx context.Context, actor chat.Actor, in *chat.ProfileInput) (map[string]any, error) {
	if in != nil {
		tx, err := s.DB.Begin(ctx)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback(ctx)
		var userID string
		if err = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, actor.ID).Scan(&userID); err != nil {
			return nil, err
		}
		if *in.AvatarID != "" {
			var mid string
			err = tx.QueryRow(ctx, `SELECT m.id FROM avatar_library a JOIN media_files m ON m.id=a.media_id WHERE m.id=$1 AND a.enabled AND m.purpose='avatar' AND m.status='ready' AND m.deleted_at IS NULL FOR SHARE OF a,m`, *in.AvatarID).Scan(&mid)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fault.New(fault.Invalid, "INVALID_AVATAR", "请选择头像库中可用的头像")
			}
			if err != nil {
				return nil, err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE users SET avatar_media_id=NULLIF($2,'') WHERE id=$1`, actor.ID, *in.AvatarID); err != nil {
			return nil, err
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
	}
	rows, err := s.chatJSONRows(ctx, `SELECT jsonb_build_object('nickname',nickname,'avatar_url',coalesce('/api/v1/media/'||avatar_media_id||'/file?variant=thumb','')) FROM users WHERE id=$1`, actor.ID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fault.New(fault.NotFound, "NOT_FOUND", "账户不存在")
	}
	return map[string]any{"data": rows[0]}, nil
}
