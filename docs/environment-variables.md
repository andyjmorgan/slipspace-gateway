# Environment Variables

Sluice's server config is loaded once at process start from `SLUICE_*` environment variables. There is no live-reload pathway for server-level settings — every value documented here lands on the in-memory `ServerEnv` (or the admin `Config`) at startup and is read-only thereafter. Restart to apply a change.

Policy-level configuration (rules, configurations, api_keys, providers, resilience policies, connectors) is loaded from `SLUICE_CONFIG_DIR` and has a partial live-edit story: rules can be created / edited / deleted through the admin write API and apply to the next request without a restart (see [`docs/admin-console.md → Config write API`](admin-console.md#config-write-api)). Direct YAML edits and the other top-level blocks still require a restart. This page is scoped to env-var-driven server config only.

This page is the operator's lookup reference. The big table below covers every `SLUICE_*` variable the gateway reads, with default, type, effect, and validation rules. The grouped tables that follow exist to give context — when you're tuning a knob, the group's prose tells you what neighbourhood you're in.

Ground truth lives in [`internal/config/env.go`](../internal/config/env.go) (the bulk of the variables), [`contracts/admin/admin.go`](../contracts/admin/admin.go) (the admin password), and [`internal/observability/setup.go`](../internal/observability/setup.go) (the OTel deployment-environment tag). If this doc and the code diverge, the code wins — open a PR.

---

## Table of contents

1. [Complete reference (alphabetical)](#complete-reference-alphabetical)
2. [Core listener and config](#core-listener-and-config)
3. [Connector spool](#connector-spool)
4. [Observability — OTLP, Prometheus, logging](#observability--otlp-prometheus-logging)
5. [Admin console](#admin-console)
6. [Live feed and body capture](#live-feed-and-body-capture)
7. [Rules engine](#rules-engine)
8. [Shutdown and drain](#shutdown-and-drain)
9. [Upstream forwarding](#upstream-forwarding)
10. [Arbiter](#telemetry-service)
11. [Validation and error sentinels](#validation-and-error-sentinels)
12. [Notes on parsing semantics](#notes-on-parsing-semantics)

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
| `SLUICE_CONFIG_DIR` | `/etc/sluice/` | string (path) | Policy + providers YAML directory. The loader reads **every** `*.yaml` file in the directory (filenames are not significant) and merges them by top-level block key; a block set by two files is a duplicate-key error. The conventional `providers.yaml` / `policy.yaml` / `admin.yaml` split is a writer convention, not a loader constraint. Subdirectories and non-`.yaml` entries are skipped. The CLI's `--dir` flag overrides this for one-shot validation. | Path must exist and be a directory at load time. | `cmd/cli/validate.go`, [`internal/config/config_model.go::Load`](../internal/config/config_model.go), [`internal/config/loader.go::ListConfigFiles`](../internal/config/loader.go) |
| `SLUICE_ENV` | _(empty)_ | string | Populates the OTel `deployment.environment` resource attribute. Set via downward API or Helm values in production; empty omits the attribute. | None. Free-form. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_EXTERNAL_URL` | _(empty)_ | string (URL) | The gateway's externally reachable base URL (e.g. `https://sluice.example.com`). Resolves the `{external_url}` template reference in response-side body rewrites — chiefly rebasing provider-returned URLs (Anthropic batches `results_url`) back through the gateway. Empty leaves `{external_url}` unresolved, dropping any rewrite that depends on it. | None. Free-form; no trailing-slash normalisation. | [`docs/actions.md`](actions.md#rewritefield--removefield--appendfield) |
| `SLUICE_HTTP_BIND` | `:8585` | string (host:port) | Data-plane listener address. `:8585` binds all interfaces; pin a host (`127.0.0.1:8585`) for loopback-only deployments. | Must parse as `host:port` with a numeric port. Empty host is allowed. | [Core listener and config](#core-listener-and-config) |
| `SLUICE_LOG_FORMAT` | `json` | enum string | Selects the slog handler: `json` (default, structured, production) or `text` (human-readable, dev). | Case-insensitive enum: `json` \| `text`. Anything else returns `ErrUnknownLogFormat`. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_LOG_LEVEL` | `info` | enum string | Minimum slog level. Below the threshold is dropped. | Case-insensitive enum: `debug` \| `info` \| `warn` \| `error`. Anything else returns `ErrUnknownLogLevel`. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_OTLP_ENDPOINT` | _(empty)_ | string (host:port or URL) | OTLP metrics exporter target. Empty disables OTLP push entirely (the Prometheus scrape side is unaffected). | Empty disables. No format validation at LoadEnv; exporter dial failures surface at startup. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_OTLP_PROTOCOL` | `grpc` | enum string | OTLP transport. `grpc` is the default; `http/protobuf` for fronts that don't speak gRPC. Only consulted when `SLUICE_OTLP_ENDPOINT` is non-empty. | Case-insensitive enum: `grpc` \| `http/protobuf`. Anything else returns `ErrUnknownOTLPProtocol`. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_OTEL_CAPTURE_CONTENT` | `false` | bool | When true, emits the bounded prompt/response content (latest user turn, model response, system instructions, tool definitions) on the GenAI span and the `gen_ai.client.inference.operation.details` event. Default off — content otherwise stays only in the connector spool (invariant #4). When on, content is credential-redacted and size-capped; the byte caps live in `admin.yaml` under `telemetry.content_capture.*` (no env-var override — see [observability.md → Content capture](observability.md#genai-spans-and-events)). | Truthy: `1`/`t`/`true`/`yes`/`on` (case-insensitive); anything else false. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_PROMETHEUS_BIND` | _(empty)_ | string (host:port) | Listener address for the `/metrics` scrape endpoint. Empty disables the scrape listener; OTLP push is unaffected. | When non-empty, must parse as `host:port` with a numeric port. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_REDACT_EXTRA_HEADERS` | _(empty)_ | string (CSV) | Comma-separated header-name substrings appended to the built-in sensitive matcher (`auth` / `api-key` / `apikey` / `token` / `cookie` / `secret` / `sluice-identity`). Any inbound header whose lowercased name contains one of these has its value masked as `[REDACTED]` before it reaches livefeed entries, connector `Record.Request.Headers`, or proxy debug-log envelopes. Use for environment-specific headers (internal tracing IDs that carry tenant identifiers, custom auth schemes, etc.). | Whitespace-trimmed per entry, empty entries dropped, deduped (case-insensitive) against built-ins. Empty / unset keeps the matcher at built-ins only. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_RULES_MAX_GROUP_DEPTH` | `8` | int | Caps recursive descent through nested `RuleGroup` children during evaluation. Guardrail against pathological YAML triggering stack overflow in the evaluator. | Must be in `[1, 64]`. | [Rules engine](#rules-engine) |
| `SLUICE_SESSION_ID_HEADERS` | _(empty)_ | string (CSV) | Comma-separated header names appended, in order, to the built-in session-id fallback chain (`Session-Id` → `X-Claude-Code-Session-Id`). The authoritative `X-Sluice-Session-Id` is always tried first regardless. Lets an operator bundle a custom client's conversation header (e.g. `X-Acme-Conversation-Id`) with no code change. A header also matched by `SLUICE_REDACT_EXTRA_HEADERS` is skipped during resolution, so a promoted session id can never bypass redaction. | Whitespace-trimmed per entry, empty entries dropped. Empty / unset uses the built-in chain only. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_THREAD_ID_HEADERS` | _(empty)_ | string (CSV) | Comma-separated header names appended, in order, to the built-in conversation/thread-id fallback chain (`Thread-Id` → `X-Claude-Code-Agent-Id`). The authoritative `X-Sluice-Thread-Id` is always tried first regardless. Lets an operator promote a custom client's thread header with no code change. A header also matched by `SLUICE_REDACT_EXTRA_HEADERS` is skipped during resolution, so a promoted thread id can never bypass redaction. | Whitespace-trimmed per entry, empty entries dropped. Empty / unset uses the built-in chain only. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_PARENT_ID_HEADERS` | _(empty)_ | string (CSV) | Comma-separated header names appended, in order, to the built-in parent-conversation fallback chain (`X-Codex-Parent-Thread-Id`). The authoritative `X-Sluice-Parent-Conversation-Id` is always tried first regardless. Lets an operator promote a custom client's parent-thread header with no code change. A header also matched by `SLUICE_REDACT_EXTRA_HEADERS` is skipped during resolution, so a promoted parent id can never bypass redaction. | Whitespace-trimmed per entry, empty entries dropped. Empty / unset uses the built-in chain only. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_AGENT_ID_HEADERS` | _(empty)_ | string (CSV) | Comma-separated header names forming the agent-id fallback chain. The built-in chain is **empty** — `gen_ai.agent.id` is reserved for genuinely named agents, and `X-Claude-Code-Agent-Id` moved to the thread-id chain (PR #320) — so this is the entire fallback list. The authoritative `X-Sluice-Agent-Id` is always tried first regardless. Lets an operator promote a custom client's agent header with no code change. A header also matched by `SLUICE_REDACT_EXTRA_HEADERS` is skipped during resolution, so a promoted agent id can never bypass redaction. | Whitespace-trimmed per entry, empty entries dropped. Empty / unset means only `X-Sluice-Agent-Id` resolves. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_USER_ID_HEADERS` | _(empty)_ | string (CSV) | Comma-separated header names forming the user-id fallback chain. Unlike session/agent id there is **no shipped client default** (no client emits a standard end-user header), so this is the entire fallback list. The authoritative `X-Sluice-User-Id` is always tried first regardless. Lets an operator promote a custom client's user header with no code change. A header also matched by `SLUICE_REDACT_EXTRA_HEADERS` is skipped during resolution, so a promoted user id can never bypass redaction. | Whitespace-trimmed per entry, empty entries dropped. Empty / unset means only `X-Sluice-User-Id` resolves. | [Observability](#observability--otlp-prometheus-logging) |
| `SLUICE_SHUTDOWN_DRAIN_SECONDS` | `300` (5 min) | int (seconds) | Bounds graceful drain on `SIGTERM` / `SIGINT`. The server stops accepting new requests, then waits up to this many seconds for in-flight requests to complete before hard-killing them. | `> 0`. | [Shutdown and drain](#shutdown-and-drain) |
| `SLUICE_SPOOL_ROOT` | `/var/lib/sluice/spool` | string (path) | On-disk root for the connector spool. The spool constructs `records/<connector>/{active,sealed,uploading,deadletter,quarantine}/` beneath this. Mount a PVC in production so segments survive process restart. | Non-empty. The path is created lazily on first use; the process must have write permissions. | [Connector spool](#connector-spool) |
| `SLUICE_TRANSLATE_LOSSY_HEADER` | `false` | bool | When true, emits an `X-Sluice-Translation-Lossy` response header listing source features dropped during cross-provider translation (the `translate` rule action). Developer/debug aid — the always-on `gateway.translation.field_drops.total` counter carries the same signal for operators, so the consumer-facing header stays off by default. | Standard bool parse (`1`/`t`/`true`/…). | [`docs/actions.md`](actions.md#translate) |
| `SLUICE_UPSTREAM_RESPONSE_HEADER_TIMEOUT_SECONDS` | `120` (2 min) | int (seconds) | Caps time-to-first-byte from the upstream provider — the wait for response headers after the request body is fully written. Stamped onto every proxy transport as `ResponseHeaderTimeout`. Only bounds the header wait; once headers arrive, streaming bodies are not subject to it. | `>= 120`. Below the floor risks cancelling slow-but-healthy upstreams mid-handshake. | [Upstream forwarding](#upstream-forwarding) |
| `SLUICE_WEBHOOK_ALLOW_PRIVATE` | _(unset)_ | bool (string `1` / `true`) | **Test-only.** Disables the per-call SSRF DNS guard on every webhook connector. The e2e harness sets this so its `httptest.Server` (bound to loopback) is reachable. **Never set this in production.** | `1` / `true` enables; anything else (including unset) leaves the guard on. | [Connectors → webhook → SSRF guard](connectors.md#ssrf-guard) |

The CLI validator at `cmd/cli/validate.go` prints "N vars resolved" using `config.EnvVarNames()`; the count reflects exactly the entries in `envVarNames` ([`internal/config/env.go`](../internal/config/env.go)) — every `SLUICE_*` var `LoadEnv` consults itself. Three extras are read elsewhere:

- `SLUICE_ADMIN_PASSWORD` is resolved by [`contracts/admin/admin.go::Config.ResolvePassword`](../contracts/admin/admin.go) at admin-block validation time, not by `LoadEnv`.
- `SLUICE_ENV` is read by [`internal/observability/setup.go`](../internal/observability/setup.go) to populate the OTel `deployment.environment` resource attribute.
- `SLUICE_WEBHOOK_ALLOW_PRIVATE` is read by [`contracts/config/connectors_validate.go::webhookAllowPrivateNetworks`](../contracts/config/connectors_validate.go) when a webhook connector resolves its destination, gating the per-call SSRF DNS guard. Test-only.

Keeping the `LoadEnv` set and the per-package extras separate is what lets the validator print a stable count without claiming ownership of vars it doesn't parse.

---

## Core listener and config

These are the "where do I bind, where do I read YAML from" knobs. Almost every deploy sets at least `SLUICE_CONFIG_DIR` (to point at a mounted secret directory) and `SLUICE_HTTP_BIND` (to pick a non-default port when colocating with a sidecar).

| Variable | Default | Type | Effect |
|---|---|---|---|
| `SLUICE_HTTP_BIND` | `:8585` | string | Data-plane listener address. |
| `SLUICE_CONFIG_DIR` | `/etc/sluice/` | string | Policy + providers YAML directory loaded at startup. |

The data plane (`SLUICE_HTTP_BIND`) is the listener clients hit with `POST /openai/v1/...`, `POST /anthropic/v1/...`, etc. The admin console binds a separate port — see [Admin console](#admin-console). The data plane never serves admin endpoints and vice versa; see CLAUDE.md's "Admin console architecture" note for the rationale.

`SLUICE_CONFIG_DIR` is the only configuration-discovery knob. The loader (`internal/config/config_model.go::Load`) reads every `*.yaml` file in the directory and merges them by top-level block key (`providers`, `groups`, `configurations`, `api_keys`, `rules`, `connectors`, `admin`, `telemetry`) — filenames carry no meaning to the loader. The same block set by two files is a duplicate-key error, so each block has exactly one canonical home across the directory. The conventional `providers.yaml` / `policy.yaml` / `admin.yaml` split is just how the writer lays blocks out; you may split or combine them however you like. Subdirectories and non-`.yaml` entries are skipped. File contents are trusted (mounted from k8s Secrets or filesystem-permissioned). There is no `${VAR}` or `env:` substitution syntax inside YAML; treat the YAML file as secret material and mount it from a Secret. See [`configuration-model.md`](configuration-model.md) for the full schema.

---

## Connector spool

The connector spool is the disk-backed buffer between OnComplete and the connector destinations (s3, azure_blob, webhook). It is independent of the OTel metric pipeline — see [load-bearing invariant #4](../CLAUDE.md). The spool is constructed lazily; deployments with no `connectors:` block leave the spool unwired.

| Variable | Default | Type | Effect |
|---|---|---|---|
| `SLUICE_SPOOL_ROOT` | `/var/lib/sluice/spool` | string (path) | On-disk root for the spool. Per-connector subdirectories (`records/<connector>/{active,sealed,uploading,deadletter,quarantine}/`) live under this. |
| `SLUICE_WEBHOOK_ALLOW_PRIVATE` | _(unset)_ | bool (`1` / `true`) | **Test-only.** Disables the per-call SSRF DNS guard on every webhook connector in the process. Never set in production. |

`SLUICE_SPOOL_ROOT` should point at a Kubernetes PVC in production so sealed segments survive process restarts. Pointing it at a tmpfs is supported for ephemeral-by-design deployments but accepts the loss-on-restart semantics. `Validate` rejects an empty value.

Per-track tuning (ring depth, rotation, retry, breaker) is not env-driven — those knobs live on the per-connector YAML entry. See [spool.md](spool.md) for the runtime model and [connectors.md](connectors.md) for the per-connector rotation override.

---

## Observability — OTLP, Prometheus, logging

The metrics + logging pipeline. Three knobs control whether each exporter is wired (`SLUICE_PROMETHEUS_BIND`, `SLUICE_OTLP_ENDPOINT`, `SLUICE_LOG_FORMAT`); two control behaviour (`SLUICE_OTLP_PROTOCOL`, `SLUICE_LOG_LEVEL`); one tags the deployment (`SLUICE_ENV`).

| Variable | Default | Type | Effect |
|---|---|---|---|
| `SLUICE_PROMETHEUS_BIND` | _(empty)_ | string | `/metrics` scrape listener. Empty disables. |
| `SLUICE_OTLP_ENDPOINT` | _(empty)_ | string | OTLP push exporter target. Empty disables. |
| `SLUICE_OTLP_PROTOCOL` | `grpc` | enum | OTLP transport: `grpc` or `http/protobuf`. |
| `SLUICE_OTEL_CAPTURE_CONTENT` | `false` | bool | Emit bounded, redacted prompt/response content on the GenAI span + operation-details event. Default off. Byte caps come from `admin.yaml` (`telemetry.content_capture.*`); see [observability.md](observability.md#genai-spans-and-events). |
| `SLUICE_LOG_FORMAT` | `json` | enum | slog handler: `json` or `text`. |
| `SLUICE_LOG_LEVEL` | `info` | enum | Minimum slog level: `debug`/`info`/`warn`/`error`. |
| `SLUICE_ENV` | _(empty)_ | string | Populates OTel `deployment.environment` resource attribute. |
| `SLUICE_SESSION_ID_HEADERS` | _(empty)_ | string (CSV) | Extra session-id fallback headers, appended after the built-in chain. `X-Sluice-Session-Id` always wins. |
| `SLUICE_THREAD_ID_HEADERS` | _(empty)_ | string (CSV) | Extra conversation/thread-id fallback headers, appended after the built-in chain (`Thread-Id` → `X-Claude-Code-Agent-Id`). `X-Sluice-Thread-Id` always wins. |
| `SLUICE_PARENT_ID_HEADERS` | _(empty)_ | string (CSV) | Extra parent-conversation fallback headers, appended after the built-in chain (`X-Codex-Parent-Thread-Id`). `X-Sluice-Parent-Conversation-Id` always wins. |
| `SLUICE_AGENT_ID_HEADERS` | _(empty)_ | string (CSV) | Agent-id fallback headers. The built-in chain is empty (`gen_ai.agent.id` is reserved for named agents; PR #320), so this is the whole chain. `X-Sluice-Agent-Id` always wins. |
| `SLUICE_USER_ID_HEADERS` | _(empty)_ | string (CSV) | User-id fallback headers. No shipped client default, so this is the whole chain. `X-Sluice-User-Id` always wins. |

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

## Upstream forwarding

| Variable | Default | Type | Effect |
|---|---|---|---|
| `SLUICE_UPSTREAM_RESPONSE_HEADER_TIMEOUT_SECONDS` | `120` (2 min) | int (seconds) | Time-to-first-byte cap on the upstream — the wait for response headers after the request body is fully written. |

This maps to `net/http.Transport.ResponseHeaderTimeout` on every transport the proxy mints (one per upstream base URL). It bounds **only** the header wait: how long the gateway will sit waiting for the provider to start replying after the request is sent. Once the upstream returns its response headers, the body — including an arbitrarily long SSE stream — is not subject to this timeout; streaming completions that run for many minutes are unaffected.

A provider under load can legitimately take more than a minute to emit the first byte of a long completion, so the default and enforced floor is `120`. `Validate` rejects anything below `120` (`MinUpstreamResponseHeaderTimeoutSeconds`) because a too-aggressive value surfaces as spurious `502 Bad Gateway` responses on slow-but-healthy upstreams — the transport cancels the connection mid-handshake and the proxy's `ErrorHandler` fires. Raise it (e.g. `300`) for providers or models with long pre-fill latencies; there is no upper bound.

The connection-establishment timeouts (TCP dial 10 s, TLS handshake 10 s) and the idle keep-alive timeout (90 s) are not env-driven — they are fixed in [`internal/proxy/transport.go`](../internal/proxy/transport.go).

---

## Arbiter

The Arbiter is a **separate binary** (`cmd/arbiter`), not the gateway. It does **not** read the `SLUICE_*` variables above — those land on the gateway's `ServerEnv`. The Arbiter takes a single YAML config file plus two environment variables, both consumed in [`cmd/arbiter/main.go`](../cmd/arbiter/main.go). Its YAML schema, listener defaults, and HMAC trust model are documented in [`arbiter.md`](arbiter.md); this table is only the env surface.

| Variable | Default | Type | Effect |
|---|---|---|---|
| `SLUICE_TELEMETRY_CONFIG` | _(empty)_ | string (path) | Path to the Arbiter's YAML config file. Read as the default value of the `-config` flag (the flag wins when both are set). When neither is set, `config.Load` returns `ErrNoConfig` and the process exits non-zero — the service has no usable defaults without a Postgres DSN and gateway registry. |
| `LOG_LEVEL` | `info` | enum string | Minimum slog level for the telemetry binary's JSON logger: `debug` / `info` / `warn` / `error` (case-insensitive; unknown values fall back to `info`). Note this is bare `LOG_LEVEL`, **not** the gateway's `SLUICE_LOG_LEVEL`. |

Everything else the service needs — the HTTP and OTLP listener binds (defaults `0.0.0.0:8686` and `0.0.0.0:8687`), the Postgres DSN, the console Basic-auth credentials, the per-gateway HMAC secrets, and the gen_ai content cap (`content_max_bytes`, default 16 KiB) — comes from the YAML file, not env vars. Like the gateway, the file is trusted material with no `${VAR}` expansion. See [`arbiter.md`](arbiter.md) for the full reference and [`arbiter-webhook.md`](arbiter-webhook.md) for the Record ingest contract.

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
- **Integer parsing is base-10 strict.** No hex, no octal, no SI suffixes. `SLUICE_ADMIN_LIVE_FEED_BODY_BYTES=1M` is rejected; write `1048576`.
- **Enum case-insensitive.** `SLUICE_LOG_LEVEL=DEBUG` works the same as `debug`. The validator lowercases before comparison.
- **Empty disables only where documented.** `SLUICE_OTLP_ENDPOINT`, `SLUICE_PROMETHEUS_BIND`, and the `SLUICE_ADMIN_LIVE_FEED_*` byte/capacity knobs honour an empty/zero value as "off". Other knobs treat empty as "use the default" — there is no way to disable the rules-engine depth cap or the shutdown drain. Connector capture is enabled by the presence of a `connectors:` block in YAML, not via an env var.
- **No live reload.** Changing any env var at runtime has no effect until the process restarts. This is intentional — the gateway is built around restart-to-change config semantics, and a hot-reload path is explicitly out of scope before v1.2.

---

## Cross-references

- [Resilience policies](./resilience.md) — uses `SLUICE_CONFIG_DIR` for policy definitions; no resilience-specific env vars.
- [Connector spool](./spool.md) — runtime semantics behind `SLUICE_SPOOL_ROOT`.
- [Connectors](./connectors.md) — destination types, including the webhook SSRF guard.
- [`CLAUDE.md`](../CLAUDE.md) — load-bearing invariants for the spool (#2), reporting/telemetry separation (#4), and admin console architecture (memory note).
- [`README.md`](../README.md) — quick-start configuration including `SLUICE_ADMIN_PASSWORD` for the bundled docker-compose.
- [`internal/config/env.go`](../internal/config/env.go) — the canonical struct + parser + validator. Read it if this doc looks suspect.
- [`contracts/admin/admin.go`](../contracts/admin/admin.go) — the admin block, including `SLUICE_ADMIN_PASSWORD` resolution.
- [`internal/observability/setup.go`](../internal/observability/setup.go) — `SLUICE_ENV` consumption for the OTel resource.
