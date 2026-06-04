// Package store is the telemetry service's Postgres layer: a pgx connection
// pool plus a minimal forward-only migration runner. T1 stands up the pool,
// the schema_migrations bookkeeping, and the lean request_events table; the
// large-payload and timeseries tables (and the query methods over them) land
// in later phases.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps the pgx pool. It owns the connection lifecycle and the schema.
type Store struct {
	// pool is the shared connection pool; pgxpool is safe for concurrent use.
	pool *pgxpool.Pool
}

// Open dials Postgres with the given DSN and verifies connectivity with a
// ping. The caller owns the returned Store and must Close it.
func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Ping verifies the pool can reach Postgres. Used by the readiness probe.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("store: ping: %w", err)
	}
	return nil
}

// Close releases the pool. Safe to call once.
func (s *Store) Close() {
	s.pool.Close()
}

// Migrate applies every migration whose version is newer than the highest
// recorded in schema_migrations, in order, each in its own transaction. It is
// idempotent: a fully-migrated database is a no-op.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, createSchemaMigrations); err != nil {
		return fmt.Errorf("store: ensure schema_migrations: %w", err)
	}

	var current int
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current)
	if err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := s.applyMigration(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration runs one migration's SQL and records it, atomically. A failed
// migration rolls back so schema_migrations never claims a half-applied step.
func (s *Store) applyMigration(ctx context.Context, m migration) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin migration %d: %w", m.version, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, m.sql); err != nil {
		return fmt.Errorf("store: apply migration %d (%s): %w", m.version, m.name, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, m.version, m.name); err != nil {
		return fmt.Errorf("store: record migration %d: %w", m.version, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit migration %d: %w", m.version, err)
	}
	return nil
}

// SchemaVersion returns the highest applied migration version, or 0 if none.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 0, err
		}
		return 0, fmt.Errorf("store: schema version: %w", err)
	}
	return v, nil
}
