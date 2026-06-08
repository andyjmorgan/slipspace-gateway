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
// 0001–0005 laid down the original three-feed model (request_events upserted by
// both OTLP and Record, request_payloads, metric_points) and the identity
// columns. 0006 re-architects to the single-writer model (Telemetry
// Rearchitecture design note): the OTel span owns request_events outright
// (complete span in span_event + projected columns), the Record feed lands a
// lazy verbatim blob in `record`, and request_payloads is dropped. metric_points
// is the timeseries feed the dashboard aggregates; it is unchanged here.
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
	{
		version: 3,
		name:    "tags_gin_index",
		// The message browser filters by post-rule tags (detail->'tags' @> ...)
		// and enumerates the distinct tag set for its dropdown. A GIN index on
		// the tags path makes both the containment filter and the
		// jsonb_array_elements_text scan index-backed instead of a full table
		// scan as the event table grows.
		sql: `CREATE INDEX IF NOT EXISTS request_events_tags ON request_events USING gin ((detail->'tags'));`,
	},
	{
		version: 4,
		name:    "add_agent_id",
		// Agent identification rides alongside session bundling: the gateway
		// resolves an agent id (X-Sluice-Agent-Id / X-Claude-Code-Agent-Id /
		// custom) and emits it as gen_ai.agent.id on the span and agent_id on
		// the Record. Additive columns + a single-column index so the message
		// browser can drill down by agent, mirroring request_events_session.
		sql: `
ALTER TABLE request_events ADD COLUMN IF NOT EXISTS agent_id        TEXT NOT NULL DEFAULT '';
ALTER TABLE request_events ADD COLUMN IF NOT EXISTS agent_id_source TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS request_events_agent ON request_events (agent_id);`,
	},
	{
		version: 5,
		name:    "add_user_id",
		// End-user identification rides the same rails as session/agent: the
		// gateway resolves a user id (X-Sluice-User-Id / custom) and emits it as
		// enduser.id on the span and user_id on the Record. Additive columns + a
		// single-column index so the message browser can drill down by end user,
		// mirroring request_events_agent.
		sql: `
ALTER TABLE request_events ADD COLUMN IF NOT EXISTS user_id        TEXT NOT NULL DEFAULT '';
ALTER TABLE request_events ADD COLUMN IF NOT EXISTS user_id_source TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS request_events_user ON request_events (user_id);`,
	},
	{
		version: 6,
		name:    "single_writer_span_event",
		// Re-architecture (Telemetry Rearchitecture design note): the OTel span
		// feed becomes the SINGLE writer of request_events; the Record feed stops
		// writing the entity and lands a lazy verbatim blob in `record`.
		//
		// request_events is rebuilt around an immutable `span_event JSONB` that
		// holds the COMPLETE span (every attribute + the gen_ai content). The
		// scalar columns (provider/model/configuration/protocol/status_code,
		// the identity ids) are a materialized index over that blob —
		// projected at ingest, backfillable by re-projecting from span_event,
		// never the source of truth. The measurement columns (latency_ms,
		// tokens_*, the two id_source columns, api_key_name, policy_ref,
		// streaming) and the gen_ai_content / detail JSONB columns are dropped:
		// they now live inside span_event. tags / rules_fired live in the blob,
		// filtered via GIN expression indexes on (span_event->'tags') and
		// (span_event->'rules_fired'), faceted via jsonb_array_elements_text.
		//
		// request_payloads is dropped — the Record's bodies/headers ride the
		// verbatim `record.body` blob, deserialized lazily when the record tab
		// opens. metric_points is unchanged here (the Timescale CAGG work is a
		// separate stream).
		//
		// observed_at is the gateway request-start (span start), not ingest
		// now() — load-bearing for ordering, pagination, retention.
		//
		// Forward-only rebuild: DROP + CREATE rather than a column-by-column
		// ALTER. Migration source backfill (last 14 days from spans / records) is
		// a separate operational step, not part of the schema migration.
		sql: `
DROP TABLE IF EXISTS request_payloads;
DROP TABLE IF EXISTS request_events;

CREATE TABLE request_events (
    correlation_id   TEXT PRIMARY KEY,
    observed_at      TIMESTAMPTZ NOT NULL,
    session_id       TEXT NOT NULL DEFAULT '',
    agent_id         TEXT NOT NULL DEFAULT '',
    user_id          TEXT NOT NULL DEFAULT '',
    provider         TEXT NOT NULL DEFAULT '',
    model            TEXT NOT NULL DEFAULT '',
    configuration    TEXT NOT NULL DEFAULT '',
    protocol         TEXT NOT NULL DEFAULT '',
    status_code      INT  NOT NULL DEFAULT 0,
    span_event       JSONB NOT NULL
);

-- Keyset pagination and the dashboard time-range scans order by
-- (observed_at DESC, correlation_id DESC); the composite serves the page seek
-- and the stats range scan without a separate sort.
CREATE INDEX request_events_observed_corr
    ON request_events (observed_at DESC, correlation_id DESC);

-- The faceted filters (configuration / provider / model / protocol / status)
-- almost always pair with a time range, so put observed_at first to keep the
-- range bound index-driven and let the equality filters narrow within it.
CREATE INDEX request_events_filter
    ON request_events (observed_at DESC, configuration, provider, model, protocol, status_code);

CREATE INDEX request_events_session ON request_events (session_id);
CREATE INDEX request_events_agent   ON request_events (agent_id);
CREATE INDEX request_events_user    ON request_events (user_id);

-- tags + rules_fired stay in the blob; the GIN expression indexes back both the
-- @> containment filter and the jsonb_array_elements_text facet scan.
CREATE INDEX request_events_tags
    ON request_events USING gin ((span_event->'tags'));
CREATE INDEX request_events_rules
    ON request_events USING gin ((span_event->'rules_fired'));

CREATE TABLE record (
    correlation_id  TEXT PRIMARY KEY,
    received_at     TIMESTAMPTZ NOT NULL,
    body            BYTEA NOT NULL
);

CREATE INDEX record_received_at ON record (received_at);`,
	},
}
