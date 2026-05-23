# Observability

Sluice ships three independent observability channels: OTel metrics for operational counters and quantiles, structured `log/slog` JSON for debugging and audit, and the connector spool for billing and downstream consumers. They share a correlation ID and resolved per-request labels but never overlap responsibilities — meters never carry bodies, captured records never replace metrics, logs never become the wire-of-record for billing.

This page is the operator reference for everything the gateway emits: every meter, the Go runtime and process collectors exposed on `/metrics`, every per-request log field, and the in-process snapshotter that backs the admin dashboard. The resilience.* and cb.* meters are covered alongside the orchestrator in [docs/resilience.md](resilience.md); this page links out rather than duplicating them. For the connector spool's wire shape (the captured-record format) see [spool.md](spool.md) and [connectors.md](connectors.md).

---

## Table of contents

1. [Three channels](#three-channels)
2. [OTel pipeline](#otel-pipeline)
3. [Meters](#meters)
   - [Requests](#requests)
   - [Tokens](#tokens)
   - [Rules](#rules)
   - [Tags](#tags)
   - [Resilience and circuit breaker](#resilience-and-circuit-breaker)
   - [Admin](#admin)
   - [Errors](#errors)
   - [Crash safety](#crash-safety)
   - [Reserved](#reserved)
4. [Runtime and process collectors](#runtime-and-process-collectors)
5. [Histogram bucket boundaries](#histogram-bucket-boundaries)
6. [Snapshotter](#snapshotter)
7. [Health endpoint](#health-endpoint)
8. [Structured logs](#structured-logs)
9. [Connector-captured records](#connector-captured-records)
10. [Cross-references](#cross-references)

---

## Three channels

> **Metrics count. Logs explain. Connectors bill.**

The three channels are intentionally disjoint. Every signal the gateway emits belongs to exactly one of them, and the consumers — Prometheus / Grafana, log aggregator, connector destination — should never have to reach across to compose a view.

**OTel metrics** are counters, histograms, and gauges scraped by Prometheus and/or pushed via OTLP. They answer *how many*, *how fast*, and *how often*. They are bounded-cardinality — labels come from operator-authored configuration (provider names, endpoint names, configuration names, rule names, policy/target names) and never from client input. They are **not** the audit log: provider response bodies, prompt contents, and per-request payloads never appear in a metric label.

**Structured logs** are JSON records emitted via `log/slog` to stdout. They carry a service header (`service`, `version`), a per-request envelope (`correlation_id`, `provider`, `endpoint`, `model`, …) and the human-readable narrative the operator needs to debug a failing request. They are **not** billable: log shipping is best-effort, the storage tier is operator-chosen, and provider response bodies are deliberately excluded — bodies go to the connector spool.

**Connector-captured records** are end-of-pipeline payloads encoded as ndjson.zst segments written to a per-connector spool and shipped to operator-configured destinations (S3, Azure Blob, webhook). They are the wire-of-record for billing, audit, and downstream replay. The spool is non-blocking — see [load-bearing invariant 2](../CLAUDE.md) — drops occur at the hot path (ring full) or on the disk path (filesystem full) rather than blocking the request. They are **not** operational telemetry: a Grafana panel must never read records from S3; the equivalent OTel meter exists for every dimension a dashboard cares about.

---

## OTel pipeline

`internal/observability/setup.go` constructs the SDK MeterProvider with three potential readers:

- **Prometheus exporter** — enabled when `gateway.prometheus.bind_addr` is non-empty. Registers an `otelprom` exporter against a fresh `prometheus.Registry` and exposes a `promhttp.Handler` the data plane mounts at `/metrics` on the Prometheus listener.
- **OTLP exporter** — enabled when `gateway.otlp.endpoint` is non-empty. `gateway.otlp.protocol` selects between gRPC (`grpc`, default) and HTTP-protobuf (`http/protobuf`, also `http`). Wrapped in a `sdkmetric.PeriodicReader` so the SDK batches pushes on its own cadence.
- **ManualReader** — **always attached**, regardless of the other two. The [snapshotter](#snapshotter) pulls from this reader on a configurable interval; nothing external consumes it. The cost when both Prom scrape and OTLP push are disabled is essentially zero — the reader only runs work when the snapshotter calls `Collect`.

The same `MeterProvider` is also registered globally via `otel.SetMeterProvider`, so stray code reaching for the global meter still lands on the same instrument set.

```mermaid
flowchart LR
    A[Meter handles<br/>internal/observability/meters.go] --> MP[SDK MeterProvider]
    MP --> R1[Prom exporter<br/>scrape via /metrics]
    MP --> R2[OTLP PeriodicReader<br/>gRPC or HTTP-protobuf]
    MP --> R3[ManualReader<br/>always attached]
    R1 -. enabled by<br/>gateway.prometheus.bind_addr .-> R1
    R2 -. enabled by<br/>gateway.otlp.endpoint .-> R2
    R3 --> Snap[Snapshotter<br/>5-min ring]
    Snap --> Admin[Admin dashboard handlers<br/>/api/v1/dashboard, /api/v1/timeseries]
```

`Provider.Shutdown` collapses every exporter shutdown and the MeterProvider shutdown into a single idempotent `once.Do` so graceful termination handlers can call it multiple times without double-flushing.

Resource attributes stamped on every metric series:

| Attribute | Source |
|---|---|
| `service.name` | `BuildInfo.Service` (always `gateway` for `cmd/gateway`) |
| `service.version` | `BuildInfo.Version`, sourced from `internal/version.Version` |
| `deployment.environment` | `SLUICE_ENV` env var, omitted when empty |

---

## Meters

Every gateway instrument is registered under the OTel meter scope `sluice-gateway` and prefixed with `gateway.` so they sort together in Prometheus and stay disjoint from any future sibling service (`a2a.`, `mcp.`, …). All names live as exported constants in [`internal/observability/meters.go`](../internal/observability/meters.go) (`Metric*`); call sites reference the constants rather than string literals.

### Requests

The data-plane request lifecycle. Per-request counters fire **exactly once per inbound request** — multi-attempt orchestration suppresses the per-attempt terminal publish so the closure is invoked once at FireTerminal time. Per-attempt phenomena (TTFB, transport errors) stay attempt-shaped because that's the shape of the underlying event.

| Metric | Type | Labels | Unit | What it counts |
|---|---|---|---|---|
| `gateway.requests.total` | counter | `provider, endpoint, model, configuration, status_code` | 1 | Total requests completed. One increment per inbound request at OnComplete, after rule mutation and (for orchestrated requests) after the resilience orchestrator has resolved a final status. |
| `gateway.request.duration` | histogram | `provider, endpoint, model, configuration, status_code` | s | End-to-end request duration. Recorded alongside `requests.total` so the two share an attribute set and Grafana can join rate + quantile by the same dimensions. |
| `gateway.active_requests` | up-down counter | (none) | 1 | Requests currently in flight. Incremented at OnRequestStart, decremented at OnComplete. |
| `gateway.request.time_to_first_byte` | histogram | `provider, endpoint, model` | s | Time from request acceptance to the first response byte. Recorded only when upstream returned headers; streaming and non-streaming both observe. No `configuration` or `status_code` — TTFB is a transport metric, not a billing one. |
| `gateway.upstream_errors.total` | counter | `provider, endpoint, model` | 1 | Errors returned by upstream providers. Bumped from `OnUpstreamError` (transport-level failure: connection refused, EOF before headers, timeout before status line). Provider 4xx / 5xx with a body do **not** fire this — those are normal completions. |
| `gateway.request.panics.total` | counter | `provider, endpoint` | 1 | Panics caught by the request-path recovery middleware (`cmd/gateway/recover.go`). A non-zero rate implies a buggy middleware or handler leaked a panic that the recovery filter converted to a 500; investigate. |

`model` is sanitised at label-emit time: empty input passes through, anything over `modelLabelMaxLen` (80 chars) collapses to `other`. This guards the cardinality of `model` against a misbehaving client that injects long or unique strings.

### Tokens

Provider-reported token usage extracted from the upstream response body. All four counters share the request labelset so token rate and traffic rate join cleanly in Grafana. Counters are bumped **only for non-zero buckets** — a request that doesn't include `usage` produces no observation rather than a zero-valued one.

| Metric | Type | Labels | Unit | What it counts |
|---|---|---|---|---|
| `gateway.tokens.input.total` | counter | `provider, endpoint, model, configuration, status_code` | 1 | Sum of prompt tokens billed for the request, from the upstream `usage` block. Includes cached input (also reported separately in `tokens.cached.total`). Zero when the upstream omitted `usage` — typical for streaming without `include_usage`, cancelled streams, or Gemini preview models. |
| `gateway.tokens.output.total` | counter | `provider, endpoint, model, configuration, status_code` | 1 | Tokens the model generated. For providers that bill reasoning tokens separately (OpenAI o1/o3, Gemini thoughts) they are included — the field reflects what the customer pays for, not just visible output. |
| `gateway.tokens.cached.total` | counter | `provider, endpoint, model, configuration, status_code` | 1 | Share of `tokens.input.total` the provider served from its prompt cache and billed at the cached-read price. Informational; already counted in input, not a deduction. |
| `gateway.tokens.cache_creation.total` | counter | `provider, endpoint, model, configuration, status_code` | 1 | Share of `tokens.input.total` billed at the cache-write premium. Anthropic-only today — OpenAI and Gemini cache writes are implicit and not separately billed, so this stays zero for them. |

Token capture is gated on the live-feed response buffer being attached to context. When bodies are disabled (`SLUICE_LIVEFEED_BODIES_ENABLED=false`) tokens stay zero and the counters don't fire.

### Rules

The rules engine instruments. Labels are bounded by the configured policy library — `rule_name` and `rule_id` come from operator YAML (or the control plane mint path), never from client input.

| Metric | Type | Labels | Unit | What it counts |
|---|---|---|---|---|
| `gateway.rule.matches.total` | counter | `rule_name, rule_id, terminated, action_count` | 1 | Rules that matched on a request. Bumped from the evaluator's match loop, after the condition matched and the actions ran. `terminated=true` iff a terminating action (`returnStatusCode`, `llmImpersonation`) short-circuited the pipeline. `action_count` is the number of action types that actually applied. |
| `gateway.rule.errors.total` | counter | `rule_name, rule_id, error_kind` | 1 | Action execution failures during rule evaluation. `error_kind` is a small fixed taxonomy: `group_depth` (RuleGroup tree exceeded the configured cap), `action_apply` (an Apply call returned an error), `body_remarshal` (typed-body re-marshal failed). |
| `gateway.rule.evaluation.duration` | histogram | `configuration` | s | Per-request rule-evaluation cycle duration. Sub-millisecond resolution since evaluation runs synchronously on the request path; the long tail captures pathological policies (deep groups, regex catastrophes). |

`rule_id` is the UUID minted by the control plane on author; static YAML-authored rules leave it empty and `rule_name` is the stable handle.

### Tags

| Metric | Type | Labels | Unit | What it counts |
|---|---|---|---|---|
| `gateway.tags.applied.total` | counter | `tag` | 1 | Applications of the `addTag` rule action. One increment per (request, tag) pair after the rules middleware drained tags from the post-rule `MutableState`. The label is bounded by the operator-defined tag library — `provider` / `endpoint` / `configuration` are deliberately omitted to keep cardinality from blowing up in the cross-product with everything else. |

The tag side-channel runs in lockstep with `Record.Tags` on captured connector records; both come from the same drain at OnComplete.

### Resilience and circuit breaker

Covered alongside the orchestrator in [docs/resilience.md → Observability](resilience.md#observability). The full set is:

- `gateway.resilience.attempts.total` (counter, labels: `policy, target, outcome`)
- `gateway.resilience.attempt.duration` (histogram, labels: `policy, target`)
- `gateway.resilience.attempts_per_request` (histogram, labels: `policy`)
- `gateway.resilience.outcome.total` (counter, labels: `policy, outcome`)
- `gateway.cb.state` (observable gauge, labels: `policy, target, pod, state_name`)
- `gateway.cb.transitions.total` (counter, labels: `policy, target, to_state`)

`gateway.cb.state` is the one ObservableGauge in the registry — it does not push, it pulls. Each collection invokes the callback registered by `RegisterCircuitBreakerStateGauge`, which iterates `BreakerStore.Snapshot()` and emits one observation per known `(policy, target)` pair. The `pod` label disambiguates multi-pod deployments since CB state is per-pod and in-memory.

### Admin

The admin console runs on a separate listener (default `:8081`); its traffic is metered separately from the data plane so SLO panels for the gateway stay disjoint from operator UI traffic.

| Metric | Type | Labels | Unit | What it counts |
|---|---|---|---|---|
| `gateway.admin.requests.total` | counter | `route, status` | 1 | Requests handled by the management-console listener. `route` is a fixed-cardinality matched-route string (`/api/v1/auth/me`, `/api/v1/messages/stream`, `static` for SPA assets, `fallback` for the index.html SPA fallback) rather than a raw URL — keeps cardinality bounded even when the SPA serves arbitrary asset paths. `status` is the response HTTP status code. |
| `gateway.admin.config_exports.total` | counter | `status` | 1 | Redacted-config bundle downloads served by `/api/v1/config/export`. Separated from the broader admin counter so a spike on non-200 surfaces export failures (malformed YAML on disk, missing config dir) without scanning logs. |

### Errors

| Metric | Type | Labels | Unit | What it counts |
|---|---|---|---|---|
| `gateway.error_responses.total` | counter | `layer, code, status_code` | 1 | JSON error responses written by the gateway middleware chain via `internal/httperr.Writer`. `layer` names the middleware that produced the error (`routing`, `handler`, …); `code` is the stable machine-readable identifier (`no_route`, `forward_failed`, `panic_recovered`, …); `status_code` is the HTTP status. This is the counter that powers the dashboard's "errors by layer" breakdown. |

### Crash safety

Both crash-safety counters surface a panic that the gateway's `recover()` wrappers converted to a logged error rather than letting the process exit. A non-zero rate on either is an operator-attention signal.

| Metric | Type | Labels | Unit | What it counts |
|---|---|---|---|---|
| `gateway.goroutine.panics.total` | counter | `site` | 1 | Panics caught by `safego.Go` in background goroutines (process kept alive). `site` is the identifier the caller passed to `safego.Go` (e.g. `bus.publisher.worker`, `bus.publisher.stop_join`). Implies an unhandled edge in a background worker; the parent caller doesn't see it because the panic was off the request path. |
| `gateway.request.panics.total` | counter | `provider, endpoint` | 1 | Panics caught by the request-path recovery middleware (`cmd/gateway/recover.go`). The client received a 500 but the process stayed up. Implies a buggy middleware or handler is leaking panics — fix the underlying bug; do not rely on the recovery filter as a long-term substitute. |

### Reserved

Two counters exist in the registry but have no production call sites yet. They are deliberately reserved so v1.2+ wiring lands without adding instruments mid-release.

| Metric | Type | Labels | Unit | Intent |
|---|---|---|---|---|
| `gateway.unmapped_fields.total` | counter | (none, reserved) | 1 | Unknown fields detected on inbound provider payloads. The DynamicProperties safety net catches these at unmarshal time; once the bodycapture middleware wires up the side-channel report, this counter will fire and unmapped-field metadata will join captured connector records. |
| `gateway.config_reload.total` | counter | (none, reserved) | 1 | Configuration reload attempts. Hot reload is a v1.2+ task; the counter is reserved against the eventual `fsnotify`-based reloader. |

---

## Runtime and process collectors

In addition to the OTel-bridged `gateway.*` meters, the Prometheus `/metrics` endpoint exposes the Go runtime and process collectors registered against the same `prometheus.Registry`. These cover the runtime telemetry that `gateway.*` deliberately doesn't model — memory pressure, goroutine counts, fd usage, CPU time. They never replace the gateway's own counters; they sit alongside them.

The registration happens in [`internal/observability/setup.go`](../internal/observability/setup.go) (~line 153):

```go
reg.MustRegister(
    collectors.NewGoCollector(),
    collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
)
```

| Metric | What it surfaces | Why operators care |
|---|---|---|
| `go_memstats_*` | Heap, stack, GC pause, allocation counters. | Memory pressure visibility. The body store and live-feed ring are bounded but request-handler allocations are not; a sustained heap climb usually means a handler is leaking. |
| `go_goroutines` | Live goroutine count. | A monotonic climb is the canonical "goroutine leak" signal. Steady-state on a healthy gateway is roughly `(active_requests × 2) + (workers × tracks) + (small fixed overhead)`. |
| `process_resident_memory_bytes` | OS-reported RSS. | The dispositive answer to "how much memory is this pod using". `go_memstats_alloc_bytes` is heap-only; RSS includes goroutine stacks, runtime metadata, mmap'd files. |
| `process_cpu_seconds_total` | OS-reported user + system CPU. | Pair with `gateway.requests.total` to compute CPU-per-request. The natural diagnostic for "the gateway feels slower today". |
| `process_open_fds` | Currently-open file descriptors. | The spool keeps one fd open per active segment; webhook connectors open a transient fd per upload. A monotonic climb means an fd leak — usually a missed `Close()` on an error path. |

The collectors are registered **only when** `SLUICE_PROMETHEUS_BIND` is non-empty — same gate as the rest of the `/metrics` surface. OTLP push does not carry these collectors (OTel's SDK has its own runtime instrumentation; we don't bridge the Prometheus ones across).

---

## Histogram bucket boundaries

Bucket boundaries are package-level vars in [`internal/observability/meters.go`](../internal/observability/meters.go) (not consts because Go doesn't permit composite literal constants). Each slice is read once by `NewMeters` and never mutated.

| Histogram | Buckets | Unit | Rationale |
|---|---|---|---|
| `gateway.request.duration`<br/>`gateway.resilience.attempt.duration` | `0, 0.1, 0.5, 1, 2, 5, 10, 30, 60, 120` | s | Covers the full envelope from millisecond completions (cached responses, fast 4xx) up to the streaming long tail without burning too many buckets in the middle. The resilience attempt histogram reuses the same shape so dashboards correlate end-to-end vs per-attempt without rebasing. |
| `gateway.request.time_to_first_byte` | `0, 0.05, 0.1, 0.25, 0.5, 1, 2, 5` | s | TTFB is dominated by upstream queue latency; fine-grained low buckets matter, the long tail past 5s is rare and is captured by the +Inf bucket. |
| `gateway.events.inline_bytes` | `1024, 4096, 16384, 65536, 262144, 786432` | By | Powers of 4 from 1 KiB to 768 KiB (the stash threshold). The top bucket is precisely the threshold so the +Inf bucket counts stashed-eligible payloads. |
| `gateway.rule.evaluation.duration` | `0, 0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1` | s | Sub-millisecond resolution since rule evaluation is synchronous on the request path. The long tail anchors at 1s to capture pathological policies (deep groups, regex catastrophes) without polluting the common case. |
| `gateway.resilience.attempts_per_request` | `1, 2, 3, 5, 10` | 1 | Integer-tuned for the realistic spread: most requests are single-shot, multi-target failover rarely exceeds 3, and the +Inf bucket past 10 covers pathological policy fan-out. |

---

## Snapshotter

The snapshotter is the in-process windowed metric store that backs the admin console's dashboard. It pulls from the always-attached ManualReader on a configurable interval and keeps a bounded ring of `Sample` values, each containing a deterministic encoding of every counter and histogram at that moment.

```mermaid
flowchart LR
    MR[ManualReader] -- Collect --> Snap[Snapshotter<br/>ring of Samples]
    Snap -- WindowEnds(window) --> H1[/api/v1/dashboard<br/>summary handler]
    Snap -- Samples() --> H2[/api/v1/timeseries<br/>time-bucketed handler]
    H1 --> UI[Admin SPA<br/>dashboard cards + charts]
    H2 --> UI
```

The dashboard handlers subtract two `Sample`s to derive windowed totals, rates, and histogram quantiles — counters are cumulative-since-process-start, so `end.value - start.value` over `end.At - start.At` gives the window's rate. Quantiles are interpolated from cumulative bucket counts using the bounds the sample captured (no re-bucketing, no per-window aggregation drift).

| Knob | Default | Override | Notes |
|---|---|---|---|
| `Interval` | 5 minutes | `SLUICE_ADMIN_SNAPSHOT_INTERVAL_MS` (production) | Matches the SPA's 288-point 24h chart cadence (288 × 5 min = 24 h). The Setup-level `Config.SnapshotInterval` is plumbed from `ServerEnv.AdminSnapshotIntervalMs`. E2E harnesses drop this to 200 ms so the dashboard reflects real traffic in test wall-clock. |
| `Capacity` | 290 samples | not exposed | One 5-min window per chart point in a 24h view (288), plus a small margin so a slow consumer doesn't lose the most recent point to a snapshot writer mid-collect. |
| `Now` | `time.Now` | test seam | Production should always leave nil. |

**Lifecycle.** `Setup` returns a constructed-but-not-started Snapshotter. The data plane's startup calls `Snapshotter.Start(ctx)` once with the server lifetime context; the immediate first call to `Snapshot` happens synchronously inside `Start` so consumers don't have to wait an entire interval before the dashboard renders anything. The collection loop runs in a `safego`-style goroutine with `recover()` — a panic during collect logs but does not tear the process down.

**Sample shape.** `Sample` is two maps:

- `Counters: map[metric] map[LabelKey] int64` — cumulative count per (metric, label set).
- `Histograms: map[metric] map[LabelKey] HistogramSnapshot` — cumulative `Sum`, `Count`, `Bounds`, `Counts` per (metric, label set). `Counts` has `len(Bounds)+1` entries; the final entry is the +Inf bucket.

`LabelKey` is a deterministic encoding (`"k1=v1,k2=v2"` with sorted keys) so map lookups across snapshots agree on equality — the underlying `attribute.Set` does not compare with `==`.

**Ring eviction.** When the ring is full the oldest sample is dropped and the rest shift left. Readers (`Samples()`, `Latest()`, `WindowEnds()`) grab a read lock and copy out the slice they need; no caller is blocked by a write.

**WindowEnds.** The window selector for the dashboard. Given a requested duration it returns the (start, end) `Sample` pair that covers at least that window, the realised duration between them (shorter than requested if the ring isn't full yet — useful for "since startup" framing), and `ok=false` if the ring has fewer than two samples.

---

## Health endpoint

The data-plane listener mounts `GET /healthz` ahead of the main handler chain ([`internal/server/server.go`](../internal/server/server.go) + [`internal/server/healthz.go`](../internal/server/healthz.go)). It is not behind auth, routing, or rule evaluation — it's a plain `http.Handler` checked into the mux before the proxy stack ever sees the request. Kubernetes' readiness and liveness probes both point at it.

| Phase | Status | Body |
|---|---|---|
| Steady state | `200 OK` | `ok\n` |
| Drain (SIGTERM received) | `503 Service Unavailable` | `draining\n` |
| Non-GET method | `405 Method Not Allowed` (with `Allow: GET`) | empty |

The 200→503 flip is the contract the kubelet's eviction loop reads. The shutdown sequence in [`Server.Run`](../internal/server/server.go) is:

1. Root `ctx` cancels (signal received).
2. `healthz.MarkDraining()` flips the response mode atomically — every subsequent probe sees 503.
3. The kube proxy culls the pod from the Service's endpoint set within milliseconds; new traffic stops arriving.
4. `http.Server.Shutdown` begins draining in-flight requests with a detached context bounded by `SLUICE_SHUTDOWN_DRAIN_SECONDS` (default 300s).
5. If drain exceeds the budget, `Server.Close` hard-closes remaining connections.

The transition is one-way per process — there is no "undrain" path. A pod that has begun draining stays draining until it exits.

`Content-Type` is `text/plain; charset=utf-8` and the body is two bytes plus a newline (`ok\n`) or eight plus a newline (`draining\n`), so the probe's curl wire cost is negligible. If a request `ctx` is already cancelled when the handler runs (client disconnect, upstream timeout — rare on a probe path), the handler returns early without writing the body to avoid noisy "use of closed connection" log noise.

The admin listener has no health endpoint of its own — its lifecycle is bound to the same root context as the data plane, and `kubectl` style introspection against the admin port is rarely useful. Probe the data plane; the admin port's readiness is implied.

---

## Structured logs

The gateway emits one logger handler per process: `log/slog` with a JSON handler bound to stdout. Format and level come from `gateway.log.format` / `gateway.log.level` (validated upstream in `ServerEnv`); the supported formats are `json` (default) and `text` (only useful for local dev tailing).

The root logger is enriched at startup with the service header. Every record carries:

| Field | Set by | When |
|---|---|---|
| `service` | `EnrichLogger` at startup | Always. The binary name — `gateway` for `cmd/gateway`. |
| `version` | `EnrichLogger` at startup | Always. From `internal/version.Version`. |

The per-request logger is derived from the root via `WithLogger(ctx, l)` and stashed on `context.Context`. Downstream middleware fetches it via `observability.FromContext(ctx)`. Field constants are exported from `internal/observability/logging.go` (`LogFieldCorrelationID`, `LogFieldProvider`, …) so middleware doesn't duplicate the strings.

Per-request enrichment, in the order it gets layered on:

| Field | Set by | When |
|---|---|---|
| `correlation_id` | auth middleware / routing middleware | As early as routing can resolve a request ID. Sourced from the inbound `X-Sluice-Correlation-Id` if present, otherwise a fresh UUIDv4 via `NewCorrelationID`. Also echoed back on the response as `X-Sluice-Correlation-Id`. |
| `api_key_id` | auth middleware | After managed-mode API-key resolution. Empty for passthrough-mode requests. |
| `configuration` | auth middleware | After the request's named configuration has been resolved. Bounded cardinality — operator-defined names. |
| `provider`, `endpoint`, `model` | the cmd/gateway final handler (forwarded path) / rules middleware (synthetic path) | At destination-finalisation time, after all rule mutations. Both writers call `WithRequestLabels(ctx, …)` and re-derive the per-request logger so reporters and downstream code see the post-rule values. |

The reporter emits one terminal log record at OnComplete:

```
INFO request completed
  status_code=200 duration_ms=1240 provider=openai endpoint=chat_completions
  model=gpt-4o-mini streaming=true ttfb_ms=180 upstream_error= policy_ref= attempts=0
```

Notable rules of the road:

- **Provider response bodies are never logged in full.** They flow into the connector spool when a configuration's `connector_bindings` allows; only metadata + correlation IDs hit logs. See CLAUDE.md's logging standards section.
- The recovery middleware logs `request handler panic recovered` with `path`, `method`, `panic`, `stack` before writing the 500. `safego.Go` logs goroutine panics with `site`, `panic`, `stack`.
- The spool's per-track goroutines log `spool: write record failed` (error) on a segment write failure and `spool: seal failed` (error) when a rotation can't complete. The uploader logs at error on `Complete` / `Deadletter` failures.

---

## Connector-captured records

When a configuration declares `connector_bindings`, the reporter at OnComplete builds one [`Record`](../contracts/connector/record.go) per inbound request and enqueues a value-copy onto every binding's spool track that the binding's sampling / filter / size-cap allows. See [connector-bindings.md](connector-bindings.md) for the evaluation order and [spool.md](spool.md) for the on-disk runtime.

The `Record` shape is the wire format every connector destination sees. Key fields:

- `correlation_id`, `provider`, `endpoint`, `model`, `configuration`, `api_key_name` — the post-rule resolved labels.
- `request`, `response` — method, path, headers, sha256, byte length, and either inline `body` or `body_omitted: true` (set when oversize behaviour stripped the body).
- `tokens` — provider-reported usage when the upstream returned one.
- `tags` — the set of tags `addTag` actions attached, in first-attach order, deduplicated.
- `rules_fired` — ordered list of rules that matched, including action types and termination flag.
- `policy_ref`, `attempts` — set only when the rules engine bound the request to a resilience policy via `useResiliencePolicy`. Single-shot requests omit both fields. See [docs/resilience.md → Observability](resilience.md#multi-attempt-record-shape) for the multi-attempt shape.

The `id`, `ts_ns`, `seq`, `instance_id` quartet stamps each record so consumers can sort by `(ts_ns, instance_id, seq)` and group by `correlation_id`. Across-instance ordering is by wall-clock; within one instance, `seq` is the per-process monotonic tiebreaker.

**Consumer sort key.** A consumer reading sealed segments must sort by `(ts_ns, instance_id, seq)` before assuming order; segments arrive from the spool's per-track drain in the same order their records enqueued, but **across** connectors there is no global ordering guarantee. The `seq` field is the per-instance disambiguator when `ts_ns` collides.

---

## Cross-references

- [docs/resilience.md](resilience.md) — resilience.* and cb.* meters in their orchestrator context; multi-attempt record shape.
- [docs/connectors.md](connectors.md) — destination types (s3, azure_blob, webhook) and per-type auth.
- [docs/connector-bindings.md](connector-bindings.md) — per-configuration sampling, filter, oversize behaviour.
- [docs/spool.md](spool.md) — disk-backed runtime between OnComplete and the destinations.
- [docs/admin-console.md](admin-console.md) — admin listener, dashboard handlers backed by the snapshotter, live-messages pane backed by the in-process ring.
- [docs/environment-variables.md](environment-variables.md) — full env reference, including `SLUICE_ADMIN_SNAPSHOT_INTERVAL_MS`, `SLUICE_ENV`, Prometheus / OTLP / log knobs.
- [docs/deployment.md](deployment.md) — K8s topology including the spool PVC mount and destination wiring.
