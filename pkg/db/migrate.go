// pkg/db/migrate.go
package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrate runs all *.sql files in migrationsDir in lexicographic order.
// Idempotent: each file is tracked in a migrations table.
func Migrate(ctx context.Context, pool *pgxpool.Pool, migrationsDir string) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("migrate: create tracking table: %w", err)
	}

	files, err := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	if err != nil {
		return fmt.Errorf("migrate: glob: %w", err)
	}
	sort.Strings(files)

	for _, f := range files {
		name := filepath.Base(f)
		var applied bool
		err := pool.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE filename=$1)", name,
		).Scan(&applied)
		if err != nil {
			return fmt.Errorf("migrate: check %s: %w", name, err)
		}
		if applied {
			continue
		}

		sql, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("migrate: read %s: %w", name, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("migrate: exec %s: %w", name, err)
		}
		if _, err := pool.Exec(ctx,
			"INSERT INTO schema_migrations(filename) VALUES($1)", name,
		); err != nil {
			return fmt.Errorf("migrate: record %s: %w", name, err)
		}
	}
	return nil
}
