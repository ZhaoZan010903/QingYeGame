package maintenance

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Data migrations share the schema transaction and its advisory lock. A recorded
// migration is never replayed, so later administrator corrections stay intact.
//
//go:embed migrations/*.sql
var dataMigrations embed.FS

func ApplyDataMigrations(ctx context.Context, tx pgx.Tx) error {
	entries, err := dataMigrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		var applied bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)", entry.Name()).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		sql, err := dataMigrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("migration %s: %w", entry.Name(), err)
		}
		if _, err = tx.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES($1)", entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

//go:embed schema.sql
var schema string

// 结构和数据迁移共享事务锁，避免双进程重复执行。
func (s *Service) Migrate(ctx context.Context) error {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(1834179)"); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, schema); e != nil {
		return e
	}
	if e = ApplyDataMigrations(ctx, tx); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
