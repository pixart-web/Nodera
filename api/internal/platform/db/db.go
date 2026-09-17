// Package db owns the PostgreSQL connection pool and the migration runner.
// PostgreSQL is Nodera's system of record (ADR-003) — nothing here is
// optional infrastructure.
package db

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Pool = pgxpool.Pool

// Connect opens a pgx connection pool. Callers must Close() it on shutdown.
func Connect(ctx context.Context, url string, maxConns int32) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: invalid database URL: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: failed to create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: failed to ping database: %w", err)
	}

	return pool, nil
}

// Migration is a single forward-only SQL migration. Nodera does not ship
// down-migrations for production safety (rule 23: never depend on manually
// reconstructed schema, and never encourage destructive rollback of applied
// schema changes in an automated tool).
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Migrate applies every migration in migrations whose version is greater
// than the highest version recorded in schema_migrations, in order, each
// inside its own transaction. It is idempotent and safe to run on every
// process start.
func Migrate(ctx context.Context, pool *Pool, migrations []Migration) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version     INTEGER PRIMARY KEY,
			name        TEXT NOT NULL,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("db: failed to create schema_migrations table: %w", err)
	}

	sorted := make([]Migration, len(migrations))
	copy(sorted, migrations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Version < sorted[j].Version })

	var applied int
	if err := pool.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&applied); err != nil {
		return fmt.Errorf("db: failed to read applied migration version: %w", err)
	}

	for _, m := range sorted {
		if m.Version <= applied {
			continue
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("db: failed to begin migration tx for %d_%s: %w", m.Version, m.Name, err)
		}
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("db: migration %d_%s failed: %w", m.Version, m.Name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`,
			m.Version, m.Name,
		); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("db: failed to record migration %d_%s: %w", m.Version, m.Name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("db: failed to commit migration %d_%s: %w", m.Version, m.Name, err)
		}
	}

	return nil
}

// ParseVersion extracts the leading integer from a migration filename like
// "0003_create_sessions.sql" -> 3.
func ParseVersion(filename string) (int, string, error) {
	name := strings.TrimSuffix(filename, ".sql")
	parts := strings.SplitN(name, "_", 2)
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("db: migration filename %q must be formatted NNNN_name.sql", filename)
	}
	v, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", fmt.Errorf("db: migration filename %q must start with a numeric version: %w", filename, err)
	}
	return v, parts[1], nil
}
