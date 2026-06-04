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
// 0001 seeds request_events — the lean per-request metadata row that both the
// gen_ai and sluice feeds stitch into (join key correlation_id, session_id
// groups a session). The large-payload-per-item and timeseries rollup tables
// arrive in a later phase alongside their ingest paths.
var migrations = []migration{
	{
		version: 1,
		name:    "request_events",
		sql: `
CREATE TABLE IF NOT EXISTS request_events (
    correlation_id TEXT PRIMARY KEY,
    session_id     TEXT,
    gateway_id     TEXT NOT NULL,
    protocol       TEXT,
    backend        TEXT,
    model          TEXT,
    status         INTEGER,
    latency_ms     BIGINT,
    input_tokens   BIGINT,
    output_tokens  BIGINT,
    total_tokens   BIGINT,
    labels         JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- ts_ns + instance_id + seq form the stable composite ordering key
    -- (invariant #8); cross-feed/cross-instance order is never receive order.
    ts_ns          BIGINT NOT NULL,
    instance_id    TEXT NOT NULL DEFAULT '',
    seq            BIGINT NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS request_events_session_idx ON request_events (session_id);
CREATE INDEX IF NOT EXISTS request_events_order_idx   ON request_events (ts_ns, instance_id, seq);
CREATE INDEX IF NOT EXISTS request_events_gateway_idx ON request_events (gateway_id);`,
	},
}
