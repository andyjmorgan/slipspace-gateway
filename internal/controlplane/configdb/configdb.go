// Package configdb is the control plane's Postgres-backed configuration store.
//
// It models config as editable entities plus immutable published snapshots:
//
//   - config_entities — the live, editable working state: one row per entity
//     (a backend, group, configuration, api-key, rule, connector), body stored
//     as the contracts/config type in JSON. CRUD maps 1:1 to the admin API.
//   - config_entity_changes — an append-only audit trail (who changed which
//     entity, old/new body).
//   - published_versions — immutable, content-addressed whole-config snapshots.
//     Editing mutates entities; publishing composes them into a versioned
//     document the control plane actually serves. Exactly one version is active;
//     rollback re-activates an older one (CP-1 immutability preserved).
//
// configdb never interprets a body — entity bodies are opaque JSON, version
// bodies are opaque serialized v2 documents. Composition (entities -> document)
// lives in the control-plane layer that knows the contracts types. Schema is
// auto-migrated on Open.
package configdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/receipt"
)

// Sentinel errors.
var (
	ErrNoActiveConfig  = errors.New("configdb: no active config version")
	ErrVersionNotFound = errors.New("configdb: version not found")
	ErrEntityNotFound  = errors.New("configdb: entity not found")
	ErrAdminNotFound   = errors.New("configdb: admin user not found")
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS config_entities (
    kind       TEXT NOT NULL,
    name       TEXT NOT NULL,
    body       JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (kind, name)
);

CREATE TABLE IF NOT EXISTS config_entity_changes (
    id         BIGSERIAL PRIMARY KEY,
    kind       TEXT NOT NULL,
    name       TEXT NOT NULL,
    op         TEXT NOT NULL,
    old_body   JSONB,
    new_body   JSONB,
    changed_by TEXT NOT NULL DEFAULT '',
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS published_versions (
    id           TEXT PRIMARY KEY,
    content_hash TEXT NOT NULL,
    body         BYTEA NOT NULL,
    published_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_by TEXT NOT NULL DEFAULT '',
    parent_id    TEXT,
    active       BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE UNIQUE INDEX IF NOT EXISTS published_versions_single_active
    ON published_versions (active) WHERE active;

CREATE TABLE IF NOT EXISTS fleet_registry (
    gateway_id           TEXT PRIMARY KEY,
    version              TEXT NOT NULL DEFAULT '',
    labels               JSONB NOT NULL DEFAULT '{}'::jsonb,
    cached_config_hashes JSONB NOT NULL DEFAULT '[]'::jsonb,
    registered_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS cp_admins (
    username      TEXT PRIMARY KEY,
    password_hash TEXT NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS receipts (
    gateway_id     TEXT NOT NULL,
    seq            BIGINT NOT NULL,
    correlation_id TEXT NOT NULL DEFAULT '',
    prev_hash      BYTEA,
    hash           BYTEA NOT NULL,
    signature      BYTEA NOT NULL,
    key_id         TEXT NOT NULL,
    body_hash      TEXT NOT NULL DEFAULT '',
    payload        BYTEA NOT NULL,
    received_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (gateway_id, seq)
);
`

// Entity is one editable config entity. Body is the contracts/config type as JSON.
type Entity struct {
	Kind      string
	Name      string
	Body      []byte
	UpdatedAt time.Time
	UpdatedBy string
}

// Change is one audit-log row.
type Change struct {
	Kind      string
	Name      string
	Op        string
	OldBody   []byte
	NewBody   []byte
	ChangedBy string
	ChangedAt time.Time
}

// Version is a published snapshot with its body.
type Version struct {
	ID          string
	Hash        string
	Body        []byte
	PublishedAt time.Time
	PublishedBy string
	ParentID    *string
}

// VersionMeta is a published snapshot without its body.
type VersionMeta struct {
	ID          string
	Hash        string
	PublishedAt time.Time
	PublishedBy string
	Active      bool
}

// DB is a Postgres-backed config store.
type DB struct {
	pool *pgxpool.Pool
}

// Open connects to dsn, auto-migrates the schema, and returns a ready store.
func Open(ctx context.Context, dsn string) (*DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("configdb: connect: %w", err)
	}
	db := &DB{pool: pool}
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		pool.Close()
		return nil, fmt.Errorf("configdb: migrate: %w", err)
	}
	return db, nil
}

// Close releases the connection pool.
func (db *DB) Close() { db.pool.Close() }

// ---- entity layer ----

// UpsertEntity creates or replaces an entity and records the change.
func (db *DB) UpsertEntity(ctx context.Context, kind, name string, body []byte, by string) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("configdb: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var old []byte
	err = tx.QueryRow(ctx, `SELECT body FROM config_entities WHERE kind=$1 AND name=$2`, kind, name).Scan(&old)
	op := "update"
	if errors.Is(err, pgx.ErrNoRows) {
		op = "create"
		old = nil
	} else if err != nil {
		return fmt.Errorf("configdb: read entity: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO config_entities (kind, name, body, updated_by) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (kind, name) DO UPDATE SET body=$3, updated_by=$4, updated_at=now()`,
		kind, name, body, by,
	); err != nil {
		return fmt.Errorf("configdb: upsert entity: %w", err)
	}
	if err := recordChange(ctx, tx, kind, name, op, old, body, by); err != nil {
		return err
	}
	return commit(ctx, tx)
}

// GetEntity returns one entity, or ErrEntityNotFound.
func (db *DB) GetEntity(ctx context.Context, kind, name string) (Entity, error) {
	var e Entity
	err := db.pool.QueryRow(ctx,
		`SELECT kind, name, body, updated_at, updated_by FROM config_entities WHERE kind=$1 AND name=$2`,
		kind, name,
	).Scan(&e.Kind, &e.Name, &e.Body, &e.UpdatedAt, &e.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return Entity{}, ErrEntityNotFound
	}
	if err != nil {
		return Entity{}, fmt.Errorf("configdb: get entity: %w", err)
	}
	return e, nil
}

// ListEntities returns all entities ordered by (kind, name).
func (db *DB) ListEntities(ctx context.Context) ([]Entity, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT kind, name, body, updated_at, updated_by FROM config_entities ORDER BY kind, name`)
	if err != nil {
		return nil, fmt.Errorf("configdb: list entities: %w", err)
	}
	defer rows.Close()

	var out []Entity
	for rows.Next() {
		var e Entity
		if err := rows.Scan(&e.Kind, &e.Name, &e.Body, &e.UpdatedAt, &e.UpdatedBy); err != nil {
			return nil, fmt.Errorf("configdb: scan entity: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteEntity removes an entity and records the change. ErrEntityNotFound when absent.
func (db *DB) DeleteEntity(ctx context.Context, kind, name, by string) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("configdb: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var old []byte
	err = tx.QueryRow(ctx, `SELECT body FROM config_entities WHERE kind=$1 AND name=$2`, kind, name).Scan(&old)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrEntityNotFound
	}
	if err != nil {
		return fmt.Errorf("configdb: read entity: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM config_entities WHERE kind=$1 AND name=$2`, kind, name); err != nil {
		return fmt.Errorf("configdb: delete entity: %w", err)
	}
	if err := recordChange(ctx, tx, kind, name, "delete", old, nil, by); err != nil {
		return err
	}
	return commit(ctx, tx)
}

// ListChanges returns the audit log newest-first, capped at limit (0 = no cap).
func (db *DB) ListChanges(ctx context.Context, limit int) ([]Change, error) {
	q := `SELECT kind, name, op, old_body, new_body, changed_by, changed_at
	      FROM config_entity_changes ORDER BY id DESC`
	args := []any{}
	if limit > 0 {
		q += ` LIMIT $1`
		args = append(args, limit)
	}
	rows, err := db.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("configdb: list changes: %w", err)
	}
	defer rows.Close()

	var out []Change
	for rows.Next() {
		var c Change
		if err := rows.Scan(&c.Kind, &c.Name, &c.Op, &c.OldBody, &c.NewBody, &c.ChangedBy, &c.ChangedAt); err != nil {
			return nil, fmt.Errorf("configdb: scan change: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- published-version layer ----

// Publish stores a composed config document as an immutable, content-addressed
// snapshot and makes it the sole active version. An identical body reuses the
// existing snapshot rather than duplicating it.
func (db *DB) Publish(ctx context.Context, body []byte, by string) (Version, error) {
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])

	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return Version{}, fmt.Errorf("configdb: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id string
	err = tx.QueryRow(ctx, `SELECT id FROM published_versions WHERE content_hash=$1 LIMIT 1`, hash).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		parent, perr := activeVersionID(ctx, tx)
		if perr != nil {
			return Version{}, perr
		}
		id = uuid.NewString()
		if _, ierr := tx.Exec(ctx,
			`INSERT INTO published_versions (id, content_hash, body, published_by, parent_id, active)
			 VALUES ($1, $2, $3, $4, $5, FALSE)`,
			id, hash, body, by, parent,
		); ierr != nil {
			return Version{}, fmt.Errorf("configdb: insert version: %w", ierr)
		}
	case err != nil:
		return Version{}, fmt.Errorf("configdb: lookup hash: %w", err)
	}

	if err := activateVersionTx(ctx, tx, id); err != nil {
		return Version{}, err
	}
	if err := commit(ctx, tx); err != nil {
		return Version{}, err
	}
	return db.versionByID(ctx, id)
}

// ActiveVersion returns the active published snapshot, or ErrNoActiveConfig.
func (db *DB) ActiveVersion(ctx context.Context) (Version, error) {
	var v Version
	err := db.pool.QueryRow(ctx,
		`SELECT id, content_hash, body, published_at, published_by, parent_id
		 FROM published_versions WHERE active LIMIT 1`,
	).Scan(&v.ID, &v.Hash, &v.Body, &v.PublishedAt, &v.PublishedBy, &v.ParentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, ErrNoActiveConfig
	}
	if err != nil {
		return Version{}, fmt.Errorf("configdb: active version: %w", err)
	}
	return v, nil
}

// ActivateVersion makes an existing snapshot the sole active one (rollback).
func (db *DB) ActivateVersion(ctx context.Context, id string) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("configdb: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var exists string
	err = tx.QueryRow(ctx, `SELECT id FROM published_versions WHERE id=$1`, id).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrVersionNotFound
	}
	if err != nil {
		return fmt.Errorf("configdb: lookup version: %w", err)
	}
	if err := activateVersionTx(ctx, tx, id); err != nil {
		return err
	}
	return commit(ctx, tx)
}

// ListVersions returns the published history newest-first.
func (db *DB) ListVersions(ctx context.Context) ([]VersionMeta, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT id, content_hash, published_at, published_by, active
		 FROM published_versions ORDER BY published_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("configdb: list versions: %w", err)
	}
	defer rows.Close()

	var out []VersionMeta
	for rows.Next() {
		var m VersionMeta
		if err := rows.Scan(&m.ID, &m.Hash, &m.PublishedAt, &m.PublishedBy, &m.Active); err != nil {
			return nil, fmt.Errorf("configdb: scan version: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (db *DB) versionByID(ctx context.Context, id string) (Version, error) {
	var v Version
	if err := db.pool.QueryRow(ctx,
		`SELECT id, content_hash, body, published_at, published_by, parent_id
		 FROM published_versions WHERE id=$1`, id,
	).Scan(&v.ID, &v.Hash, &v.Body, &v.PublishedAt, &v.PublishedBy, &v.ParentID); err != nil {
		return Version{}, fmt.Errorf("configdb: version by id: %w", err)
	}
	return v, nil
}

// ---- helpers ----

// querier is the subset of pgx shared by pool and transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func recordChange(ctx context.Context, q querier, kind, name, op string, oldBody, newBody []byte, by string) error {
	if _, err := q.Exec(ctx,
		`INSERT INTO config_entity_changes (kind, name, op, old_body, new_body, changed_by)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		kind, name, op, nullJSON(oldBody), nullJSON(newBody), by,
	); err != nil {
		return fmt.Errorf("configdb: record change: %w", err)
	}
	return nil
}

// nullJSON returns nil for an empty body so it lands as SQL NULL in a JSONB
// column (an empty []byte is not valid JSON).
func nullJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func activeVersionID(ctx context.Context, q querier) (*string, error) {
	var id string
	err := q.QueryRow(ctx, `SELECT id FROM published_versions WHERE active LIMIT 1`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("configdb: active version id: %w", err)
	}
	return &id, nil
}

func activateVersionTx(ctx context.Context, q querier, id string) error {
	if _, err := q.Exec(ctx, `UPDATE published_versions SET active=FALSE WHERE active AND id<>$1`, id); err != nil {
		return fmt.Errorf("configdb: clear active: %w", err)
	}
	if _, err := q.Exec(ctx, `UPDATE published_versions SET active=TRUE WHERE id=$1`, id); err != nil {
		return fmt.Errorf("configdb: set active: %w", err)
	}
	return nil
}

func commit(ctx context.Context, tx pgx.Tx) error {
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("configdb: commit: %w", err)
	}
	return nil
}

// FleetGateway is one fleet member's persisted state in fleet_registry. It is
// the shared, replica-independent source of truth for the fleet, replacing the
// per-process in-memory registry so every CP replica sees one consistent view.
type FleetGateway struct {
	ID                 string
	Version            string
	Labels             map[string]string
	CachedConfigHashes []string
	RegisteredAt       time.Time
	LastSeen           time.Time
}

const fleetReturning = `gateway_id, version, labels, cached_config_hashes, registered_at, last_seen`

// RegisterGateway records a gateway's first appearance or refreshes an existing
// one (idempotent on gateway_id). registered_at is preserved across refreshes;
// last_seen is stamped server-side so every replica shares one clock. A
// register does not touch cached_config_hashes (only heartbeats carry those).
func (db *DB) RegisterGateway(ctx context.Context, id, version string, labels map[string]string) (FleetGateway, error) {
	labelsJSON, err := marshalJSONObject(labels)
	if err != nil {
		return FleetGateway{}, err
	}
	row := db.pool.QueryRow(ctx, `
INSERT INTO fleet_registry (gateway_id, version, labels, last_seen, registered_at)
VALUES ($1, $2, $3, now(), now())
ON CONFLICT (gateway_id) DO UPDATE
  SET version = EXCLUDED.version,
      labels  = EXCLUDED.labels,
      last_seen = now()
RETURNING `+fleetReturning, id, version, labelsJSON)
	return scanFleetGateway(row)
}

// HeartbeatGateway updates liveness plus mutable state, self-registering an
// unknown gateway so a replica that never saw the original Register still
// converges on the next heartbeat round.
func (db *DB) HeartbeatGateway(ctx context.Context, id, version string, labels map[string]string, hashes []string) (FleetGateway, error) {
	labelsJSON, err := marshalJSONObject(labels)
	if err != nil {
		return FleetGateway{}, err
	}
	hashesJSON, err := marshalJSONArray(hashes)
	if err != nil {
		return FleetGateway{}, err
	}
	row := db.pool.QueryRow(ctx, `
INSERT INTO fleet_registry (gateway_id, version, labels, cached_config_hashes, last_seen, registered_at)
VALUES ($1, $2, $3, $4, now(), now())
ON CONFLICT (gateway_id) DO UPDATE
  SET version = EXCLUDED.version,
      labels  = EXCLUDED.labels,
      cached_config_hashes = EXCLUDED.cached_config_hashes,
      last_seen = now()
RETURNING `+fleetReturning, id, version, labelsJSON, hashesJSON)
	return scanFleetGateway(row)
}

// ListGateways returns every known gateway ordered by id, for stable rendering.
func (db *DB) ListGateways(ctx context.Context) ([]FleetGateway, error) {
	rows, err := db.pool.Query(ctx, `SELECT `+fleetReturning+` FROM fleet_registry ORDER BY gateway_id`)
	if err != nil {
		return nil, fmt.Errorf("configdb: list gateways: %w", err)
	}
	defer rows.Close()

	var out []FleetGateway
	for rows.Next() {
		g, serr := scanFleetGateway(rows)
		if serr != nil {
			return nil, serr
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// rowScanner is satisfied by both pgx.Row and pgx.Rows, so one scan helper
// serves the single-row upserts and the List query.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanFleetGateway(s rowScanner) (FleetGateway, error) {
	var (
		g          FleetGateway
		labelsJSON []byte
		hashesJSON []byte
	)
	if err := s.Scan(&g.ID, &g.Version, &labelsJSON, &hashesJSON, &g.RegisteredAt, &g.LastSeen); err != nil {
		return FleetGateway{}, fmt.Errorf("configdb: scan gateway: %w", err)
	}
	if err := json.Unmarshal(labelsJSON, &g.Labels); err != nil {
		return FleetGateway{}, fmt.Errorf("configdb: unmarshal labels: %w", err)
	}
	if err := json.Unmarshal(hashesJSON, &g.CachedConfigHashes); err != nil {
		return FleetGateway{}, fmt.Errorf("configdb: unmarshal hashes: %w", err)
	}
	return g, nil
}

func marshalJSONObject(m map[string]string) ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("configdb: marshal labels: %w", err)
	}
	return b, nil
}

func marshalJSONArray(s []string) ([]byte, error) {
	if s == nil {
		return []byte("[]"), nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("configdb: marshal hashes: %w", err)
	}
	return b, nil
}

const receiptColumns = `gateway_id, seq, correlation_id, prev_hash, hash, signature, key_id, body_hash, payload`

// AppendReceipt extends a gateway's tamper-evidence chain by one record: it
// reads the chain tail, builds the next signed receipt (receipt.Chain), and
// inserts it — all under a per-gateway transaction-scoped advisory lock so
// concurrent appends for the same gateway cannot fork the chain. The caller
// supplies the record without Seq; AppendReceipt assigns it.
func (db *DB) AppendReceipt(ctx context.Context, rec receipt.Record, signer receipt.Signer) (receipt.Receipt, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return receipt.Receipt{}, fmt.Errorf("configdb: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Serialize this gateway's chain so two appends can't read the same tail.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`, rec.GatewayID); err != nil {
		return receipt.Receipt{}, fmt.Errorf("configdb: lock chain: %w", err)
	}

	var (
		prevHash []byte
		lastSeq  int64
	)
	err = tx.QueryRow(ctx,
		`SELECT hash, seq FROM receipts WHERE gateway_id=$1 ORDER BY seq DESC LIMIT 1`, rec.GatewayID).
		Scan(&prevHash, &lastSeq)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		prevHash, lastSeq = nil, 0
	case err != nil:
		return receipt.Receipt{}, fmt.Errorf("configdb: read chain tail: %w", err)
	}

	rec.Seq = uint64(lastSeq + 1) //nolint:gosec // chain seq is app-assigned, always >= 1
	r := receipt.Chain(prevHash, rec, signer)

	seqArg := int64(r.Seq) //nolint:gosec // chain seq is app-assigned, always >= 1
	if _, err := tx.Exec(ctx,
		`INSERT INTO receipts (`+receiptColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		r.GatewayID, seqArg, r.CorrelationID, r.PrevHash, r.Hash, r.Signature, r.KeyID, r.BodyHash, r.Payload); err != nil {
		return receipt.Receipt{}, fmt.Errorf("configdb: insert receipt: %w", err)
	}
	if err := commit(ctx, tx); err != nil {
		return receipt.Receipt{}, err
	}
	return r, nil
}

// ListReceipts returns a gateway's chain in seq order, ready for
// receipt.VerifyChain.
func (db *DB) ListReceipts(ctx context.Context, gatewayID string) ([]receipt.Receipt, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT `+receiptColumns+` FROM receipts WHERE gateway_id=$1 ORDER BY seq`, gatewayID)
	if err != nil {
		return nil, fmt.Errorf("configdb: list receipts: %w", err)
	}
	defer rows.Close()

	var out []receipt.Receipt
	for rows.Next() {
		var (
			r   receipt.Receipt
			seq int64
		)
		if err := rows.Scan(&r.GatewayID, &seq, &r.CorrelationID, &r.PrevHash, &r.Hash, &r.Signature, &r.KeyID, &r.BodyHash, &r.Payload); err != nil {
			return nil, fmt.Errorf("configdb: scan receipt: %w", err)
		}
		r.Seq = uint64(seq) //nolint:gosec // chain seq from BIGINT PK, always >= 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetAdminHash returns the stored password hash for username, or
// ErrAdminNotFound if there is no such admin. The hash is the source of truth
// for console auth, shared across all CP replicas.
func (db *DB) GetAdminHash(ctx context.Context, username string) (string, error) {
	var hash string
	err := db.pool.QueryRow(ctx, `SELECT password_hash FROM cp_admins WHERE username=$1`, username).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrAdminNotFound
	}
	if err != nil {
		return "", fmt.Errorf("configdb: get admin hash: %w", err)
	}
	return hash, nil
}

// SeedAdmin inserts the admin only if one does not already exist for username,
// and reports whether it actually seeded. Idempotent and race-safe across
// replicas: concurrent first-boots resolve to a single winning insert, the
// rest observe seeded=false and use the existing row.
func (db *DB) SeedAdmin(ctx context.Context, username, passwordHash string) (bool, error) {
	tag, err := db.pool.Exec(ctx,
		`INSERT INTO cp_admins (username, password_hash) VALUES ($1, $2)
         ON CONFLICT (username) DO NOTHING`, username, passwordHash)
	if err != nil {
		return false, fmt.Errorf("configdb: seed admin: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
