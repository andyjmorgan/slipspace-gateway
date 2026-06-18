<div align="center">

<img src="web/public/sluice.svg" alt="SlipSpace logo" width="104" height="104" />

# slipspace-gateway

**One endpoint in front of every LLM provider — with the policy, observability, and durability your platform team actually needs.**

</div>

SlipSpace is a slim, fast AI provider gateway in Go. Point your SDKs at a single base URL and it routes to OpenAI, Anthropic, and Google Gemini — swapping in upstream credentials, applying per-tenant policy (auth, rules, resilience), translating between provider dialects, emitting GenAI-grade telemetry, and spooling an auditable record of every request to durable storage. All without ever blocking the request path or mangling a payload it doesn't understand.

It speaks the providers' **native wire protocols** (plus their OpenAI-compatible surfaces), streams token-for-token, and forwards unknown fields **byte-for-byte** so it never falls behind a provider's API.

---

## Why SlipSpace

| | |
|---|---|
| **One base URL, every provider** | Protocol-keyed routing — clients send the bare provider-native path; bindings pick the upstream by `(protocol, model)`. No `/<provider>/` prefixes, no per-provider clients. Streaming and non-streaming, OpenAI · Anthropic · Gemini. |
| **High-fidelity passthrough** | Every model type carries `DynamicProperties`; every polymorphic block has an `Unknown*` fallback. Fields SlipSpace has never heard of round-trip to the upstream **intact**. The day a provider ships a new param, your callers get it — no gateway release required. |
| **GenAI telemetry, done right** | OpenTelemetry meters (Prometheus scrape *and* OTLP push) plus `gen_ai.*` spans following the OTel GenAI semconv — latency, model, provider. Optional, redacted prompt/response capture. |
| **Token & cost accounting** | Per-request prompt, completion, cache-**read**, and cache-**write** token counts on every span — the exact dimensions a spend dashboard needs — aggregated into per-provider / per-model / per-configuration panels. |
| **Session · agent · user attribution** | Resolve conversation, agent/sub-agent, and end-user identity from a configurable header chain (SlipSpace-native + Claude Code defaults), stamp it on every span, record, and log line, and drill into a full session timeline in the console. |
| **Tag and slice** | `addTag` rules label any request; the post-rule tag set rides on records and powers tag-fire panels and tag-filtered queries across the fleet. |
| **Durable, non-blocking audit spool** | End-of-request records buffer to a disk-backed `ndjson.zst` spool and ship out-of-band to S3, Azure Blob, or webhooks. The client **never** waits on backpressure — full ring or full disk drops on the floor and bumps a counter. |
| **Rich rules engine** | Match on provider, protocol, model, header, tag, or body field (with AND/OR groups); act with `changeProvider`, `changeModelName`, `setHeader`, `addTag`, `rewriteField`, `translate`, `returnStatusCode`, `useResiliencePolicy`, and more. Edit rules **live** through the admin API — no restart. |
| **Cross-provider translation** | Bidirectional **Anthropic Messages ↔ OpenAI Chat** — request, streaming + non-streaming response, tool calls, and errors — triggered by an explicit `translate` rule. Fail-closed on unsupported pairs, with a lossy-translation header and drop counter. |
| **Resilience built in** | Per-policy **failover** and **weighted load-balancing** across providers, with a **circuit breaker** that sheds load from a flapping upstream and a recorded attempt log on every request. |
| **Horizontally scalable** | Stateless data plane — run as many replicas as you like behind one Service. Per-pod spool PVCs, graceful drain (`/healthz` flips to 503 before shutdown so load balancers bleed off in flight), and clean multi-pod record semantics. |
| **DevOps-friendly** | A single multi-arch image (amd64 + arm64) with the admin SPA baked in. Config is a directory of trusted YAML; servers tune via `SLUICE_*` env vars. Turnkey Docker Compose stacks, a CLI for key-gen and config validation, and a `/metrics` endpoint out of the box. |
| **Admin console** | An embedded React SPA: live dashboard, config inspector, real-time message feed, and a visual rule editor — served from the binary, no separate deploy. |

---

## One endpoint, every provider

The gateway is keyed by **protocol, not provider**. The inbound path identifies the wire shape; the matched configuration's bindings pick the upstream by `(protocol, model)`, and a `changeProvider` rule can override it. Clients point a vanilla provider SDK at one SlipSpace base URL — no URL prefixes, no bespoke client.

| Protocol | Inbound path(s) | Wire shape |
|---|---|---|
| `chat` | `/v1/chat/completions` *(also `/chat/completions`)* | OpenAI chat completions — also the OpenAI-compat surface for Anthropic and Gemini[^compat] |
| `responses` | `/v1/responses` *(also `/responses`)* | OpenAI Responses |
| `messages` | `/v1/messages` *(also `/messages`)* | Anthropic native Messages |
| `generate_content` | `/v1beta/models/{model}:generateContent` *(also `:streamGenerateContent`)* | Gemini native |
| `embeddings` | `/v1/embeddings` *(also `/embeddings`)* | Embeddings (model rides in the body; routed via catch-all bindings) |

Anything that isn't a generative protocol (provider model-lists, Anthropic message batches, other opaque surfaces) falls through to per-configuration **passthrough** matching and is forwarded verbatim. See [docs/routing.md](docs/routing.md) for the full selection algorithm.

[^compat]: Anthropic and Gemini both accept OpenAI-shaped `chat.completions` requests on the `chat` protocol but expect `Authorization: Bearer <key>` rather than their native `x-api-key` / `x-goog-api-key`. SlipSpace applies the right credential format automatically whenever `chat` traffic resolves onto one of those providers. See [config-dev/policy.yaml](config-dev/policy.yaml) for a working redirect.

## Two auth models, one policy bundle

- **Managed** — the client uses a SlipSpace-issued key (`Authorization: Bearer sk_live_…`, or the provider-native `x-api-key` / `x-goog-api-key`); the gateway swaps in the real upstream credentials before forwarding. Your provider keys never leave the gateway.
- **Passthrough (BYOK)** — the client brings its own upstream token (e.g. Claude Code OAuth); the gateway selects policy via an `X-Sluice-Identity` header and forwards the client's `Authorization` verbatim.

Both resolve to a named **Configuration** — a reusable policy bundle of upstream credentials, bindings, rules, and resilience. Many keys can share one configuration. See [docs/auth.md](docs/auth.md).

## High fidelity: it never drops a field

Provider APIs move constantly, and a dropped field is a *silent* failure — you forward, the provider responds, the client gets something subtly wrong, and nothing logs. SlipSpace is built so that can't happen: every wire type embeds `DynamicProperties` and every polymorphic base has an `Unknown*` fallback, so any field the gateway doesn't model is preserved and forwarded unchanged in **both** directions. New provider params work the day they ship — no waiting on a SlipSpace release. This is a load-bearing invariant backed by golden round-trip tests, fuzzing on every `UnmarshalJSON`, and a reflection meta-test that fails the build if a new field lacks a JSON tag. See [docs/models.md](docs/models.md) and [docs/provider-models.md](docs/provider-models.md).

## Rules engine

Per-configuration rules evaluate in order and short-circuit on `behavior: exit`. Conditions match on `provider`, `protocol`, `modelName`, `header`, `tag`, or `bodyField`, nested freely in AND/OR `group`s. Actions transform the request or response:

- **Route** — `changeProvider`, `changeModelName`, `changeUrl`, `changeApiKey`
- **Shape** — `setHeader`, `appendQueryString`, `rewriteField` (set / remove / append), `addTag`
- **Translate** — `translate` between provider dialects (below)
- **Control** — `useResiliencePolicy`, `returnStatusCode`

Rules can be created, updated, and deleted **live** through the admin write API (`POST/PUT/DELETE /admin/api/v1/config/rules`): the change is validated, persisted to `policy.yaml`, and published atomically, so in-flight requests see either the old or new rule set — never a torn mix. See [docs/rules.md](docs/rules.md) and [docs/actions.md](docs/actions.md).

## Cross-provider translation

Send an Anthropic Messages request and have it served by OpenAI — or the reverse — without your client knowing. SlipSpace ships **bidirectional Anthropic Messages ↔ OpenAI Chat** translation covering the request, the non-streaming **and** streaming response, tool calls, and error responses, triggered by an explicit `translate` rule action. It's **fail-closed** on undeclared or unsupported pairs, surfaces a flag-gated `X-Sluice-Translation-Lossy` header, and counts any dropped fields. Proven by a Go differential matrix plus the official OpenAI **and** Anthropic Python SDK wire-compat suites. See [docs/actions.md → `translate`](docs/actions.md#translate).

## Resilience

Group multiple providers behind a resilience policy and SlipSpace will keep traffic flowing when one degrades:

- **Failover** — try targets in order until one succeeds.
- **Weighted load-balance** — distribute across targets (latency-biased or strict weights).
- **Circuit breaker** — trip a flapping upstream out of rotation and recover it automatically.

Every request carries a recorded attempt log. See [docs/resilience.md](docs/resilience.md).

## Observability + the Arbiter

Telemetry and reporting are kept as **separate channels** by design (a Grafana panel never reads audit records from S3):

- **Metrics & traces** — OpenTelemetry meters exposed on a Prometheus `/metrics` scrape endpoint *and/or* pushed over OTLP, plus `gen_ai.*` spans following the OTel GenAI semantic conventions. Every request carries its **token usage broken out four ways** — prompt, completion, cache-read, and cache-write — alongside latency (incl. time-to-first-chunk) and `rules_fired` / `tags_fired` counts, so cost and policy dashboards build straight off the meters. Prompt/response content capture is optional, redacted, and size-capped.
- **Audit records** — the full end-of-request envelope (bodies, headers, post-rule tags, fired-rule chain, resilience attempts) flows through the spool to your destinations.

An optional **Arbiter** (`cmd/arbiter`) ingests gen_ai OTLP spans/meters and HMAC-trusted Record webhooks from a whole fleet of gateways into Postgres and serves a unified operator console — keeping the two channels physically separate even as it converges them per request. See [docs/observability.md](docs/observability.md) and the dedicated section below.

## Arbiter

The **Arbiter** is the optional converged **security + telemetry** service (formerly the telemetry service) — a separate deployable, with its own Postgres/TimescaleDB datastore, that one or more gateways report into. The gateway data plane never depends on it: if the Arbiter is down the gateway keeps forwarding traffic, since OTLP export and Record push are best-effort and fire-and-forget. It only ever *consumes* telemetry and never sits on a request path.

It binds two listeners. Gateways export gen_ai OTLP spans and `slipspace.*` meters over **OTLP gRPC (`:8687`)**; the operator console, query API, and the HMAC-trusted Record webhook (`POST /api/v1/ingest/record`) live on **HTTP (`:8686`)** behind Basic auth. The fleet-wide console retains full history — the dashboard reads continuous aggregates over the meters, the message inspector lazily joins the verbatim Record blob per request.

On top of telemetry, the Arbiter runs **async, per-message security scanning** via pluggable **detectors** (PII / prompt-injection / toxicity) speaking the `slipspace.detect.v1` contract. Detectors return a score plus a raw label only; the Arbiter owns the **verdict**, reducing findings to one of **flagged / partial / clean** — `partial` (inconclusive) is first-class and never folds into `clean`, and scanning is fail-open by default. Operators tune it with scan-tag scoping (which traffic to scan), finding exclusion (suppress noisy categories), and severity classification (`info`/`warning`/`error`). The verdict and findings are emitted back out as **enriched OTel spans** carrying `slipspace.security.*` attributes.

The image is `ghcr.io/andyjmorgan/arbiter`. The quickstart bundle ships a gateway + Arbiter Compose stack ([`deploy/quickstart/`](deploy/quickstart/)); for the wire-level detail see [docs/arbiter.md](docs/arbiter.md) (service overview) and [docs/arbiter-api.md](docs/arbiter-api.md) (console query API).

## Plugging in a security detector

A **detector** is how you give the Arbiter a security signal — PII, prompt-injection, toxicity, or safety. It's a small, contract-speaking container the Arbiter calls per content unit; you wire it in by config and the Arbiter does the rest.

- **One contract, every family** — the `slipspace.detect.v1` IDL ([`proto/slipspace/detect/v1/detect.proto`](proto/slipspace/detect/v1/detect.proto)) is a single shape for all detectors: a `DetectRequest` carries one content `Unit` to scan, the detector replies with a `DetectResponse` of `Finding`s. A `Finding` is `category` + `score` + `raw_label` (+ optional span); the detector's `Family` is `PII` / `INJECTION` / `TOXICITY` / `SAFETY`. Variation between families lives in values, never structure.
- **Detectors stay dumb; the Arbiter owns the verdict** — a detector returns a score and its native label only, never a decision or risk level. The Arbiter reduces findings to a verdict (flagged / partial / clean) from policy. An inconclusive or truncated scan is first-class `partial`, never silently `clean`.
- **Transport is protojson over HTTP today** — async, `POST /detect` with a protojson `DetectRequest`. The gRPC `DetectionService` and the inline streaming early-termination RPC are declared in the proto to shape-lock the contract but are deferred (the inline path is not shipped).

The reference detectors live under [`deploy/detectors/`](deploy/detectors/): `detect_core.py` is the pure, unit-tested contract + chunk-planning logic, and `app.py` (sequence classification for injection / toxicity) and `pii_app.py` (Presidio + a PII model) are the model wiring behind a small FastAPI service. Oversized inputs are chunked with overlap rather than truncated, so the model window can't become a scan-evasion hole.

Today this ships as **per-model FastAPI detectors** — one container per model, configured by env (`DETECTOR_MODEL_ID`, `DETECTOR_FAMILY`, `DETECTOR_LABEL_MAP`, …), so injection and toxicity are the same image with different config. A generic **archetype-driver runtime** (drivers like hf-sequence-classification, hf-token-classification, gliner, presidio, http, with the model expressed as YAML) is the intended direction, not the current default. Either way the seam is the contract: anything that speaks `slipspace.detect.v1` plugs in. See [docs/arbiter.md](docs/arbiter.md) for the full picture.

## Session, agent & user attribution

Every request is correlated on three orthogonal identity axes on top of its `correlation_id`: **session** (the conversation), **agent** (the agent or sub-agent that issued it), and **user** (the end user it was made for). Each resolves from an authoritative `X-Sluice-Session-Id` / `X-Sluice-Agent-Id` / `X-Sluice-User-Id` header, falling back through a built-in chain (including Claude Code's `x-claude-code-session-id` / `X-Claude-Code-Agent-Id`) that operators extend with `SLUICE_*_ID_HEADERS` — no client code change. The resolved ids are echoed on the response, attached to every span, record, and log line, and let the console group a whole multi-turn **session into one timeline** and filter the fleet by agent or user. See [docs/observability.md → Session bundling](docs/observability.md#session-bundling).

## Durable, non-blocking spool

End-of-request records buffer to a disk-backed, zstd-compressed `ndjson.zst` spool and ship out of band to **S3**, **Azure Blob**, or **webhook** destinations, with per-binding sampling, filtering, and body-size caps. The hot path is sacred: `Enqueue` is non-blocking, and a full ring buffer or a full disk drops the record and increments a counter rather than ever making a client wait. See [docs/connectors.md](docs/connectors.md), [docs/connector-bindings.md](docs/connector-bindings.md), and [docs/spool.md](docs/spool.md).

## Built to a high bar

- **95% coverage** on internal + schema packages, CI-enforced.
- **E2E is the spec** — every feature has a black-box case proving it works through the real binary, with destination-side record assertions.
- **Wire-compat suite** — the official OpenAI / Anthropic / Gemini Python SDKs run against a spawned stack; any failure is a release blocker.
- **Fuzzed** — every `UnmarshalJSON`, the YAML loader, and route detection.
- **Slim dependency graph**, stdlib-first, `-race` everywhere, goroutine-leak checked.

## Mock LLM

`cmd/mockllm` is an in-repo Go mock upstream — a stand-in for OpenAI, Anthropic, and Gemini that returns **rule-driven canned responses** in each provider's wire shape (streaming and non-streaming). It replaces an earlier external C# mock; the published image is `ghcr.io/andyjmorgan/slipspace-mockllm` (built by `release.yaml`).

A canned response matches on `method` / `path` / `request_body_contains` and can stage realistic scenarios — multi-step pools (`max_responses`, e.g. "503 twice then 200"), pre-status and inter-chunk delays, and transport-level failures (`close`, `hang`) — which is what makes it a faithful target for resilience and streaming tests. Responses are loaded from a file (`--responses`) or staged per-session over its `/control/responses` endpoint; an empty pool returns a synthetic default.

It backs both loops:

- **Local dev** — `make dev` brings `mockllm` up via `docker-compose.yaml` (compose network alias `mockllm:5555`) and runs the gateway natively, so no real provider credentials are needed.
- **Tests** — `make e2e` spawns the gateway and `mockllm` for the black-box matrix, and `make py-compat` builds `mockllm` to run the official-SDK wire-compat suite against it.

See [docs/local-development.md → Mock LLM](docs/local-development.md#mock-llm).

---

## Quickstart

Turnkey stack from the **published images** (no build, real providers): see [`deploy/quickstart/`](deploy/quickstart/) — three copy-paste Compose stacks (gateway + console, gateway only, gateway + Arbiter). Set keys in `.env` and `docker compose up`.

```sh
cd deploy/quickstart
cp .env.example .env          # add your provider key(s)
docker compose -f compose.admin.yaml up -d --wait
curl http://localhost:8585/v1/chat/completions \
  -H "Authorization: Bearer sk_quickstart_demo_key" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"ping"}]}'
```

For local development against the mock LLM (build from source):

```sh
make dev          # gateway + mockllm
make py-compat    # official-SDK wire-compat suite against the mock
make e2e          # end-to-end against the real binary (Docker required)
```

## Configuration

Config lives in `SLUICE_CONFIG_DIR` (default `/etc/sluice/`). The loader merges **every** `*.yaml` in the directory by top-level block key — filenames are convention, not constraint. File contents are trusted (mounted from Secrets/filesystem); there is no `${VAR}` expansion inside YAML. The conventional split:

- **`providers.yaml`** — upstream connections: base URL, the protocols each speaks, per-protocol auth, and optional passthrough families. Providers hold no credentials.
- **`policy.yaml`** — `configurations` (with their `bindings`), `api_keys`, `rules`, resilience `groups`, and `connectors`.
- **`admin.yaml`** *(optional)* — gates the management console. Off by default.

See [config-dev/](config-dev/) for working examples, [docs/](docs/) for the full operator + developer reference, and [docs/environment-variables.md](docs/environment-variables.md) for every `SLUICE_*` knob.

## Management console

A Vite + React + shadcn SPA embedded into the gateway binary via `//go:embed` — dashboard, config inspector, live message feed, and a visual rule editor. Enable it in `admin.yaml`, then open `http://localhost:8081/admin` (username `admin`, password from `SLUICE_ADMIN_PASSWORD`). Build with `make web` (SPA into the embed FS) or `make build` (SPA + binary). Full local-dev loops — compose, SPA hot-reload, pure-Go — are in [docs/local-development.md](docs/local-development.md) and [docs/admin-console.md](docs/admin-console.md).

## Repo layout

```
cmd/
  gateway/      data plane binary
  arbiter/      Arbiter (OTLP + HMAC Record-webhook ingest, Postgres, console)
  cli/          key generation, config validation
  mockllm/      Go mock LLM for tests + local dev
internal/       compiler-enforced private engines
protocols/      public — on-the-wire models per provider protocol
models/         public — shared multimodal types + DynamicProperties
contracts/      public — control-plane schemas (rules, resilience, config, connector)
deploy/         dockerfiles, compose stacks, quickstart bundle
test/
  e2e/          black-box matrix against the real binary
  python/       wire-compat: official SDKs against a spawned stack
  smoke/        live-deploy liveness checks
```

## Design & contributing

The long-form design — module layout, configuration/rule/resilience schemas, the pipeline + middleware model, connector + spool architecture, testing strategy — lives in a DonkeyWork project; [CLAUDE.md](CLAUDE.md) is the standing brief for any agent or human in this repo and indexes those notes.

## License

See [LICENSE](LICENSE).
