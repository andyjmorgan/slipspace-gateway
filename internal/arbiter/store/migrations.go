package store

// migration is one forward-only schema step. version is monotonic and unique;
// name is human-facing bookkeeping recorded in schema_migrations.
type migration struct {
	version int
	name    string
	sql     string
	// noTx runs the migration's statements with autocommit instead of inside a
	// single transaction. TimescaleDB forbids creating a continuous aggregate
	// (CREATE MATERIALIZED VIEW ... WITH (timescaledb.continuous)) inside a
	// transaction block, so every CAGG-creating migration sets this — currently
	// 0007 (the four original 1-minute aggregates) and 0019 (cagg_cost_1m).
	// noTx SQL is split on `;`
	// and each statement executed in turn, so it MUST NOT contain embedded
	// semicolons (no dollar-quoted bodies/strings) and every statement MUST be
	// idempotent (IF NOT EXISTS / if_not_exists) — a mid-batch failure re-runs
	// the whole step, since the version row is recorded only after all
	// statements succeed.
	noTx bool
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
		// gateway emits slipspace.provider and ingest reads provider after this.
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
		// resolves an agent id (the authoritative X-Slipspace-Agent-Id, plus any
		// operator-configured custom headers) and emits it as gen_ai.agent.id on
		// the span and agent_id on the Record. Additive columns + a single-column
		// index so the message browser can drill down by agent, mirroring
		// request_events_session. (Historical note: X-Claude-Code-Agent-Id was
		// originally resolved here too; migration 9 moved it to the
		// conversation/thread axis, and DefaultAgentIDHeaders is now empty.)
		sql: `
ALTER TABLE request_events ADD COLUMN IF NOT EXISTS agent_id        TEXT NOT NULL DEFAULT '';
ALTER TABLE request_events ADD COLUMN IF NOT EXISTS agent_id_source TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS request_events_agent ON request_events (agent_id);`,
	},
	{
		version: 5,
		name:    "add_user_id",
		// End-user identification rides the same rails as session/agent: the
		// gateway resolves a user id (X-Slipspace-User-Id / custom) and emits it as
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
	{
		version: 7,
		name:    "metric_points_hypertable_caggs",
		noTx:    true,
		// Telemetry Rearchitecture (metrics plane): the dashboard stops scanning
		// the request_events entity for rollups and reads pre-aggregated
		// TimescaleDB continuous aggregates instead — the scale fix. metric_points
		// becomes a hypertable; four CAGGs bucket the meter feed at 1 minute with
		// count/sum only (NO percentiles for MVP — plain timescaledb, no toolkit).
		// The dashboard rolls these 1-minute buckets up to its requested
		// granularity with an outer time_bucket.
		//
		// CAGG dimensions are projected from the meter labels JSONB (->> is
		// immutable, so it is legal in a continuous-aggregate GROUP BY):
		//   - cagg_requests_1m: slipspace.requests.total, keyed by
		//     provider/model/configuration/protocol/status_code → request counts,
		//     error rate (status_code), and every By* / ProviderHealth panel.
		//   - cagg_tokens_1m: the four token COUNTERS (input/output added by the
		//     gateway alongside the existing cached/cache_creation) keyed by
		//     provider/model/configuration/protocol → token sums (Totals, ByModel)
		//     without an entity scan. (input/output rows appear once the gateway
		//     emits the new counters; the view is correct from creation.)
		//   - cagg_rules_1m / cagg_tags_1m: the rule.fired / tags.applied meters
		//     for the RulesFired / TagsFired panels.
		//
		// CREATE EXTENSION lives here (not the @75 GitOps image swap): it is
		// idempotent, superuser-capable via the telemetry role, and co-located
		// with the DDL that needs it. Created WITH NO DATA + a refresh policy so
		// the first materialization runs in the background, not in the migration.
		sql: `
CREATE EXTENSION IF NOT EXISTS timescaledb;

SELECT create_hypertable('metric_points', 'observed_at', if_not_exists => TRUE, migrate_data => TRUE);

CREATE INDEX IF NOT EXISTS metric_points_name_labels_time ON metric_points (metric_name, observed_at DESC);

CREATE MATERIALIZED VIEW IF NOT EXISTS cagg_requests_1m
WITH (timescaledb.continuous, timescaledb.materialized_only = false) AS
SELECT time_bucket('1 minute', observed_at)        AS bucket,
       labels->>'gen_ai.provider.name'             AS provider,
       labels->>'gen_ai.request.model'             AS model,
       labels->>'slipspace.configuration'          AS configuration,
       labels->>'slipspace.protocol'               AS protocol,
       labels->>'http.response.status_code'        AS status_code,
       sum(value)                                  AS requests
FROM metric_points
WHERE metric_name = 'slipspace.requests.total'
GROUP BY bucket, provider, model, configuration, protocol, status_code
WITH NO DATA;

CREATE MATERIALIZED VIEW IF NOT EXISTS cagg_tokens_1m
WITH (timescaledb.continuous, timescaledb.materialized_only = false) AS
SELECT time_bucket('1 minute', observed_at)        AS bucket,
       metric_name                                 AS metric_name,
       labels->>'gen_ai.provider.name'             AS provider,
       labels->>'gen_ai.request.model'             AS model,
       labels->>'slipspace.configuration'          AS configuration,
       labels->>'slipspace.protocol'               AS protocol,
       sum(value)                                  AS tokens
FROM metric_points
WHERE metric_name IN ('slipspace.tokens.input.total', 'slipspace.tokens.output.total', 'slipspace.tokens.cached.total', 'slipspace.tokens.cache_creation.total')
GROUP BY bucket, metric_name, provider, model, configuration, protocol
WITH NO DATA;

CREATE MATERIALIZED VIEW IF NOT EXISTS cagg_rules_1m
WITH (timescaledb.continuous, timescaledb.materialized_only = false) AS
SELECT time_bucket('1 minute', observed_at)        AS bucket,
       labels->>'rule_name'                        AS rule_name,
       labels->>'slipspace.configuration'          AS configuration,
       sum(value)                                  AS fired
FROM metric_points
WHERE metric_name = 'slipspace.rule.fired'
GROUP BY bucket, rule_name, configuration
WITH NO DATA;

CREATE MATERIALIZED VIEW IF NOT EXISTS cagg_tags_1m
WITH (timescaledb.continuous, timescaledb.materialized_only = false) AS
SELECT time_bucket('1 minute', observed_at)        AS bucket,
       labels->>'tag'                              AS tag,
       labels->>'slipspace.configuration'          AS configuration,
       sum(value)                                  AS applied
FROM metric_points
WHERE metric_name = 'gateway.tags.applied.total'
GROUP BY bucket, tag, configuration
WITH NO DATA;

SELECT add_continuous_aggregate_policy('cagg_requests_1m', start_offset => INTERVAL '3 hours', end_offset => INTERVAL '1 minute', schedule_interval => INTERVAL '1 minute', if_not_exists => TRUE);

SELECT add_continuous_aggregate_policy('cagg_tokens_1m', start_offset => INTERVAL '3 hours', end_offset => INTERVAL '1 minute', schedule_interval => INTERVAL '1 minute', if_not_exists => TRUE);

SELECT add_continuous_aggregate_policy('cagg_rules_1m', start_offset => INTERVAL '3 hours', end_offset => INTERVAL '1 minute', schedule_interval => INTERVAL '1 minute', if_not_exists => TRUE);

SELECT add_continuous_aggregate_policy('cagg_tags_1m', start_offset => INTERVAL '3 hours', end_offset => INTERVAL '1 minute', schedule_interval => INTERVAL '1 minute', if_not_exists => TRUE);`,
	},
	{
		version: 8,
		name:    "drop_dupe_span_keys",
		// span_event historically stored two byte-identical duplicate pairs: the
		// raw OTel attrs slipspace.tags / slipspace.rules_fired (copied verbatim by the
		// pre-fix buildSpanEvent) alongside the derived, normalised tags /
		// rules_fired arrays. Every reader (the GIN indexes on
		// (span_event->'tags') / (span_event->'rules_fired'), the facets unnest,
		// the EventFilter @> containment, store.SpanFields) consumes only the
		// bare keys, so the slipspace.-prefixed copies were dead weight. Ingest now
		// skips them; this strips them from existing rows so the blob carries each
		// value once. Idempotent: subtracting an absent key is a no-op.
		sql: `UPDATE request_events SET span_event = span_event - 'slipspace.tags' - 'slipspace.rules_fired';`,
	},
	{
		version: 9,
		name:    "add_conversation_parent",
		// Unified conversation/thread/parent paradigm (Session Bundling design
		// note). session_id keeps its meaning (the bundle root, now projected
		// from slipspace.session_id); two additive columns split out the per-turn
		// thread and its parent so a subagent is modelled coherently across
		// clients (Codex Thread-Id / X-Codex-Parent-Thread-Id; Claude Code's
		// X-Claude-Code-Agent-Id, moved off the squatted agent_id axis).
		//
		// conversation_id is the gen_ai.conversation.id projection (the thread,
		// == session for a main agent); parent_conversation_id links a subagent
		// thread toward its session. Both indexed for the message browser's
		// drill-down. Forward-only: existing rows keep '' until re-projected.
		sql: `
ALTER TABLE request_events ADD COLUMN IF NOT EXISTS conversation_id        TEXT NOT NULL DEFAULT '';
ALTER TABLE request_events ADD COLUMN IF NOT EXISTS parent_conversation_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS request_events_conversation ON request_events (conversation_id);
CREATE INDEX IF NOT EXISTS request_events_parent       ON request_events (parent_conversation_id);`,
	},
	{
		version: 10,
		name:    "promote_token_columns",
		// Detoast fix for the session list (#318). The Sessions aggregate summed
		// the per-request usage straight out of the span_event JSONB
		// (SUM((span_event->>'gen_ai.usage.input_tokens')::bigint + …)). Because
		// Postgres TOASTs the whole blob out of line, that SUM detoasts +
		// decompresses every ~95 kB span per row — measured at ~3.8 s over ~550
		// rows, vs ~1.4 ms for the same query with the token term removed. The cost
		// is entirely the blob touch, not the row count.
		//
		// Promote the two counts the aggregate needs to BIGINT columns, projected
		// from the span at ingest exactly like the other filter columns. The blob
		// stays the source of truth (SpanFields still decodes tokens for the
		// inspector / message rows); these are a materialized projection of it.
		// Forward-only and additive; the cached / cache-creation counts stay
		// blob-only (no aggregate reads them).
		//
		// ADD COLUMN with a constant DEFAULT is a metadata-only change (PG11+), so
		// this is instant on a large table. The backfill of EXISTING rows is
		// deliberately NOT here: re-deriving the columns from the blob is the exact
		// per-row detoast this change exists to avoid, and doing it inline would
		// scan + decompress every span. Migrate() runs before the HTTP server binds,
		// so a multi-minute table scan here outlasts the liveness probe and the pod
		// is SIGTERM'd mid-UPDATE (context canceled) into a crash loop. Per the v6
		// precedent ("backfill is a separate operational step, not part of the
		// schema migration"), new rows project correct tokens from ingest
		// immediately; existing rows are backfilled out-of-band (batched, after the
		// pod is serving) and read 0 until then.
		sql: `
ALTER TABLE request_events ADD COLUMN IF NOT EXISTS tokens_in  BIGINT NOT NULL DEFAULT 0;
ALTER TABLE request_events ADD COLUMN IF NOT EXISTS tokens_out BIGINT NOT NULL DEFAULT 0;`,
	},
	{
		version: 11,
		name:    "backfill_bookkeeping",
		// Bookkeeping for the out-of-band backfills that migrations defer
		// (v6 precedent, v10 token columns). v10 deliberately left existing
		// rows reading 0 and promised a "batched, after the pod is serving"
		// backfill — but shipped no mechanism, leaving the step to ad-hoc SQL
		// against prod and its hand-typed column names (the 2026-06-10 prod
		// errors: the dropped `streaming` column, `blob` for `record.body`).
		// store.BackfillTokenColumns is that mechanism in code; this table
		// records completion so it runs once, not on every boot (re-deriving
		// every row is the exact detoast scan v10 exists to avoid).
		sql: `
CREATE TABLE IF NOT EXISTS backfill_runs (
    name         TEXT PRIMARY KEY,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);`,
	},
	{
		version: 12,
		name:    "promote_tags_column",
		// Detoast fix for the facets dropdowns + the session-list tag rollup,
		// same lesson as v10 (#318). Distinct-tag enumeration ran
		// `jsonb_array_elements_text(span_event->'tags')` over the whole table —
		// a full scan that detoasts every ~95 kB span (measured ~30 s on prod),
		// because the GIN index on (span_event->'tags') backs containment (@>)
		// but cannot enumerate distinct elements. /api/v1/facets computed tags
		// last and returned all-or-nothing, so the scan blowing past the request
		// deadline emptied EVERY dropdown. Aggregating tags per session from the
		// blob would re-trigger the same scan.
		//
		// Promote tags to a GIN-indexed text[] column, projected from the span at
		// ingest exactly like model/configuration/tokens_in. The blob stays the
		// source of truth (SpanFields still decodes tags for the inspector /
		// message rows); the column is a materialized projection. Forward-only
		// and additive.
		//
		// ADD COLUMN with a constant DEFAULT is metadata-only (PG11+) → instant.
		// The backfill of existing rows is deliberately NOT here (it detoasts
		// every span — the exact cost this exists to avoid — and would outlast the
		// liveness probe inside Migrate()); store.BackfillTags runs it out-of-band
		// after the pod is serving (v10/v11 precedent). Existing rows read '{}'
		// until then. The old (span_event->'tags') GIN index is left in place —
		// harmless once readers move to the column; drop is a follow-up.
		sql: `
ALTER TABLE request_events ADD COLUMN IF NOT EXISTS tags text[] NOT NULL DEFAULT '{}';
CREATE INDEX IF NOT EXISTS request_events_tags_arr ON request_events USING gin (tags);`,
	},
	{
		version: 13,
		name:    "arbiter_scanner_tables",
		// SlipSpace Arbiter async security scanner (REF-007/REF-008). Four tables,
		// all decoupled from request_events by design (ADR-003): they reference
		// correlation_id but carry NO foreign key, so a finding that lands before
		// (or without) its span simply waits to be joined and never blocks ingest.
		//
		// check_tasks is the transactional outbox (ADR-004): one row per
		// (correlation_id, unit_id, check_type), exploded in the SAME tx as the
		// span upsert so the work survives a pod crash. The dispatcher claims due
		// rows with SELECT ... FOR UPDATE SKIP LOCKED + a locked_until lease, so
		// multi-pod drain needs no extra infrastructure. finding holds per-hit
		// rows; verdict is the reduced per-span outcome (FLAGGED ▸ PARTIAL ▸ CLEAN,
		// ADR-017) with provenance; evidence stores ONLY the offending field, as
		// application-side ciphertext (ADR-018 — the evidence store is a PII
		// concentrator: encrypted at rest, retention-bounded). The full body is
		// never stored. All additive, forward-only; regular tx (no hypertable).
		sql: `
CREATE TABLE IF NOT EXISTS check_tasks (
    correlation_id  TEXT        NOT NULL,
    unit_id         TEXT        NOT NULL,
    check_type      TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'pending',
    attempt         INT         NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_until    TIMESTAMPTZ,
    stage           INT         NOT NULL DEFAULT 0,
    unit_locator    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    result          JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (correlation_id, unit_id, check_type)
);
CREATE INDEX IF NOT EXISTS check_tasks_claimable
    ON check_tasks (next_attempt_at)
    WHERE status IN ('pending', 'processing');

CREATE TABLE IF NOT EXISTS finding (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    correlation_id   TEXT        NOT NULL,
    unit_id          TEXT        NOT NULL,
    check_type       TEXT        NOT NULL,
    category         TEXT        NOT NULL,
    score            REAL        NOT NULL DEFAULT 0,
    raw_label        TEXT        NOT NULL DEFAULT '',
    span_start       INT,
    span_end         INT,
    span_basis       TEXT        NOT NULL DEFAULT '',
    localization     TEXT        NOT NULL DEFAULT '',
    detector_id      TEXT        NOT NULL DEFAULT '',
    detector_version TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS finding_correlation ON finding (correlation_id);

CREATE TABLE IF NOT EXISTS verdict (
    correlation_id TEXT        PRIMARY KEY,
    state          TEXT        NOT NULL,
    max_score      REAL        NOT NULL DEFAULT 0,
    top_category   TEXT        NOT NULL DEFAULT '',
    finding_count  INT         NOT NULL DEFAULT 0,
    inconclusive   TEXT[]      NOT NULL DEFAULT '{}',
    provenance     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    decided_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS verdict_state_time ON verdict (state, decided_at DESC);

CREATE TABLE IF NOT EXISTS evidence (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    correlation_id TEXT        NOT NULL,
    unit_id        TEXT        NOT NULL,
    check_type     TEXT        NOT NULL,
    ciphertext     BYTEA       NOT NULL,
    nonce          BYTEA       NOT NULL,
    key_id         TEXT        NOT NULL DEFAULT '',
    expires_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS evidence_correlation ON evidence (correlation_id);
CREATE INDEX IF NOT EXISTS evidence_expiry ON evidence (expires_at) WHERE expires_at IS NOT NULL;`,
	},
	{
		version: 14,
		name:    "finding_offending_text",
		// Denormalize the offending text onto each finding so the Security
		// reports can show it directly. Stored plaintext, per finding (the exact
		// span substring for localized hits, the whole unit otherwise) — accepting
		// duplication across a unit's findings, and with the span_event copy. The
		// encrypted evidence table (ADR-018) stays for the at-rest-encrypted lane,
		// but the same content already lives in span_event in plaintext, so the
		// finding-level copy adds no new exposure while making the report legible.
		// Additive, forward-only; backfilled rows keep the '' default.
		sql: `ALTER TABLE finding ADD COLUMN IF NOT EXISTS offending_text TEXT NOT NULL DEFAULT '';`,
	},
	{
		version: 15,
		name:    "security_dashboard_time_indexes",
		// The security dashboard rollups time-bucket the verdict + finding tables
		// by their timestamps (QueryDashboardSecurity). finding has only a
		// correlation index, and verdict's only time index leads with state — so a
		// bare time-window count over either would seq-scan. Add a created_at /
		// decided_at index on each so the dashboard window scan stays cheap as the
		// scanner accumulates rows. Additive, forward-only; regular tx (no
		// hypertable involved — these are plain relational tables).
		sql: `
CREATE INDEX IF NOT EXISTS finding_created_at ON finding (created_at DESC);
CREATE INDEX IF NOT EXISTS verdict_decided_at ON verdict (decided_at DESC);`,
	},
	{
		version: 16,
		name:    "scan_audit_log",
		// Append-only audit log of security scans that did NOT complete cleanly —
		// the operational failures (timeout, unreachable, detector_error,
		// no_detector, unit_missing), each tagged with a reason. Distinct from the
		// check_tasks outbox, which is a MUTATING work queue (claimed, retried,
		// marked terminal, and pruned under retention): once a task ends terminal
		// its failure history is gone, so check_tasks cannot answer "when did scans
		// fail?". This table is the immutable record of those events, never updated
		// or deleted by the scanner, so the dashboard can surface WHEN failures
		// happen — not just the aggregate inconclusive count the verdict posture
		// carries. Append-only, forward-only; regular tx (no hypertable — a plain
		// relational table like finding/verdict). The occurred_at DESC index backs
		// the dashboard's newest-first window scan.
		sql: `
CREATE TABLE IF NOT EXISTS scan_audit (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    correlation_id TEXT        NOT NULL,
    unit_id        TEXT        NOT NULL,
    check_type     TEXT        NOT NULL,
    detector_id    TEXT        NOT NULL DEFAULT '',
    reason         TEXT        NOT NULL,
    attempts       INT         NOT NULL DEFAULT 0,
    detail         TEXT        NOT NULL DEFAULT '',
    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS scan_audit_occurred_at ON scan_audit (occurred_at DESC);`,
	},
	{
		version: 17,
		name:    "finding_verdict_severity",
		// Operator-assigned severity for the scanner's tag/finding controls
		// (Scanner Tag Selection + Finding Filtering + Severity design note). The
		// scanner maps each finding's category to info/warning/error via
		// scanner.severity at scan time and stamps it on the finding; the reduce
		// step rolls the max up onto the verdict so the Security dashboard can
		// triage by level. Both additive, forward-only; a plain DEFAULT '' makes
		// ADD COLUMN metadata-only (PG11+), so this is instant — existing rows read
		// '' (no severity) until re-scanned. Regular tx (no hypertable).
		sql: `
ALTER TABLE finding ADD COLUMN IF NOT EXISTS severity TEXT NOT NULL DEFAULT '';
ALTER TABLE verdict ADD COLUMN IF NOT EXISTS severity TEXT NOT NULL DEFAULT '';`,
	},
	{
		version: 18,
		name:    "tool_call_index",
		// Tool-Call Index design note: individual tool calls become first-class and
		// searchable (e.g. audit every "Skill" invocation) rather than buried in the
		// span_event.gen_ai_content blob (rendered only on per-span drill-down). The
		// gateway already normalises tool calls across all three providers into a
		// uniform part shape, so ingest reads them out of the blob with no
		// per-protocol parsing.
		//
		// One row per tool_call_id (the provider-assigned id: toolu_… / call_… /
		// Gemini id). The two halves of a single invocation land on DIFFERENT spans —
		// the call (name + arguments) in an assistant RESPONSE span, its result in the
		// NEXT REQUEST span of the same session — so the row is filled by two
		// independent upserts joined on the id; a row with no result yet is a real
		// "pending" state (e.g. a client-side call the user abandoned), not an error.
		// Decoupled from request_events by design (like the scanner tables, ADR-003):
		// references correlation_id but carries NO foreign key, so a half that lands
		// before its span never blocks ingest.
		//
		// observed_at is the first-seen time (set on insert from whichever half
		// arrived first, never updated) — the stable, non-null keyset axis; called_at
		// / responded_at are the true semantic timestamps and may be null. arguments
		// is the raw JSON the model emitted (JSONB; null when the call had none);
		// result is the tool output text (capped at ingest, true size in
		// result_chars). Additive, forward-only; regular tx (no hypertable).
		sql: `
CREATE TABLE IF NOT EXISTS tool_call (
    tool_call_id            TEXT PRIMARY KEY,
    tool_name               TEXT        NOT NULL DEFAULT '',
    session_id              TEXT        NOT NULL DEFAULT '',
    executor                TEXT        NOT NULL DEFAULT '',
    provider                TEXT        NOT NULL DEFAULT '',
    configuration           TEXT        NOT NULL DEFAULT '',
    protocol                TEXT        NOT NULL DEFAULT '',
    model                   TEXT        NOT NULL DEFAULT '',
    arguments               JSONB,
    arguments_chars         INTEGER     NOT NULL DEFAULT 0,
    result                  TEXT        NOT NULL DEFAULT '',
    result_chars            INTEGER     NOT NULL DEFAULT 0,
    call_correlation_id     TEXT        NOT NULL DEFAULT '',
    response_correlation_id TEXT        NOT NULL DEFAULT '',
    called_at               TIMESTAMPTZ,
    responded_at            TIMESTAMPTZ,
    observed_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The audit query orders by (observed_at DESC, tool_call_id DESC); the name-led
-- composite serves "every call to tool X, newest first" index-only, and the
-- bare composite serves the unfiltered list + its keyset seek.
CREATE INDEX IF NOT EXISTS tool_call_name_observed ON tool_call (tool_name, observed_at DESC, tool_call_id DESC);
CREATE INDEX IF NOT EXISTS tool_call_observed      ON tool_call (observed_at DESC, tool_call_id DESC);
CREATE INDEX IF NOT EXISTS tool_call_session       ON tool_call (session_id);`,
	},
	{
		version: 19,
		name:    "cagg_cost_1m",
		noTx:    true,
		// Token Costing design note (P3): the gateway's pricing engine emits
		// slipspace.cost.usd.total (a float counter labelled with the shared
		// request dimensions + slipspace.cost.category), which lands in
		// metric_points through the generic OTLP ingest with no ingest
		// changes. This CAGG is the dashboard's cost rollup — same
		// migration-7 template as the token CAGG, with the charge category
		// as an extra dimension so spend can be split input / output /
		// cache_read / cache_write / tool_calls. Buckets carrying no cost
		// simply have no rows; the view is correct from creation and starts
		// filling the moment a costing-enabled gateway reports. Follows
		// invariant #4: the dashboard reads this meter rollup, never
		// records. Same noTx constraints as migration 7 (statements split
		// on ';', each individually idempotent).
		sql: `
CREATE MATERIALIZED VIEW IF NOT EXISTS cagg_cost_1m
WITH (timescaledb.continuous, timescaledb.materialized_only = false) AS
SELECT time_bucket('1 minute', observed_at)        AS bucket,
       labels->>'gen_ai.provider.name'             AS provider,
       labels->>'gen_ai.request.model'             AS model,
       labels->>'slipspace.configuration'          AS configuration,
       labels->>'slipspace.protocol'               AS protocol,
       labels->>'slipspace.cost.category'          AS category,
       sum(value)                                  AS usd
FROM metric_points
WHERE metric_name = 'slipspace.cost.usd.total'
GROUP BY bucket, provider, model, configuration, protocol, category
WITH NO DATA;

SELECT add_continuous_aggregate_policy('cagg_cost_1m', start_offset => INTERVAL '3 hours', end_offset => INTERVAL '1 minute', schedule_interval => INTERVAL '1 minute', if_not_exists => TRUE);`,
	},
	{
		version: 20,
		name:    "promote_cost_column",
		// Token Costing P4 (session / message / sub-agent rollups). The gateway
		// stamps the pricing engine's estimate on the span as slipspace.cost.usd;
		// summing it per session (and per conversation_id thread) straight out of
		// the span_event JSONB would be the exact whole-blob detoast migration 10
		// eliminated for tokens. Promote it to a DOUBLE PRECISION column,
		// projected at ingest like tokens_in/tokens_out; the blob stays the
		// source of truth (SpanFields still decodes cost + the unpriced flag for
		// the inspector). No index — it is only ever summed under GROUP BYs on
		// the already-indexed session_id / conversation_id, exactly like the
		// token columns.
		//
		// Same deferral as v10: ADD COLUMN with a constant DEFAULT is
		// metadata-only, and the historical fill is the out-of-band
		// BackfillCost walker (v11 bookkeeping), never an inline scan here.
		// Pre-backfill rows read 0 until the boot walker re-projects them.
		sql: `
ALTER TABLE request_events ADD COLUMN IF NOT EXISTS cost_usd DOUBLE PRECISION NOT NULL DEFAULT 0;`,
	},
	{
		version: 21,
		name:    "advise_audit",
		// Agent-routing judgement audit log: one row per advisory decision the
		// advise handler serves (fresh judgement, cache hit, or judge failure),
		// carrying the full request payload the judge saw and the verdict it
		// returned. Written post-response by the advise handler (never on the
		// advisory path's latency budget); append-only. Like scan_audit, this
		// is a derived control-plane table — NOT the S3/spool Record channel —
		// so reading it from the console stays inside invariant #4.
		//
		// The received_at index backs the newest-first audit list; the
		// conversation_id index backs the savings join against request_events
		// (which is itself indexed on conversation_id).
		sql: `
CREATE TABLE IF NOT EXISTS advise_audit (
    id                 BIGSERIAL PRIMARY KEY,
    received_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    gateway_id         TEXT        NOT NULL,
    conversation_id    TEXT        NOT NULL,
    session_id         TEXT        NOT NULL DEFAULT '',
    configuration      TEXT        NOT NULL DEFAULT '',
    protocol           TEXT        NOT NULL DEFAULT '',
    provider           TEXT        NOT NULL DEFAULT '',
    requested_model    TEXT        NOT NULL DEFAULT '',
    agent_family       TEXT        NOT NULL DEFAULT '',
    entrypoint         TEXT        NOT NULL DEFAULT '',
    is_subagent        BOOLEAN     NOT NULL DEFAULT false,
    tool_names         TEXT[]      NOT NULL DEFAULT '{}',
    system_prefix      TEXT        NOT NULL DEFAULT '',
    first_user_message TEXT        NOT NULL DEFAULT '',
    verdict_switch     BOOLEAN     NOT NULL DEFAULT false,
    verdict_model      TEXT        NOT NULL DEFAULT '',
    verdict_reason     TEXT        NOT NULL DEFAULT '',
    verdict_confidence REAL        NOT NULL DEFAULT 0,
    cache_hit          BOOLEAN     NOT NULL DEFAULT false,
    judge_latency_ms   INTEGER     NOT NULL DEFAULT 0,
    error              TEXT        NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS advise_audit_received     ON advise_audit (received_at DESC);
CREATE INDEX IF NOT EXISTS advise_audit_conversation ON advise_audit (conversation_id);`,
	},
}
