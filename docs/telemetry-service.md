# Telemetry service

The **telemetry service** is a standalone binary (`cmd/telemetry`, image `sluice-telemetry`) that one or more Sluice gateways report into. It is the central, Postgres-backed home for everything a fleet of gateways emits: the gen_ai OTLP spans, the `sluice.*` OTel meters, and the full per-request **Record** (request/response bodies, headers, rule chain, resilience attempts). It stitches those feeds together by `correlation_id` / `session_id` and serves an operator console — the same dashboard + message inspector the gateway's own admin console exposes, but fleet-wide and with full-history retention.

It is deployed **separately** from the gateway, with its **own Postgres**. The gateway data plane never depends on it: if the telemetry service is down, the gateway keeps forwarding traffic (OTLP export and Record push are best-effort, fire-and-forget). The service never sits on a request path and never signs receipts — it only ever *consumes* telemetry. This is the reverse of the scrapped control plane it replaces.

This page is the operator overview. For the wire-level detail:

- **Console + query API** — [telemetry-service-api.md](telemetry-service-api.md)
- **HMAC Record webhook contract** — [telemetry-webhook.md](telemetry-webhook.md)
- **Postgres schema + migrations** — [telemetry-database-schema.md](telemetry-database-schema.md)
- **What the gateway emits** (meters, the Record envelope) — [observability.md](observability.md), [spool.md](spool.md), [connectors.md](connectors.md)

---

## Table of contents

1. [Purpose](#purpose)
2. [Topology: two listeners](#topology-two-listeners)
3. [The three feeds and invariant #4](#the-three-feeds-and-invariant-4)
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

The telemetry service fills that gap. Gateways report into it over two wire contracts (OTLP and the HMAC Record webhook); the service persists everything in Postgres and serves the fleet-wide console.

The service is **read-only toward the gateways**: the only inbound trust check is the HMAC signature on Record pushes (see [the registry](#the-three-feeds-and-invariant-4)). No gateway can read anything back from the service; the console is for human operators behind HTTP Basic auth.

---

## Topology: two listeners

The process binds exactly two listeners (`cmd/telemetry/main.go::run`):

```mermaid
flowchart LR
    subgraph GW["Gateway fleet"]
        M["sluice.* meters"]
        S["gen_ai OTLP spans"]
        R["Record push<br/>(HMAC-signed)"]
    end

    subgraph TS["telemetry service"]
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

**HTTP listener — default `0.0.0.0:8686`** (`config.DefaultHTTPBind`, `internal/telemetry/config/config.go:24`). It multiplexes three kinds of route (`internal/telemetry/server/server.go::Handler`):

- **Open probes** — `GET /healthz` (liveness) and `GET /readyz` (readiness; 503 until Postgres is reachable, so a load balancer drains the instance while the store recovers).
- **Open HMAC Record webhook** — `POST /api/v1/ingest/record`. "Open" in the routing sense only: it carries no Basic auth, because the push **authenticates itself** via its `X-Sluice-Signature` HMAC. See [telemetry-webhook.md](telemetry-webhook.md).
- **Basic-auth console + query API** — `GET /api/v1/dashboard/*`, `/api/v1/messages*`, `/api/v1/events*`, `/api/v1/sessions/{id}`, `/api/v1/facets`, plus the SPA bundle at `GET /` (`internal/telemetry/server/query.go::registerQueryRoutes`). Every API route is wrapped in `Server.basicAuth` (`server.go:109`). See [telemetry-service-api.md](telemetry-service-api.md).

**OTLP gRPC listener — default `0.0.0.0:8687`** (`config.DefaultOTLPBind`, `config.go:25`). One gRPC server registers **both** OTLP receivers (`internal/telemetry/ingest/grpc.go::NewOTLPServer`):

- the **trace** service (`ingest.TraceReceiver`) — one `request_events` row per gen_ai span;
- the **metrics** service (`ingest.MetricsReceiver`) — `metric_points` rows from the gateway's exported counters and gauges.

Gateways export to `:8687` directly or via an intervening OTel collector.

The two listeners run on independent goroutines bound to the signal context (`safego.Go(ctx, "telemetry.serve.http", …)` and `"telemetry.serve.otlp"`); the first to return an error tears the other down.

> The exact ports are **defaults**, applied only when the YAML omits them (`config.applyDefaults`, `config.go:113`). Set `http_bind` / `otlp_bind` to override.

---

## The three feeds and invariant #4

CLAUDE.md **invariant #4** mandates that reporting and telemetry stay separate channels: OTel meters carry counters/histograms/gauges; the connector spool carries the end-of-pipeline **Record** (audit, billing, replay). "A Grafana panel must never read records from S3; the equivalent OTel meter exists for every dimension a dashboard cares about." The telemetry service is the consuming end of both channels, and it preserves that separation in its storage layout — it never reconstructs a meter from a Record, or vice versa. Three feeds land in three tables (`internal/telemetry/store/migrations.go`, migration `0001`):

| Feed | Listener / route | Receiver | Table | Role |
|---|---|---|---|---|
| **gen_ai spans** | OTLP gRPC `:8687` (trace) | `ingest.TraceReceiver` (`ingest/otlp.go:31`) | `request_events` | Lean, queryable per-request row. Drives recent-history + drill-down. |
| **`sluice.*` meters** | OTLP gRPC `:8687` (metrics) | `ingest.MetricsReceiver` (`ingest/otlp.go:71`) | `metric_points` | Raw numeric samples the dashboard aggregates into rate / token / latency panels. |
| **Record** | HTTP `:8686` `POST /api/v1/ingest/record` | `ingest.RecordHandler` (`ingest/record.go:48`) | `request_events` (gateway columns) + `request_payloads` (one row per body/header/SSE-rollup item) | The full audit copy: bodies, headers, rule chain, resilience attempts. |

How this complies with invariant #4:

- **Meters never carry bodies.** The metrics feed only ever produces `metric_points` (numeric samples + bounded-cardinality labels); `PointsFromMetric` (`ingest/otlp.go:107`) reads only OTLP number data points. The console's aggregate panels read `metric_points`, never `request_payloads`.
- **The Record never replaces a meter.** The HMAC webhook feeds `request_payloads` (the heavy, audit-grade copy) and the *gateway half* of `request_events`. The message **inspector** reads `request_payloads`; the **dashboard** reads `metric_points` and `request_events` aggregates. The two are joined only for a human looking at one request, never to compose a metric.
- **The two halves of `request_events` are owned by different feeds.** The gen_ai OTLP span owns the gen_ai columns (provider/model/tokens) via `UpsertRequestEvent`; the Record push owns the gateway columns (configuration, method, status, rule chain) via `UpsertGatewayRecord`. They upsert into the same `correlation_id` row from opposite sides without clobbering each other (see [telemetry-database-schema.md](telemetry-database-schema.md) for the conflict clauses).

The Record feed is the **gateway → service trust boundary**. `RecordHandler.ServeHTTP` (`ingest/record.go:62`) reads `X-Sluice-Gateway-Id` + `X-Sluice-Signature`, then calls `Registry.Verify` (`internal/telemetry/registry/registry.go:52`), which recomputes the hex HMAC-SHA256 of the raw body under the registered secret and compares constant-time (`hmac.Equal`). A record is stored **iff** its signature verifies; unknown-gateway and bad-signature both collapse to `401` so the caller learns nothing beyond "rejected". The body is capped at **16 MiB** (`maxRecordBytes`, `ingest/record.go:27`) — the gateway bounds captured bodies at 10 MiB inbound, leaving headroom for the JSON envelope.

---

## Configuration

Unlike the gateway — which scans a whole config **directory** — the telemetry service takes a **single YAML file** (`internal/telemetry/config/config.go::Load`). File contents are trusted (mounted from a k8s Secret or a filesystem-permissioned path); there is **no `${VAR}` / `env:` interpolation** inside the YAML, matching the gateway's "only file paths are env-overridable" rule. The decoder runs with `KnownFields(true)` (`config.go:101`), so a misspelled key is a hard parse error rather than a silently dropped field.

### Schema and defaults

```yaml
# Console + HMAC-webhook ingest listener. Default 0.0.0.0:8686.
http_bind: "0.0.0.0:8686"

# OTLP gRPC listener for the gen_ai spans + sluice meters. Default 0.0.0.0:8687.
otlp_bind: "0.0.0.0:8687"

# Per-request cap (bytes) on the gen_ai content the console keeps from OTLP
# spans. Content is a best-effort console aid — the audit copy travels the
# Record feed — so it is bounded by default. Omit to take the default (16384);
# set 0 (or negative) to disable the cap and store the full content.
content_max_bytes: 16384

postgres:
  # libpq/pgx connection string. Required.
  dsn: "postgres://telemetry:telemetry@telemetry-db:5432/telemetry?sslmode=disable"

console:
  # HTTP Basic login for the operator console. Required.
  username: "admin"
  # bcrypt hash of the console password (NOT the cleartext). Required.
  password_hash: "$2a$10$Yd...replace.with.a.real.bcrypt.hash...."

# Registered gateways permitted to push Records. A webhook is trusted iff its
# X-Sluice-Signature verifies against the matching gateway's hmac_secret.
gateways:
  - id: "gw-1"
    hmac_secret: "a-long-random-shared-secret"
```

| Key | Type | Default | Meaning | Source |
|---|---|---|---|---|
| `http_bind` | string | `0.0.0.0:8686` | Console + HMAC webhook listener | `config.go:24,38,114` |
| `otlp_bind` | string | `0.0.0.0:8687` | OTLP gRPC listener (traces + metrics) | `config.go:25,40,117` |
| `content_max_bytes` | int (pointer) | `16384` | Per-request gen_ai content cap from OTLP spans. Unset → default; `0`/negative → unlimited | `config.go:33,45,125` |
| `postgres.dsn` | string | — (**required**) | pgx/libpq connection string | `config.go:59` |
| `console.username` | string | — (**required**) | HTTP Basic login | `config.go:65` |
| `console.password_hash` | string | — (**required**) | **bcrypt** hash of the console password | `config.go:69` |
| `gateways[].id` | string | — (**required**) | Stable gateway identifier echoed on its Record pushes and carried on events for stitching | `config.go:76` |
| `gateways[].hmac_secret` | string | — (**required**) | Shared secret the gateway signs Record pushes with | `config.go:79` |

`content_max_bytes` is modelled as a `*int` so the loader can distinguish "unset" (take the `16384` default) from an explicit `0` (unlimited). `Config.ContentCap` (`config.go:125`) resolves the effective value, which the trace receiver treats as "keep the whole content" when `<= 0`.

### Validation

`Config.Validate` (`config.go:135`) runs after defaults are applied and rejects, with a specific error, any config that would leave the service unable to do its job:

- `postgres.dsn is required` — no store to write to.
- `console.username is required` / `console.password_hash is required` — no credentials to guard the console.
- `gateways[i]: id is required` / `gateways[i] (<id>): hmac_secret is required` — a registry entry must be usable.
- `gateways[i]: duplicate id "<id>"` — gateway ids must be unique, since they key the HMAC-secret map (`registry.New`, `registry/registry.go:34`).

An empty `gateways` list is **valid** — the service then accepts no Record pushes (every webhook 401s as an unknown gateway) but still ingests OTLP and serves the console. Listener binds are *not* validated here; an unbindable address surfaces at `net.Listen` / `ListenAndServe` time.

### Generating the console password hash

`console.password_hash` is verified with `bcrypt.CompareHashAndPassword` (`server.go:123`), so the YAML must carry a **bcrypt** hash, never cleartext. Generate one with `htpasswd` (from `apache2-utils` / `httpd-tools`), stripping the `user:` prefix and normalising the variant prefix to `$2a$`:

```sh
htpasswd -bnBC 10 "" 'your-console-password' | tr -d ':\n' | sed 's/^\$2y/\$2a/'
```

Paste the `$2a$...` output as `password_hash`. The username comparison is `subtle.ConstantTimeCompare` and both branches always run (`server.go:121`), so a wrong username and a wrong password cost the same — no timing oracle.

> The console deliberately sends a **bare `401`** with **no `WWW-Authenticate` header** (`server.go:109` comment). The SPA drives the credential prompt with its own login form and attaches the `Authorization` header on every fetch; emitting the challenge header would make browsers pop their native auth dialog over the SPA on every poll. `curl --basic -u admin:… ` still works.

### Environment

The binary reads only two environment variables; everything else lives in the YAML (`cmd/telemetry/main.go`):

| Variable | Purpose | Source |
|---|---|---|
| `SLUICE_TELEMETRY_CONFIG` | Default path to the config YAML when `-config` is not passed | `main.go:46` |
| `LOG_LEVEL` | `debug` / `info` (default) / `warn` / `error` for the `slog` JSON handler | `main.go:55,149` |

Flags: `-config <path>` (overrides `SLUICE_TELEMETRY_CONFIG`) and `-version` (print version and exit). The service logs JSON to **stderr** with a fixed `service=telemetry` + `version` header (`main.go:164`).

---

## Startup and graceful shutdown

`run` (`cmd/telemetry/main.go:68`) is the lifecycle:

1. **Load + validate** the config (`config.Load`).
2. **Open Postgres** under a bounded budget — `storeOpenBudget = 15s` (`main.go:38`) — so a wedged database surfaces fast at boot instead of hanging the process. `store.Open` dials the pool and pings it (`store/store.go:87`).
3. **Migrate** forward-only, each step in its own transaction, idempotent (`store.Migrate`, `store/store.go:113`). The applied schema version is logged.
4. **Wire the feeds**: build the `registry` from `cfg.Gateways`, the `RecordHandler`, and the OTLP trace + metrics receivers; construct the console `Server`.
5. **Serve both listeners** on context-bound goroutines.

Shutdown is signal-driven (`SIGINT` / `SIGTERM` via `signal.NotifyContext`):

- The OTLP gRPC server is stopped with `GracefulStop` (drains in-flight RPCs).
- The HTTP server is drained with `Shutdown` under a **5-second** budget — `shutdownTimeout` (`main.go:37`). That shutdown context is **deliberately detached** from the cancelled signal context (`//nolint:contextcheck`, `main.go:143`) so the drain budget outlives the `SIGTERM` that triggered it.

If either listener returns a non-`ErrServerClosed` error before a signal arrives, `run` stops the OTLP server and returns the error, exiting non-zero (`main.go:128`).

---

## Deployment model

The telemetry service is a **separate deployable** from the gateway, with its **own Postgres**. It shares no process, no config file, and no database with the gateway; the only coupling is the OTLP + HMAC-webhook wire contract (`deploy/docker/Dockerfile.telemetry` header).

- **Image** — `sluice-telemetry`, built from `deploy/docker/Dockerfile.telemetry`. A multi-stage build compiles the second Vite target (`npm run build:telemetry`) into `internal/telemetry/server/webdist`, embeds it via `go:embed`, builds a static `CGO_ENABLED=0` binary, and ships it on `scratch` as non-root (`USER 65532:65532`). `EXPOSE 8686 8687`.
- **Local stack** — `docker-compose.telemetry.yaml` brings up Postgres 16 + the telemetry binary, mounting `deploy/compose/telemetry.yaml` at `/etc/sluice/telemetry.yaml`. Run it independently of the gateway compose file:

  ```sh
  docker compose -f docker-compose.telemetry.yaml up --build
  ```

  Before first run, replace the `REPLACE_WITH_BCRYPT_HASH` and `REPLACE_WITH_SHARED_SECRET` placeholders in `deploy/compose/telemetry.yaml`. The console + webhook land on `:8686`, OTLP gRPC on `:8687`.
- **Postgres** — the service expects a dedicated database reachable via `postgres.dsn`. It owns its schema end-to-end through the embedded migration runner; no external migration tooling is required. The pool is a plain `pgxpool` (`store/store.go:87`).
- **Probes** — wire `GET /healthz` to liveness and `GET /readyz` to readiness; `/readyz` returns `503` while Postgres is unreachable.

On the gateway side, point its OTLP exporter at `telemetry-host:8687` and configure a Record-push (webhook) destination at `https://telemetry-host:8686/api/v1/ingest/record` with the matching `gateways[].id` / `hmac_secret`. See [telemetry-webhook.md](telemetry-webhook.md) for the push contract and [observability.md](observability.md) for the gateway's OTLP export configuration.

---

## Cross-references

- [telemetry-service-api.md](telemetry-service-api.md) — the Basic-auth console + query API (dashboard, messages, events, sessions, facets).
- [telemetry-webhook.md](telemetry-webhook.md) — the HMAC Record webhook contract (headers, signature, body shape, error codes).
- [telemetry-database-schema.md](telemetry-database-schema.md) — `request_events`, `request_payloads`, `metric_points`, migrations, and the upsert-from-both-sides model.
- [observability.md](observability.md) — what the gateway emits: every meter and the OTLP export pipeline.
- [spool.md](spool.md) / [connectors.md](connectors.md) — the connector spool and the Record envelope the webhook pushes.
- `CLAUDE.md` → load-bearing invariant #4 — reporting and telemetry are separate channels.
