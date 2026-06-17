// Package store is the Arbiter's Postgres layer: a pgx connection
// pool (behind a narrow querier interface) plus a forward-only migration
// runner, and the typed accessors over request_events, request_payloads, and
// metric_points.
//
// The store depends on the querier interface, not *pgxpool.Pool directly, so
// the query/scan/error-wrap logic is unit-testable with a hand-rolled fake (the
// same fake-the-boundary pattern the s3/azureblob connectors use); the real SQL
// is exercised through Postgres in test/e2e/telemetry.
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// rows is the subset of pgx.Rows the store consumes.
type rows interface {
	Next() bool
	Scan(dest ...any) error
	Close()
	Err() error
}

// txn is the subset of pgx.Tx the store consumes.
type txn interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// querier is the store's database dependency: the slice of *pgxpool.Pool the
// store actually calls. Abstracted so the store is testable without a live
// Postgres.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) rowScanner
	Begin(ctx context.Context) (txn, error)
	Ping(ctx context.Context) error
	Close()
}

// Store wraps the querier. It owns the connection lifecycle and the schema.
type Store struct {
	// db is the database dependency (a *pgxpool.Pool adapter in production).
	db querier
}

// poolQuerier adapts *pgxpool.Pool to the querier interface. The pgx concrete
// return types (pgx.Rows, pgx.Row, pgx.Tx) structurally satisfy the narrow
// interfaces, so the delegations are one-liners.
type poolQuerier struct{ pool *pgxpool.Pool }

func (p poolQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return p.pool.Exec(ctx, sql, args...)
}

func (p poolQuerier) Query(ctx context.Context, sql string, args ...any) (rows, error) {
	return p.pool.Query(ctx, sql, args...)
}

func (p poolQuerier) QueryRow(ctx context.Context, sql string, args ...any) rowScanner {
	return p.pool.QueryRow(ctx, sql, args...)
}

func (p poolQuerier) Begin(ctx context.Context) (txn, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err // avoid wrapping a nil pgx.Tx in a non-nil interface
	}
	return tx, nil
}

func (p poolQuerier) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }
func (p poolQuerier) Close()                         { p.pool.Close() }

// newStore wraps a querier. Used by Open and by unit tests with a fake.
func newStore(db querier) *Store { return &Store{db: db} }

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
	return newStore(poolQuerier{pool: pool}), nil
}

// Ping verifies the store can reach Postgres. Used by the readiness probe.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.db.Ping(ctx); err != nil {
		return fmt.Errorf("store: ping: %w", err)
	}
	return nil
}

// Close releases the pool. Safe to call once.
func (s *Store) Close() { s.db.Close() }

// Migrate applies every migration whose version is newer than the highest
// recorded in schema_migrations, in order, each in its own transaction. It is
// idempotent: a fully-migrated database is a no-op.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.Exec(ctx, createSchemaMigrations); err != nil {
		return fmt.Errorf("store: ensure schema_migrations: %w", err)
	}

	current, err := s.SchemaVersion(ctx)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		apply := s.applyMigration
		if m.noTx {
			apply = s.applyMigrationNoTx
		}
		if err := apply(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration runs one migration's SQL and records it, atomically. A failed
// migration rolls back so schema_migrations never claims a half-applied step.
func (s *Store) applyMigration(ctx context.Context, m migration) error {
	tx, err := s.db.Begin(ctx)
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
	return commit(ctx, tx)
}

// applyMigrationNoTx runs a migration's statements with autocommit (no
// enclosing transaction) — required for TimescaleDB continuous-aggregate DDL,
// which cannot run inside a transaction block. Statements are split on `;` and
// executed in order; the version row is recorded only after all succeed, so a
// mid-batch failure re-runs the whole (idempotent) step rather than leaving
// schema_migrations claiming a half-applied version.
func (s *Store) applyMigrationNoTx(ctx context.Context, m migration) error {
	for _, stmt := range splitStatements(m.sql) {
		if _, err := s.db.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("store: apply migration %d (%s): %w", m.version, m.name, err)
		}
	}
	if _, err := s.db.Exec(ctx,
		`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, m.version, m.name); err != nil {
		return fmt.Errorf("store: record migration %d: %w", m.version, err)
	}
	return nil
}

// splitStatements breaks a semicolon-separated SQL batch into individual
// statements, trimming whitespace and dropping empties. noTx migrations carry
// no embedded semicolons (no dollar-quoted bodies/strings), so a plain split is
// safe — see the migration.noTx contract.
func splitStatements(sql string) []string {
	parts := strings.Split(sql, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// SchemaVersion returns the highest applied migration version, or 0 if none.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 0, err
		}
		return 0, fmt.Errorf("store: schema version: %w", err)
	}
	return v, nil
}

// ensure the pool adapter satisfies querier at compile time.
var _ querier = poolQuerier{}
