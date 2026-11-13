package postgres

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MediaReadAudit struct{ DB *pgxpool.Pool }

func (s MediaReadAudit) Start(ctx context.Context, id, actor, subject, version string, seconds int, reason string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO admin_logs(id,user_id,action,resource_id,detail) VALUES($1,$2,'security.media-read',$3,$4)`, id, actor, subject, map[string]any{"expected_version": version, "duration_seconds": seconds, "reason": reason, "outcome": "pending"})
	return err
}
func (s MediaReadAudit) Complete(ctx context.Context, id string, state string) error {
	_, err := s.DB.Exec(ctx, `UPDATE admin_logs SET detail=detail||jsonb_build_object('outcome',$2::text) WHERE id=$1 AND action='security.media-read'`, id, state)
	return err
}
