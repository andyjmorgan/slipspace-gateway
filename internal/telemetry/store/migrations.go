package store

// migration is one forward-only schema step. version is monotonic and unique;
// name is human-facing bookkeeping recorded in schema_migrations.
type migration struct {
	version int
	name    string
	sql     string
}

// createSchemaMigrations bootstraps the bookkeeping table the runner reads
// before applying anything. Kept out of the numbered set so the runner can
// query the current version on a brand-new database.
const createSchemaMigrations = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);`

// migrations is the ordered set of schema steps. Append-only; never edit or
// renumber a shipped entry.
//
// 0001 lays down the telemetry store's three feeds:
//
//   - request_events — the lean, queryable per-request row both the gen_ai and
//     sluice OTLP feeds stitch into. Join key correlation_id; session_id groups
//     a session. This is the recent-history + drill-down surface.
//   - request_payloads — the heavy large-payload feed, ONE ROW PER ITEM (a kind
//     discriminator separates request body / response body / assembled-SSE
//     rollup), joined to an event by correlation_id. Webhook-pushed, HMAC-
//     trusted. Cross-instance order uses (ts_ns, instance_id, seq) — invariant
//     #8, never receive order.
//   - metric_points — raw OTLP numeric samples (the timeseries feed) the
//     dashboard aggregates into rate/token/latency panels.
var migrations = []migration{
	{
		version: 1,
		name:    "telemetry_core",
		sql: `
CREATE TABLE IF NOT EXISTS request_events (
    correlation_id        TEXT PRIMARY KEY,
    gateway_id            TEXT NOT NULL DEFAULT '',
    configuration         TEXT NOT NULL DEFAULT '',
    backend               TEXT NOT NULL DEFAULT '',
    model                 TEXT NOT NULL DEFAULT '',
    protocol              TEXT NOT NULL DEFAULT '',
    method                TEXT NOT NULL DEFAULT '',
    status_code           INT NOT NULL DEFAULT 0,
    upstream_status       INT NOT NULL DEFAULT 0,
    latency_ms            BIGINT NOT NULL DEFAULT 0,
    tokens_in             BIGINT NOT NULL DEFAULT 0,
    tokens_out            BIGINT NOT NULL DEFAULT 0,
    tokens_cached         BIGINT NOT NULL DEFAULT 0,
    tokens_cache_creation BIGINT NOT NULL DEFAULT 0,
    session_id            TEXT NOT NULL DEFAULT '',
    session_id_source     TEXT NOT NULL DEFAULT '',
    api_key_name          TEXT NOT NULL DEFAULT '',
    policy_ref            TEXT NOT NULL DEFAULT '',
    streaming             BOOLEAN NOT NULL DEFAULT FALSE,
    gen_ai_content        JSONB,
    detail                JSONB,
    observed_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS request_events_observed_at ON request_events (observed_at DESC);

-- Keyset pagination and the dashboard time-range scans both order by
-- (observed_at DESC, correlation_id DESC); the composite serves the page seek
-- and the stats range scan without a separate sort.
CREATE INDEX IF NOT EXISTS request_events_observed_corr
    ON request_events (observed_at DESC, correlation_id DESC);

-- The dashboard filters (configuration / backend / model / gateway / protocol)
-- almost always pair with a time range, so put observed_at first to keep the
-- range bound index-driven and let the equality filters narrow within it.
CREATE INDEX IF NOT EXISTS request_events_filter
    ON request_events (observed_at DESC, configuration, backend, model, gateway_id, protocol);

CREATE INDEX IF NOT EXISTS request_events_session ON request_events (session_id);

CREATE TABLE IF NOT EXISTS request_payloads (
    correlation_id TEXT NOT NULL,
    kind           TEXT NOT NULL,
    instance_id    TEXT NOT NULL DEFAULT '',
    seq            BIGINT NOT NULL DEFAULT 0,
    ts_ns          BIGINT NOT NULL DEFAULT 0,
    gateway_id     TEXT NOT NULL DEFAULT '',
    body           BYTEA NOT NULL,
    captured_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (correlation_id, kind, instance_id, seq)
);

CREATE INDEX IF NOT EXISTS request_payloads_correlation ON request_payloads (correlation_id);

CREATE TABLE IF NOT EXISTS metric_points (
    metric_name TEXT NOT NULL,
    labels      JSONB NOT NULL DEFAULT '{}'::jsonb,
    value       DOUBLE PRECISION NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS metric_points_name_time ON metric_points (metric_name, observed_at DESC);`,
	},
	{
		version: 2,
		name:    "rename_backend_to_provider",
		// Vocabulary unification: the configured upstream is "provider"
		// everywhere else (config schema, admin API, gen_ai OTel label), so the
		// telemetry dimension follows. Hard cut — no dual-read window; the
		// gateway emits sluice.provider and ingest reads provider after this.
		// Postgres rewrites the request_events_filter index definition to the
		// new column name automatically, so only the rename is needed.
		sql: `ALTER TABLE request_events RENAME COLUMN backend TO provider;`,
	},
}
