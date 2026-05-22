# Environment Variables

Sluice's server config is loaded once at process start from `SLUICE_*` environment variables. There is no live-reload pathway — every value documented here lands on the in-memory `ServerEnv` (or the admin `Config`) at startup and is read-only thereafter. Restart to apply a change.

This page is the operator's lookup reference. The big table below covers every `SLUICE_*` variable the gateway reads, with default, type, effect, and validation rules. The grouped tables that follow exist to give context — when you're tuning a knob, the group's prose tells you what neighbourhood you're in.

Ground truth lives in [`internal/config/env.go`](../internal/config/env.go) (the bulk of the variables), [`contracts/admin/admin.go`](../contracts/admin/admin.go) (the admin password), and [`internal/observability/setup.go`](../internal/observability/setup.go) (the OTel deployment-environment tag). If this doc and the code diverge, the code wins — open a PR.

---

## Table of contents

1. [Complete reference (alphabetical)](#complete-reference-alphabetical)
2. [Core listener and config](#core-listener-and-config)
3. [Bus and NATS reporting](#bus-and-nats-reporting)
4. [Observability — OTLP, Prometheus, logging](#observability--otlp-prometheus-logging)
5. [Admin console](#admin-console)
6. [Live feed and body capture](#live-feed-and-body-capture)
7. [Rules engine](#rules-engine)
8. [Shutdown and drain](#shutdown-and-drain)
9. [Validation and error sentinels](#validation-and-error-sentinels)
10. [Notes on parsing semantics](#notes-on-parsing-semantics)

---

## Complete reference (alphabetical)

Every `SLUICE_*` variable the gateway reads, in one table. Defaults are what `LoadEnv` falls back to when the variable is unset or empty after trim.

| Variable | Default | Type | Effect | Validation | Related doc |
|---|---|---|---|---|---|
| `SLUICE_ADMIN_LIVE_FEED_BODY_BYTES` | `209715200` (200 MiB) | int (bytes) | Total byte budget of the in-process LRU that backs `GET /admin/api/v1/messages/{id}/body`. `0` disables body capture; the live-tail pane still renders metadata. | `>= 0`. When `> 0`, `SLUICE_ADMIN_LIVE_FEED_BODY_MAX_BYTES` must also be `> 0`. | [Live feed and body capture](#live-feed-and-body-capture) |
| `SLUICE_ADMIN_LIVE_FEED_BODY_MAX_BYTES` | `8388608` (8 MiB) | int (bytes) | Per-body capture cap before truncation. Bodies above the cap are stored head-only with a truncated flag. Ignored when `SLUICE_ADMIN_LIVE_FEED_BODY_BYTES=0`. | `>= 0`. Must be `> 0` when `SLUICE_ADMIN_LIVE_FEED_BODY_BYTES > 0`. | [Live feed and body capture](#live-feed-and-body-capture) |
| `SLUICE_ADMIN_LIVE_FEED_CAPACITY` | `100` | int | Size of the in-process ring of completed requests backing the admin live-messages pane. `0` disables the ring and degrades `/messages/*` endpoints to 503. | `>= 0` (0 disables). | [Live feed and body capture](#live-feed-and-body-capture) |
| `SLUICE_ADMIN_PASSWORD` | _(empty)_ | string | HTTP Basic auth password for the admin console. Wins over `admin.password` in `admin.yaml` when both are set. Required when `admin.enabled: true`. | Non-empty when `admin.enabled: true`. No length/charset rules. | [Admin console](#admin-console) |
| `SLUICE_ADMIN_SNAPSHOT_INTERVAL_MS` | `300000` (5 min) | int (ms) | Cadence at which the admin dashboard's metric snapshotter reads the in-process registry. Production default gives the 24 h chart 288 sample points. E2E tests drop this to ~200 ms so dashboards react in test wall-clock. | `> 0`. | [Admin console](#admin-console) |
| `SLUICE_CONFIG_DIR` | `/etc/sluice/` | string (path) | Policy + providers YAML directory. The loader accepts a closed allow-list of three filenames (`providers.yaml`, `policy.yaml`, `admin.yaml`); any other filename aborts the load. Each file may only carry the top-level keys allowed for its filename. The CLI's `--dir` flag overrides this for one-shot validation. | Path must exist and be a directory at load time. | `cmd/cli/validate.go`, [`internal/config/loader.go`](../internal/config/loader.go) |
| `SLUICE_ENV` | _(empty)_ | string | Populates the OTel `deployment.environment` resource attribute. Set via downward API or Helm values in production; empty omits the attribute. | None. Free-form. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_HTTP_BIND` | `:8585` | string (host:port) | Data-plane listener address. `:8585` binds all interfaces; pin a host (`127.0.0.1:8585`) for loopback-only deployments. | Must parse as `host:port` with a numeric port. Empty host is allowed. | [Core listener and config](#core-listener-and-config) |
| `SLUICE_LOG_FORMAT` | `json` | enum string | Selects the slog handler: `json` (default, structured, production) or `text` (human-readable, dev). | Case-insensitive enum: `json` \| `text`. Anything else returns `ErrUnknownLogFormat`. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_LOG_LEVEL` | `info` | enum string | Minimum slog level. Below the threshold is dropped. | Case-insensitive enum: `debug` \| `info` \| `warn` \| `error`. Anything else returns `ErrUnknownLogLevel`. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_NATS_BUCKET` | `GATEWAY_EVENT_STASH` | string | JetStream Object Store bucket used to stash envelopes that exceed `SLUICE_NATS_STASH_THRESHOLD_BYTES`. The bucket is auto-created on startup. | Non-empty (the default is used when unset). | [Bus and NATS reporting](#bus-and-nats-reporting) |
| `SLUICE_NATS_PUBLISH_QUEUE_SIZE` | `10000` | int | Bounds the publisher's in-process queue between caller goroutines and worker goroutines. When full, `Publish` drops the envelope and increments the drop counter — never blocks. | `> 0`. | [Bus and NATS reporting](#bus-and-nats-reporting) |
| `SLUICE_NATS_STASH_THRESHOLD_BYTES` | `786432` (768 KiB) | int (bytes) | Inline-vs-stashed cutoff. Envelopes whose serialized payload exceeds this length are uploaded to the Object Store and the publish carries an object reference instead. | `> 0`. | [Bus and NATS reporting](#bus-and-nats-reporting) |
| `SLUICE_NATS_STREAM` | `GATEWAY_EVENTS` | string | JetStream stream name reporting events publish to. Created on startup against subject `gateway.>`. | Non-empty (the default is used when unset). | [Bus and NATS reporting](#bus-and-nats-reporting) |
| `SLUICE_NATS_URL` | _(empty)_ | string (URL) | NATS server connection string (e.g. `nats://nats:4222`). Empty disables reporting entirely — the publisher is not wired and events are dropped silently. | Empty disables. No URL parsing at LoadEnv; connection errors surface at startup. | [Bus and NATS reporting](#bus-and-nats-reporting) |
| `SLUICE_OTLP_ENDPOINT` | _(empty)_ | string (host:port or URL) | OTLP metrics exporter target. Empty disables OTLP push entirely (the Prometheus scrape side is unaffected). | Empty disables. No format validation at LoadEnv; exporter dial failures surface at startup. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_OTLP_PROTOCOL` | `grpc` | enum string | OTLP transport. `grpc` is the default; `http/protobuf` for fronts that don't speak gRPC. Only consulted when `SLUICE_OTLP_ENDPOINT` is non-empty. | Case-insensitive enum: `grpc` \| `http/protobuf`. Anything else returns `ErrUnknownOTLPProtocol`. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_PROMETHEUS_BIND` | _(empty)_ | string (host:port) | Listener address for the `/metrics` scrape endpoint. Empty disables the scrape listener; OTLP push is unaffected. | When non-empty, must parse as `host:port` with a numeric port. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_RULES_MAX_GROUP_DEPTH` | `8` | int | Caps recursive descent through nested `RuleGroup` children during evaluation. Guardrail against pathological YAML triggering stack overflow in the evaluator. | Must be in `[1, 64]`. | [Rules engine](#rules-engine) |
| `SLUICE_SHUTDOWN_DRAIN_SECONDS` | `300` (5 min) | int (seconds) | Bounds graceful drain on `SIGTERM` / `SIGINT`. The server stops accepting new requests, then waits up to this many seconds for in-flight requests to complete before hard-killing them. | `> 0`. | [Shutdown and drain](#shutdown-and-drain) |

That's 20 variables total — the 18 names returned by `config.EnvVarNames()` plus two read from other packages.

The CLI validator at `cmd/cli/validate.go` prints "N vars resolved" using `config.EnvVarNames()`; the count reflects exactly the 18 entries in `envVarNames` ([`internal/config/env.go`](../internal/config/env.go)) — every `SLUICE_*` var `LoadEnv` consults itself. The two extras that round the total up to 20 are read elsewhere:

- `SLUICE_ADMIN_PASSWORD` is resolved by [`contracts/admin/admin.go::Config.ResolvePassword`](../contracts/admin/admin.go) at admin-block validation time, not by `LoadEnv`.
- `SLUICE_ENV` is read by [`internal/observability/setup.go`](../internal/observability/setup.go) to populate the OTel `deployment.environment` resource attribute.

Keeping the `LoadEnv` set and the per-package extras separate is what lets the validator print a stable count without claiming ownership of vars it doesn't parse.

---

## Core listener and config

These are the "where do I bind, where do I read YAML from" knobs. Almost every deploy sets at least `SLUICE_CONFIG_DIR` (to point at a mounted secret directory) and `SLUICE_HTTP_BIND` (to pick a non-default port when colocating with a sidecar).

| Variable | Default | Type | Effect |
|---|---|---|---|
| `SLUICE_HTTP_BIND` | `:8585` | string | Data-plane listener address. |
| `SLUICE_CONFIG_DIR` | `/etc/sluice/` | string | Policy + providers YAML directory loaded at startup. |

The data plane (`SLUICE_HTTP_BIND`) is the listener clients hit with `POST /openai/v1/...`, `POST /anthropic/v1/...`, etc. The admin console binds a separate port — see [Admin console](#admin-console). The data plane never serves admin endpoints and vice versa; see CLAUDE.md's "Admin console architecture" note for the rationale.

`SLUICE_CONFIG_DIR` is the only configuration-discovery knob. The loader accepts a closed allow-list of three filenames — `providers.yaml`, `policy.yaml`, and `admin.yaml` — and any other filename in the directory aborts the load. Each file may only carry the top-level keys allowed for its filename (the merge model assumes every key has exactly one canonical home, so duplicate-key collisions are impossible by construction). File contents are trusted (mounted from k8s Secrets or filesystem-permissioned). There is no `${VAR}` or `env:` substitution syntax inside YAML; treat the YAML file as a secret material and mount it from a Secret. See [`configuration-model.md`](configuration-model.md) for the full schema.

---

## Bus and NATS reporting

Reporting is the side channel that carries end-of-pipeline event envelopes (audit, billing, UI live feed) to NATS JetStream. It is independent of the OTel metric pipeline — see [load-bearing invariant #4](../CLAUDE.md#load-bearing-invariants-never-violate). Reporting is optional; leave `SLUICE_NATS_URL` empty in dev and the publisher is never wired.

| Variable | Default | Type | Effect |
|---|---|---|---|
| `SLUICE_NATS_URL` | _(empty)_ | string | NATS connection string. Empty disables reporting entirely. |
| `SLUICE_NATS_STREAM` | `GATEWAY_EVENTS` | string | JetStream stream name. |
| `SLUICE_NATS_BUCKET` | `GATEWAY_EVENT_STASH` | string | Object Store bucket for large-payload stash. |
| `SLUICE_NATS_STASH_THRESHOLD_BYTES` | `786432` (768 KiB) | int | Inline-vs-stashed cutoff. |
| `SLUICE_NATS_PUBLISH_QUEUE_SIZE` | `10000` | int | In-process queue depth between caller and worker goroutines. |

`SLUICE_NATS_URL` is the master switch — empty = no reporting. The publisher's queue (`SLUICE_NATS_PUBLISH_QUEUE_SIZE`) is the backpressure boundary: when full, `Publish` drops the envelope and increments `gateway.events_dropped.total` rather than blocking the request path. **The client must never wait on the bus** — see load-bearing invariant #2.

`SLUICE_NATS_STASH_THRESHOLD_BYTES` controls the envelope pattern: payloads at or below the threshold publish inline; above it, the publisher uploads the raw bytes to the Object Store bucket and publishes a small reference envelope instead. The default 768 KiB sits below NATS's default 1 MiB max-payload limit with headroom for envelope metadata; bump only if your NATS cluster is configured with a larger `max_payload`.

---

## Observability — OTLP, Prometheus, logging

The metrics + logging pipeline. Three knobs control whether each exporter is wired (`SLUICE_PROMETHEUS_BIND`, `SLUICE_OTLP_ENDPOINT`, `SLUICE_LOG_FORMAT`); two control behaviour (`SLUICE_OTLP_PROTOCOL`, `SLUICE_LOG_LEVEL`); one tags the deployment (`SLUICE_ENV`).

| Variable | Default | Type | Effect |
|---|---|---|---|
| `SLUICE_PROMETHEUS_BIND` | _(empty)_ | string | `/metrics` scrape listener. Empty disables. |
| `SLUICE_OTLP_ENDPOINT` | _(empty)_ | string | OTLP push exporter target. Empty disables. |
| `SLUICE_OTLP_PROTOCOL` | `grpc` | enum | OTLP transport: `grpc` or `http/protobuf`. |
| `SLUICE_LOG_FORMAT` | `json` | enum | slog handler: `json` or `text`. |
| `SLUICE_LOG_LEVEL` | `info` | enum | Minimum slog level: `debug`/`info`/`warn`/`error`. |
| `SLUICE_ENV` | _(empty)_ | string | Populates OTel `deployment.environment` resource attribute. |

Prometheus scrape and OTLP push are independent: enable one, the other, both, or neither. `SLUICE_PROMETHEUS_BIND=:9090` exposes `/metrics` at port 9090; `SLUICE_OTLP_ENDPOINT=otel-collector:4317` pushes to an OTLP collector on the gRPC port. Use `SLUICE_OTLP_PROTOCOL=http/protobuf` when fronting OTLP through an HTTP proxy that doesn't speak gRPC.

`SLUICE_LOG_FORMAT=text` is for dev tail-following; never set it in production — log aggregation pipelines (Loki, Datadog) expect the json shape. `SLUICE_LOG_LEVEL=debug` is the verbose mode; it adds per-request body-capture logging that's quite chatty.

`SLUICE_ENV` is purely descriptive — it does not change behaviour, only tags. Set it to `dev`, `staging`, `production`, or whatever your environment naming convention is, so the OTel resource attribute disambiguates dashboards across deploys.

---

## Admin console

The admin console is a separate listener (default `:8081`) with its own auth surface. Whether the listener starts is gated by `admin.enabled` in `admin.yaml`, not an env var — see CLAUDE.md's "Admin console architecture" note. The env vars in this section only tune the console's behaviour once it's enabled.

| Variable | Default | Type | Effect |
|---|---|---|---|
| `SLUICE_ADMIN_PASSWORD` | _(empty)_ | string | HTTP Basic auth password. Wins over yaml `password` when both are set. Required when `admin.enabled: true`. |
| `SLUICE_ADMIN_SNAPSHOT_INTERVAL_MS` | `300000` (5 min) | int (ms) | Metric snapshotter cadence. |
| `SLUICE_ADMIN_LIVE_FEED_CAPACITY` | `100` | int | Live-messages ring size. `0` disables the pane. |
| `SLUICE_ADMIN_LIVE_FEED_BODY_BYTES` | `209715200` (200 MiB) | int | Total byte budget of the body LRU. `0` disables body capture. |
| `SLUICE_ADMIN_LIVE_FEED_BODY_MAX_BYTES` | `8388608` (8 MiB) | int | Per-body capture cap before truncation. |

`SLUICE_ADMIN_PASSWORD` is the production-friendly path to setting the operator password — point a k8s Secret at the env var rather than checking the password into yaml. The yaml `password` field is a dev convenience.

`SLUICE_ADMIN_SNAPSHOT_INTERVAL_MS` matters because the admin dashboard's totals/rates/quantiles tiles are backed by snapshots, not raw counter reads. 5 minutes gives the 24 h window 288 points (every 5 min × 288 = 24 h). Drop it to ~200 ms in e2e tests so the dashboard reflects synthetic traffic before the test fixture tears down. Don't drop it in production — at the default sample cadence the snapshotter is essentially free; at 1 s it becomes a non-trivial fraction of process time.

The live-feed knobs (`*_CAPACITY`, `*_BODY_BYTES`, `*_BODY_MAX_BYTES`) are explored in the next section.

---

## Live feed and body capture

The live messages pane shows a few-minute live tail of completed requests in the admin console. It is **not** an audit log — see CLAUDE.md's "Design for 1M token contexts" memory note. The ring is in-process, lossy on restart, and intentionally small.

| Variable | Default | Type | Effect |
|---|---|---|---|
| `SLUICE_ADMIN_LIVE_FEED_CAPACITY` | `100` | int | Number of completed-request metadata entries the ring holds. `0` disables. |
| `SLUICE_ADMIN_LIVE_FEED_BODY_BYTES` | `209715200` (200 MiB) | int | Total bytes the body LRU may hold across all captured request/response bodies. `0` disables body capture. |
| `SLUICE_ADMIN_LIVE_FEED_BODY_MAX_BYTES` | `8388608` (8 MiB) | int | Largest single body to capture in full. Bodies above the cap are stored head-only with a `truncated: true` flag. |

The ring (`*_CAPACITY`) sizes the message metadata list — once 100 entries are in, the oldest evict on each new arrival. 100 is deliberately small: a 1M-token-context request body can be 4 MB+, and the pane is honest about being a live tail, not a record of truth.

The body store (`*_BODY_BYTES`) is a separate LRU that holds the captured request/response bodies keyed by `event_id`. At the 200 MiB default with the 8 MiB per-body cap, the store holds roughly 25 max-sized bodies or several hundred typical ones. The two LRUs evict independently — a message can show up in the live-tail pane after its body has already been evicted from the body store, in which case `GET /admin/api/v1/messages/{id}/body` returns 404 with `gone: true`.

Three useful disable configurations:

- `SLUICE_ADMIN_LIVE_FEED_CAPACITY=0` — no live-tail pane at all. The `/messages/*` endpoints return 503.
- `SLUICE_ADMIN_LIVE_FEED_CAPACITY=100 SLUICE_ADMIN_LIVE_FEED_BODY_BYTES=0` — metadata-only pane, no body capture. Useful when the gateway is memory-constrained and you only want recent-request visibility.
- Defaults — both panes work, with the 200 MiB body store budget.

`SLUICE_ADMIN_LIVE_FEED_BODY_MAX_BYTES > 0` is required whenever `SLUICE_ADMIN_LIVE_FEED_BODY_BYTES > 0`; setting one without the other is an invalid configuration and `Validate` rejects it.

---

## Rules engine

| Variable | Default | Type | Effect |
|---|---|---|---|
| `SLUICE_RULES_MAX_GROUP_DEPTH` | `8` | int | Caps recursive descent through nested `RuleGroup` children. |

`SLUICE_RULES_MAX_GROUP_DEPTH` exists as a guardrail against pathological YAML that would otherwise stack-overflow the evaluator. Operator-authored policies almost never need more than 3-4 levels — the default 8 is conservative. The upper bound (`MaxRulesMaxGroupDepth`, currently 64) is the validator's hard ceiling; beyond that, the cost of *authoring* a rule tree this deep outweighs any expressive gain. Use a flat priority chain instead.

When evaluation breaches the cap, the evaluator records a `rules.evaluation_depth_exceeded` metric increment and short-circuits as no-match — the request continues without a rule fired.

---

## Shutdown and drain

| Variable | Default | Type | Effect |
|---|---|---|---|
| `SLUICE_SHUTDOWN_DRAIN_SECONDS` | `300` (5 min) | int (seconds) | Maximum graceful drain duration on `SIGTERM` / `SIGINT`. |

On signal receipt the server stops accepting new requests and waits up to `SLUICE_SHUTDOWN_DRAIN_SECONDS` for in-flight requests to finish. After the deadline elapses, any still-running requests are hard-cancelled via context cancellation; the binary exits non-zero. 5 minutes accommodates a long streaming response from any of the supported providers; raise it if you're seeing premature cancellations on long-tail requests in your environment.

---

## Validation and error sentinels

`LoadEnv` parses; `Validate` enforces invariants. The sentinel errors live in [`internal/config/errors.go`](../internal/config/errors.go):

| Sentinel | Fires when |
|---|---|
| `ErrInvalidEnv` | A numeric env var fails to parse, or a bounds check fails (negative duration, zero queue size, etc.). |
| `ErrInvalidBind` | `SLUICE_HTTP_BIND` or `SLUICE_PROMETHEUS_BIND` is not a parseable `host:port` with a numeric port. |
| `ErrUnknownLogLevel` | `SLUICE_LOG_LEVEL` is set to something other than `debug`/`info`/`warn`/`error`. |
| `ErrUnknownLogFormat` | `SLUICE_LOG_FORMAT` is set to something other than `json`/`text`. |
| `ErrUnknownOTLPProtocol` | `SLUICE_OTLP_PROTOCOL` is set to something other than `grpc`/`http/protobuf`. |

`Validate` returns the first violation it finds — fix one error at a time. The errors wrap the env var name and offending value, so the message identifies what to change without consulting source.

`SLUICE_ADMIN_PASSWORD` validation lives in `contracts/admin.Config.Validate` (sentinel `ErrPasswordRequired`) rather than the generic env validator — the admin block runs its own check after the yaml is merged.

---

## Notes on parsing semantics

A handful of conventions the parser applies consistently:

- **Trim before non-empty check.** `envString` and `envInt` both trim whitespace and treat a whitespace-only value as unset. `SLUICE_LOG_LEVEL=" "` falls back to the default `info`, not an empty-string error.
- **Integer parsing is base-10 strict.** No hex, no octal, no SI suffixes. `SLUICE_NATS_STASH_THRESHOLD_BYTES=1M` is rejected; write `1048576`.
- **Enum case-insensitive.** `SLUICE_LOG_LEVEL=DEBUG` works the same as `debug`. The validator lowercases before comparison.
- **Empty disables only where documented.** `SLUICE_NATS_URL`, `SLUICE_OTLP_ENDPOINT`, `SLUICE_PROMETHEUS_BIND`, and the `SLUICE_ADMIN_LIVE_FEED_*` byte/capacity knobs honour an empty/zero value as "off". Other knobs treat empty as "use the default" — there is no way to disable the rules-engine depth cap, the shutdown drain, or the publish queue.
- **No live reload.** Changing any env var at runtime has no effect until the process restarts. This is intentional — the gateway is built around restart-to-change config semantics, and a hot-reload path is explicitly out of scope before v1.2.

---

## Cross-references

- [Resilience policies](./resilience.md) — uses `SLUICE_CONFIG_DIR` for policy definitions; no resilience-specific env vars.
- [`CLAUDE.md`](../CLAUDE.md) — load-bearing invariants for the bus (#2), reporting/telemetry separation (#4), and admin console architecture (memory note).
- [`README.md`](../README.md) — quick-start configuration including `SLUICE_ADMIN_PASSWORD` for the bundled docker-compose.
- [`internal/config/env.go`](../internal/config/env.go) — the canonical struct + parser + validator. Read it if this doc looks suspect.
- [`contracts/admin/admin.go`](../contracts/admin/admin.go) — the admin block, including `SLUICE_ADMIN_PASSWORD` resolution.
- [`internal/observability/setup.go`](../internal/observability/setup.go) — `SLUICE_ENV` consumption for the OTel resource.
