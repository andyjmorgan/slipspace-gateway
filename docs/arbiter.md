# Arbiter

The **Arbiter** is a standalone binary (`cmd/arbiter`, image `slipspace-arbiter`) that one or more SlipSpace gateways report into. It is the central, Postgres-backed (TimescaleDB) home for everything a fleet of gateways emits: the gen_ai OTLP spans, the `slipspace.*` OTel meters, and the full per-request **Record** (request/response bodies, headers, rule chain, resilience attempts). The gen_ai span is the **single writer** of the per-request entity; the Record lands a lazy verbatim blob joined by `correlation_id` only when an operator opens the inspector; the meters feed pre-aggregated dashboards. The service serves an operator console — the same dashboard + message inspector the gateway's own admin console exposes, but fleet-wide and with full-history retention.

It is deployed **separately** from the gateway, with its **own Postgres**. The gateway data plane never depends on it: if the Arbiter is down, the gateway keeps forwarding traffic (OTLP export and Record push are best-effort, fire-and-forget). The service never sits on a request path and never signs receipts — it only ever *consumes* telemetry.

This page is the operator overview. For the wire-level detail:

- **Console + query API** — [arbiter-api.md](arbiter-api.md)
- **HMAC Record webhook contract** — [arbiter-webhook.md](arbiter-webhook.md)
- **Postgres schema + migrations** — [arbiter-database-schema.md](arbiter-database-schema.md)
- **What the gateway emits** (meters, the Record envelope) — [observability.md](observability.md), [spool.md](spool.md), [connectors.md](connectors.md)

---

## Table of contents

1. [Purpose](#purpose)
2. [Topology: two listeners](#topology-two-listeners)
3. [The two ingest channels and invariant #4](#the-two-ingest-channels-and-invariant-4)
4. [Configuration](#configuration)
   - [Schema and defaults](#schema-and-defaults)
   - [Validation](#validation)
   - [Generating the console password hash](#generating-the-console-password-hash)
   - [Environment](#environment)
5. [Startup and graceful shutdown](#startup-and-graceful-shutdown)
6. [Deployment model](#deployment-model)
7. [Cross-references](#cross-references)

---

## Purpose

A single gateway ships its own admin console (`docs/admin-console.md`) backed by an in-process snapshotter and a bounded ring of recent messages. That is enough to watch *one* instance, *recently*. It is not enough to:

- watch a **fleet** of gateways in one place;
- retain **full history** across restarts (the gateway ring is in-memory and bounded);
- keep the **full Record** — request/response bodies, headers, assembled SSE rollup — for audit and replay without standing up an S3/Azure connector destination.

The Arbiter fills that gap. Gateways report into it over two wire contracts (OTLP and the HMAC Record webhook); the service persists everything in Postgres and serves the fleet-wide console.

The service is **read-only toward the gateways**: the only inbound trust check is the HMAC signature on Record pushes (see [the registry](#the-two-ingest-channels-and-invariant-4)). No gateway can read anything back from the service; the console is for human operators behind HTTP Basic auth.

---

## Topology: two listeners

The process binds exactly two listeners (`cmd/arbiter/main.go::run`):

```mermaid
flowchart LR
    subgraph GW["Gateway fleet"]
        M["slipspace.* meters"]
        S["gen_ai OTLP spans"]
        R["Record push<br/>(HMAC-signed)"]
    end

    subgraph TS["Arbiter"]
        direction TB
        H["HTTP :8686"]
        O["OTLP gRPC :8687"]
        PG[("Postgres")]
        H --> PG
        O --> PG
    end

    M -->|OTLP gRPC| O
    S -->|OTLP gRPC| O
    R -->|POST /api/v1/ingest/record| H
    OP(["Operator"]) -->|Basic auth| H
```

**HTTP listener — default `0.0.0.0:8686`** (`config.DefaultHTTPBind`, `internal/arbiter/config/config.go:27`). It multiplexes three kinds of route (`internal/arbiter/server/server.go::Handler`):

- **Open probes** — `GET /healthz` (liveness) and `GET /readyz` (readiness; 503 until Postgres is reachable, so a load balancer drains the instance while the store recovers).
- **Open HMAC Record webhook** — `POST /api/v1/ingest/record`. "Open" in the routing sense only: it carries no Basic auth, because the push **authenticates itself** via its `X-Slipspace-Signature` HMAC. See [arbiter-webhook.md](arbiter-webhook.md).
- **Open HMAC routing advisor** — `POST /api/v1/advise/route` (present only when the `advise` config block is enabled). Same self-authenticating HMAC convention and the same `gateways[]` registry as the Record webhook, but a distinct channel: control-plane advice for agent-aware routing, neither telemetry nor audit. See [agent-routing.md](agent-routing.md).
- **Basic-auth console + query API** — `GET /api/v1/dashboard/*`, `/api/v1/messages*`, `/api/v1/events*`, `/api/v1/sessions/{id}`, `/api/v1/facets`, plus the SPA bundle at `GET /` (`internal/arbiter/server/query.go::registerQueryRoutes`). Every API route is wrapped in `Server.basicAuth` (`server.go:159-168`). See [arbiter-api.md](arbiter-api.md).

**OTLP gRPC listener — default `0.0.0.0:8687`** (`config.DefaultOTLPBind`, `config.go:28`). One gRPC server registers **both** OTLP receivers (`internal/arbiter/ingest/grpc.go::NewOTLPServer`):

- the **trace** service (`ingest.TraceReceiver`) — one `request_events` row per gen_ai span; the span is the **single writer** of the entity (the complete span lands in `span_event`, the filter columns are projected from it);
- the **metrics** service (`ingest.MetricsReceiver`) — `metric_points` rows from the gateway's exported counters and gauges. `metric_points` is a TimescaleDB hypertable; the dashboard reads five 1-minute continuous aggregates over it (`cagg_requests_1m`, `cagg_tokens_1m`, `cagg_rules_1m`, `cagg_tags_1m`, and `cagg_cost_1m` — the cost rollup added in migration 19 for costing v2.1.0), never the entity. Histogram and summary metrics are skipped on ingest — only number data points land.

Gateways export to `:8687` directly or via an intervening OTel collector.

The two listeners run on independent goroutines bound to the signal context (`safego.Go(ctx, "telemetry.serve.http", …)` and `"telemetry.serve.otlp"`); the first to return an error tears the other down.

> The exact ports are **defaults**, applied only when the YAML omits them (`config.applyDefaults`, `config.go:361`). Set `http_bind` / `otlp_bind` to override.

---

## The two ingest channels and invariant #4

CLAUDE.md **invariant #4** mandates that reporting and telemetry stay separate channels: OTel meters carry counters/histograms/gauges; the connector spool carries the end-of-pipeline **Record** (audit, billing, replay). "A Grafana panel must never read records from S3; the equivalent OTel meter exists for every dimension a dashboard cares about." The Arbiter is the consuming end of both channels, and it preserves that separation in its storage layout — it never reconstructs a meter from a Record, or vice versa.

Under the **single-writer** rearchitecture, two ingest channels land in three tables (`internal/arbiter/store/migrations.go`). `metric_points` is created in migration `0001` and only converted to a hypertable (with its continuous aggregates) in `0007`; migrations `0006`–`0007` create `request_events` and `record`:

| Channel | Listener / route | Receiver | Table(s) | Role |
|---|---|---|---|---|
| **gen_ai spans** | OTLP gRPC `:8687` (trace) | `ingest.TraceReceiver` | `request_events` (**sole writer**) | The complete span in `span_event JSONB`; the filter columns (provider/model/configuration/protocol/status_code + the identity ids) projected from it. Drives recent-history + drill-down. |
| **`slipspace.*` meters** | OTLP gRPC `:8687` (metrics) | `ingest.MetricsReceiver` | `metric_points` (hypertable) → five CAGGs | Raw number data points; the dashboard reads the 1-minute continuous aggregates over them (`cagg_requests_1m`, `cagg_tokens_1m`, `cagg_rules_1m`, `cagg_tags_1m`, `cagg_cost_1m`) for its rate / token / fired-row / cost panels. |
| **Record** | HTTP `:8686` `POST /api/v1/ingest/record` | `ingest.RecordHandler` (`ingest/record.go`) | `record` (one verbatim blob per correlation id) | The full audit copy: bodies, headers, rule chain, resilience attempts — stored as the raw `cc.Record` bytes, joined to the entity lazily by `correlation_id` only when the inspector's record tab opens. |

How this complies with invariant #4:

- **Meters never carry bodies.** The metrics feed only ever produces `metric_points` (numeric samples + bounded-cardinality labels); `PointsFromMetric` reads only OTLP number data points (histograms/summaries are dropped). The console's aggregate panels read the `metric_points` continuous aggregates, never the `record` blob.
- **The Record never replaces a meter.** The HMAC webhook feeds only the `record` table — the heavy, audit-grade verbatim copy. The message **inspector** decodes that blob lazily; the **dashboard** reads the CAGGs. The two are joined only for a human looking at one request, never to compose a metric.
- **The entity has exactly one writer.** The gen_ai OTLP span owns `request_events` outright via `UpsertRequestEvent` — there is no second feed writing the entity, and therefore no cross-feed COALESCE merge to keep correct. The gateway facts the console needs (configuration, protocol, method, status, tags, rule chain) ride the span as `slipspace.*` attributes and are projected into the entity from the span, not merged in from the Record. The Record exists only as the lazily-joined raw audit copy.

The Record feed is the **gateway → service trust boundary**. `RecordHandler.ServeHTTP` (`ingest/record.go`) reads `X-Slipspace-Gateway-Id` + `X-Slipspace-Signature`, then calls `Registry.Verify` (`internal/arbiter/registry/registry.go`), which recomputes the hex HMAC-SHA256 of the raw body under the registered secret and compares constant-time (`hmac.Equal`). A record is stored **iff** its signature verifies; unknown-gateway and bad-signature both collapse to `401` so the caller learns nothing beyond "rejected". The handler decodes the body only far enough to read `correlation_id`, then stores the raw bytes verbatim — no fan-out into columns or payload rows. The body is capped at **16 MiB** (`maxRecordBytes`, `ingest/record.go`) — the gateway bounds captured bodies at 10 MiB inbound, leaving headroom for the JSON envelope.

---

## Configuration

Unlike the gateway — which scans a whole config **directory** — the Arbiter takes a **single YAML file** (`internal/arbiter/config/config.go::Load`). File contents are trusted (mounted from a k8s Secret or a filesystem-permissioned path); there is **no `${VAR}` / `env:` interpolation** inside the YAML, matching the gateway's "only file paths are env-overridable" rule. The decoder runs with `KnownFields(true)` (`config.go:349`), so a misspelled key is a hard parse error rather than a silently dropped field.

### Schema and defaults

```yaml
# Console + HMAC-webhook ingest listener. Default 0.0.0.0:8686.
http_bind: "0.0.0.0:8686"

# OTLP gRPC listener for the gen_ai spans + slipspace meters. Default 0.0.0.0:8687.
otlp_bind: "0.0.0.0:8687"

# Per-request cap (bytes) on the gen_ai content the console keeps from OTLP
# spans. Content is a best-effort console aid — the audit copy travels the
# Record feed — so it is bounded by default. Omit to take the default (16384);
# set 0 (or negative) to disable the cap and store the full content.
content_max_bytes: 16384

# Per-field cap (bytes) the console's session-spans projection
# (GET /api/v1/sessions/{id}/spans) applies to served content fields — text,
# tool args, input_text, output_text. The *_chars fields keep the true
# uncapped sizes, so the console's "showing first N of M" notice fires exactly
# when this cap truncated. Omit to take the default (65536); 0 disables.
span_field_max_bytes: 65536

postgres:
  # libpq/pgx connection string. Required.
  dsn: "postgres://telemetry:telemetry@arbiter-db:5432/telemetry?sslmode=disable"

console:
  # HTTP Basic login for the operator console. Required.
  username: "admin"
  # bcrypt hash of the console password (NOT the cleartext). Required.
  password_hash: "$2a$10$Yd...replace.with.a.real.bcrypt.hash...."

# Registered gateways permitted to push Records. A webhook is trusted iff its
# X-Slipspace-Signature verifies against the matching gateway's hmac_secret.
gateways:
  - id: "gw-1"
    hmac_secret: "a-long-random-shared-secret"
```

| Key | Type | Default | Meaning | Source |
|---|---|---|---|---|
| `http_bind` | string | `0.0.0.0:8686` | Console + HMAC webhook listener | `config.go:27,49,362` |
| `otlp_bind` | string | `0.0.0.0:8687` | OTLP gRPC listener (traces + metrics) | `config.go:28,51,365` |
| `content_max_bytes` | int (pointer) | `16384` | Per-request gen_ai content cap from OTLP spans. Unset → default; `0`/negative → unlimited | `config.go` (`DefaultContentMaxBytes`, `ContentCap`) |
| `span_field_max_bytes` | int (pointer) | `65536` | Per-field content cap the session-spans projection applies to served text/args. Unset → default; `0`/negative → unlimited | `config.go` (`DefaultSpanFieldMaxBytes`, `SpanFieldCap`) |
| `postgres.dsn` | string | — (**required**) | pgx/libpq connection string | `config.go:307` |
| `console.username` | string | — (**required**) | HTTP Basic login | `config.go:313` |
| `console.password_hash` | string | — (**required**) | **bcrypt** hash of the console password | `config.go:317` |
| `gateways[].id` | string | — (**required**) | Stable gateway identifier echoed on its Record pushes and carried on events for stitching | `config.go:324` |
| `gateways[].hmac_secret` | string | — (**required**) | Shared secret the gateway signs Record pushes with | `config.go:327` |

`content_max_bytes` is modelled as a `*int` so the loader can distinguish "unset" (take the `16384` default) from an explicit `0` (unlimited). `Config.ContentCap` (`config.go:378`) resolves the effective value, which the trace receiver treats as "keep the whole content" when `<= 0`.

When a span's assembled gen_ai content exceeds the effective cap, the service stores **no** content for that request — only a marker `{"truncated": true, "original_bytes": N}` (`internal/arbiter/ingest/content.go`). The bounded content lives under `gen_ai_content` inside the entity's `span_event` blob. So an over-cap request still appears in the console with its metadata intact, but the gen_ai content in the inspector is replaced by that marker. The full bodies are unaffected on the **Record** feed — they land verbatim in the `record` table for audit/replay; the cap only bounds the console's convenience copy taken from OTLP spans.

### Validation

`Config.Validate` (`config.go:431`) runs after defaults are applied and rejects, with a specific error, any config that would leave the service unable to do its job:

- `postgres.dsn is required` — no store to write to.
- `console.username is required` / `console.password_hash is required` — no credentials to guard the console.
- `gateways[i]: id is required` / `gateways[i] (<id>): hmac_secret is required` — a registry entry must be usable.
- `gateways[i]: duplicate id "<id>"` — gateway ids must be unique, since they key the HMAC-secret map (`registry.New`, `registry/registry.go:34`).

An empty `gateways` list is **valid** — the service then accepts no Record pushes (every webhook 401s as an unknown gateway) but still ingests OTLP and serves the console. Listener binds are *not* validated here; an unbindable address surfaces at `net.Listen` / `ListenAndServe` time.

### Scanner scan-scoping, filtering, and severity

When the optional Arbiter scanner is enabled (`scanner.enabled: true`), four additive controls on the `scanner` block scope which traffic is scanned, suppress noisy findings, and classify what survives. All are off by default — an unset block scans every span and keeps every finding, unchanged from before.

```yaml
scanner:
  enabled: true
  # Span-level scan selection by request tag (the post-rule slipspace.tags set).
  scan_tags:
    include: ["tier:*"]        # empty/omitted = scan all spans
    exclude: ["env:internal"]  # wins over include
  # Drop matching finding categories before they are persisted (exclude-only).
  finding_exclude: ["pii.url"]
  # Drop individual findings by category + offending-text value (value-scoped).
  finding_suppress:
    - { category: "pii.person", offending_text: "(?i)^claude code$", reason: "first-party Claude Code traffic" }
  # Classify surviving findings into info/warning/error. Ordered: FIRST match wins.
  severity:
    unmapped: warning          # level for categories no rule matches (default warning)
    rules:
      - { match: "pii.url",     level: info }
      - { match: "pii.*",       level: warning }
      - { match: "injection.*", level: error }
  detectors: [ ... ]           # unchanged
```

- **Patterns are full globs** (`path.Match`, `internal/arbiter/arbiter/match.go`). Tags and finding categories contain no `/`, so `*` spans the whole value: `pii.*`, `*:internal`, and `*.url` all match. A malformed glob is rejected at config load.
- **`scan_tags`** gates `Scanner.Explode`: a span is scanned iff *(include is empty **or** a tag matches an include glob)* **and** *no tag matches an exclude glob* — exclude wins. A span the policy excludes produces no check tasks and falls back to a plain upsert, exactly as if the scanner were disabled for it. Note a span with **no tags** is not scanned when `include` is non-empty (it matches nothing).
- **`finding_exclude`** drops findings whose `category` matches any glob before they are persisted, so an excluded category never reaches the `finding` table, never gets an evidence row, and never flags the verdict. If *all* of a unit's findings are excluded, its encrypted evidence row is not written either.
- **`finding_suppress`** is the finer-grained companion: it drops an individual finding only when *both* its `category` matches the rule's glob **and** its `offending_text` matches the rule's regex — value-scoped suppression of a known-benign hit (e.g. the product name "Claude Code" a PII detector reads as a person), without disabling the whole category. The `offending_text` pattern is a **Go RE2 regex** (`internal/arbiter/arbiter/match.go`), matched **unanchored**: a bare substring matches partially, `^claude code$` exactly, and a `(?i)` prefix case-insensitively. `category` and `reason` are required; an empty/malformed glob or regex is rejected at config load. Suppressed findings take the same path as excluded ones (no `finding` row, no evidence, no verdict flag); each is logged at **Debug** with its category + `reason` + correlation id (never the offending text — that is PII), so a too-broad rule is auditable via `count_over_time` on the logs. Suppression is value-scoped, so an attacker cannot self-suppress by embedding the benign string elsewhere — only the finding whose own offending span matches is dropped; sibling findings survive.
- **`severity`** maps each surviving finding's category to `info`/`warning`/`error`, evaluated top-to-bottom with first-match-wins (order specific globs before general ones). A category matching no rule takes `unmapped` (default `warning`). The level is stamped on the finding (migration 17) and the worst level across a span's findings (`info < warning < error`) is rolled up onto the verdict — surfaced as a chip in the console Security view and as the `slipspace.security.severity` attribute on the enriched verdict span.

Validation (`config.Validate`) rejects, when the scanner is enabled, a malformed glob in any list, a `finding_suppress[]` rule missing its `category`/`offending_text`/`reason` or carrying an invalid `offending_text` regex, and a `severity.rules[].level` / `severity.unmapped` outside `{info, warning, error}`.

### Generating the console password hash

`console.password_hash` is verified with `bcrypt.CompareHashAndPassword` (`server.go:195`), so the YAML must carry a **bcrypt** hash, never cleartext. Generate one with `htpasswd` (from `apache2-utils` / `httpd-tools`), stripping the `user:` prefix and normalising the variant prefix to `$2a$`:

```sh
htpasswd -bnBC 10 "" 'your-console-password' | tr -d ':\n' | sed 's/^\$2y/\$2a/'
```

Paste the `$2a$...` output as `password_hash`. The username comparison is `subtle.ConstantTimeCompare` and both branches always run (`server.go:194`), so a wrong username and a wrong password cost the same — no timing oracle.

> The console deliberately sends a **bare `401`** with **no `WWW-Authenticate` header** (`server.go:135` comment). The SPA drives the credential prompt with its own login form and attaches the `Authorization` header on every fetch; emitting the challenge header would make browsers pop their native auth dialog over the SPA on every poll. `curl --basic -u admin:… ` still works.

### Environment

The binary reads only two environment variables; everything else lives in the YAML (`cmd/arbiter/main.go`):

| Variable | Purpose | Source |
|---|---|---|
| `SLIPSPACE_ARBITER_CONFIG` | Default path to the config YAML when `-config` is not passed | `main.go:51` |
| `LOG_LEVEL` | `debug` / `info` (default) / `warn` / `error` for the `slog` JSON handler | `main.go:61` |

Flags: `-config <path>` (overrides `SLIPSPACE_ARBITER_CONFIG`) and `-version` (print version and exit). The service logs JSON to **stderr** with a fixed `service=arbiter` + `version` header (`main.go:268-272`).

---

## Startup and graceful shutdown

`run` (`cmd/arbiter/main.go:74`) is the lifecycle:

1. **Load + validate** the config (`config.Load`).
2. **Open Postgres** under a bounded budget — `storeOpenBudget = 15s` (`main.go:44`) — so a wedged database surfaces fast at boot instead of hanging the process. `store.Open` dials the pool and pings it (`store/store.go:88`).
3. **Migrate** forward-only, each step in its own transaction, idempotent (`store.Migrate`, `store/store.go:114`). The applied schema version is logged.
4. **Wire the feeds**: build the `registry` from `cfg.Gateways`, the `RecordHandler`, and the OTLP trace + metrics receivers; construct the console `Server`.
5. **Serve both listeners** on context-bound goroutines.

Shutdown is signal-driven (`SIGINT` / `SIGTERM` via `signal.NotifyContext`):

- The OTLP gRPC server is stopped with `GracefulStop` (drains in-flight RPCs).
- The HTTP server is drained with `Shutdown` under a **5-second** budget — `shutdownTimeout` (`main.go:43`). That shutdown context is **deliberately detached** from the cancelled signal context (`//nolint:contextcheck`, `main.go:247`) so the drain budget outlives the `SIGTERM` that triggered it.

If either listener returns a non-`ErrServerClosed` error before a signal arrives, `run` stops the OTLP server and returns the error, exiting non-zero (`main.go:208`).

---

## Deployment model

The Arbiter is a **separate deployable** from the gateway, with its **own Postgres**. It shares no process, no config file, and no database with the gateway; the only coupling is the OTLP + HMAC-webhook wire contract (`deploy/docker/Dockerfile.arbiter` header).

- **Image** — `slipspace-arbiter` (the OCI image title in `deploy/docker/Dockerfile.arbiter`; the binary is `arbiter` and the local compose build tag is `arbiter:dev`), built from `deploy/docker/Dockerfile.arbiter`. A multi-stage build compiles the second Vite target (`npm run build:telemetry`) into `internal/arbiter/server/webdist`, embeds it via `go:embed`, builds a static `CGO_ENABLED=0` binary, and ships it on `scratch` as non-root (`USER 65532:65532`). `EXPOSE 8686 8687`.
- **Local stack** — `docker-compose.arbiter.yaml` brings up Postgres 16 + the arbiter binary, mounting `deploy/compose/arbiter.yaml` at `/etc/slipspace/arbiter.yaml`. Run it independently of the gateway compose file:

  ```sh
  docker compose -f docker-compose.arbiter.yaml up --build
  ```

  Before first run, replace the `REPLACE_WITH_BCRYPT_HASH` and `REPLACE_WITH_SHARED_SECRET` placeholders in `deploy/compose/arbiter.yaml`. The console + webhook land on `:8686`, OTLP gRPC on `:8687`.
- **Postgres / TimescaleDB** — the service expects a dedicated database reachable via `postgres.dsn`, with the **TimescaleDB extension available** (migration 7 runs `CREATE EXTENSION IF NOT EXISTS timescaledb` and turns `metric_points` into a hypertable with continuous aggregates). The `arbiter` Postgres image / compose stack ships TimescaleDB; a plain Postgres must have the extension installed and loadable by the telemetry role. The service owns its schema end-to-end through the embedded migration runner; no external migration tooling is required. The pool is a plain `pgxpool` (`store/store.go`).
- **Probes** — wire `GET /healthz` to liveness and `GET /readyz` to readiness; `/readyz` returns `503` while Postgres is unreachable.
- **Data lifecycle / retention** — the service has **no built-in retention or pruning**. Every ingested span, metric point, and Record persists indefinitely; the store layer has no TTL or `DELETE` path. The `record` table (full request/response bodies, headers, and SSE rollups, one verbatim blob per request) is by far the heaviest and grows with fleet traffic. Operators own retention: size the database for the expected volume, and prune out of band — e.g. a scheduled job deleting `request_events` rows by `observed_at` and `record` rows by `received_at`, and a TimescaleDB retention policy (`add_retention_policy`) dropping old `metric_points` chunks. Lowering `content_max_bytes` bounds the console's gen_ai content copy in `span_event` but **not** the Record bodies in `record`, so it is not a substitute for pruning. Plan disk before pointing a busy fleet at it.

On the gateway side, point its OTLP exporter at `telemetry-host:8687` and configure a Record-push (webhook) destination at `https://telemetry-host:8686/api/v1/ingest/record` with the matching `gateways[].id` / `hmac_secret`. See [arbiter-webhook.md](arbiter-webhook.md) for the push contract and [observability.md](observability.md) for the gateway's OTLP export configuration.

---

## Cross-references

- [arbiter-api.md](arbiter-api.md) — the Basic-auth console + query API (dashboard, messages, events, sessions, facets).
- [arbiter-webhook.md](arbiter-webhook.md) — the HMAC Record webhook contract (headers, signature, body shape, error codes).
- [arbiter-database-schema.md](arbiter-database-schema.md) — `request_events` (the single-writer entity + `span_event` projection), the lazy `record` blob, `metric_points` + continuous aggregates, and migrations.
- [observability.md](observability.md) — what the gateway emits: every meter and the OTLP export pipeline.
- [spool.md](spool.md) / [connectors.md](connectors.md) — durable connector spool semantics and the real-time Record webhook pusher.
- `CLAUDE.md` → load-bearing invariant #4 — reporting and telemetry are separate channels.
