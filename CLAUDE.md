# Sluice Gateway — Claude Project Instructions

This file is the standing brief for any AI assistant (or human) working in this repo. Read it before touching code.

## What this is

Sluice is a slim, observable AI provider gateway in Go. It intercepts API calls to OpenAI, Anthropic, and Google Gemini, applies per-tenant policy (auth, rules, resilience, telemetry), and forwards to the upstream provider after credential substitution.

Repo: `git@github.com:andyjmorgan/sluice-gateway.git`

Two coexisting auth modes:
- **Managed:** client uses a Sluice-issued API key (`Authorization: Bearer sk_live_...`); gateway swaps in the upstream provider credentials before forwarding.
- **Passthrough:** client uses their own upstream token (e.g., Claude Code OAuth); gateway picks the policy via `X-Sluice-Identity: <sk_live_... api-key secret>` (preferred) or the deprecated legacy `X-Sluice-Configuration: <name>` header, and forwards the `Authorization` header verbatim.

Both modes resolve to a named **Configuration** — a reusable policy bundle (upstream credentials, rules, resilience). Many API keys can share one configuration.

## Where the canonical design lives

**DonkeyWork project `522d9204-c3b6-4719-b0c9-8ef91b968314`** ("Sluice Gateway") holds the source of truth: 20+ design notes, 16 milestones, 120+ tasks with acceptance criteria.

Fetch via:
- `mcp__donkeywork__notes_list_by_project` for the design notes
- `mcp__donkeywork__milestones_list` then `mcp__donkeywork__tasks_list_by_milestone` for tasks

The notes you'll reference most often:

- **Module Layout** — directory tree, rationale
- **Configuration Schema** — full YAML shape for `gateway.yaml`, `providers.yaml`, `configurations.yaml`, `api_keys.yaml`
- **Pipeline + Middleware** — the typed-message channel pattern
- **Provider Models + DynamicProperties (load-bearing)** — unknown-field preservation, polymorphic content
- **Rule Schema** — conditions, actions, evaluator algorithm
- **Resilience Schema + Engine** — orchestrators, circuit breaker
- **Connector + Spool Architecture** — disk-backed ndjson.zst buffer between OnComplete and per-destination upload workers (s3 / azure_blob / webhook). Replaces NATS reporting from v1.1 onward.
- **Telemetry Strategy (OTel)** — meters, scrape vs push
- **Testing Strategy** — 95% + E2E first-class + Python SDK compat
- **Coding Standards** — modern Go conventions
- **Authentication & Auth Modes** — managed + passthrough resolution flow
- **.NET → Go Translation Table** — pattern mapping from `airia-ai-gateway`
- **Local Dev Setup + Mock LLM** — docker-compose, mock LLM rules
- **HTTP Forwarder Wrapper Design** — the thin `httputil.ReverseProxy` layer
- **OpenAI Compat Quirks** — anthropic/gemini OpenAI-compat surfaces, per-endpoint `auth_header` / `auth_format`, direct-route vs rule-redirect pattern (v1.0.2)

When in doubt, check the notes — they're the long-form. This file is the index.

## Current state

Shipped through **v1.1.18**: data plane forwarding for all three providers (streaming + non-streaming), rules engine, resilience orchestrator (failover + load_balance, circuit breaker), connector spool (s3 / azure_blob / webhook), admin console (dashboard + config inspector + live messages), and the rules read-write API + visual editor. OpenAI-compat chat surfaces on Anthropic + Gemini.

**Cross-provider translation (v1.2):** shipped **bidirectionally** for **Anthropic Messages ↔ OpenAI Chat** — both `messages`→`chat` and `chat`→`messages` are registered translators (direct pairwise matrix, no hub), each covering request, non-streaming + streaming response, tool calls, and error responses — triggered by the explicit `translate` rule action (`internal/translate/`), fail-closed on undeclared/unsupported pairs, with a drop counter + flag-gated `X-Sluice-Translation-Lossy` header. Proven by a Go e2e differential matrix on both arms, the native Anthropic **and** OpenAI Python SDK wire-compat suites, and a property-coverage meta-test. See [docs/actions.md → `translate`](docs/actions.md#translate). Deferred post-MVP: Gemini translation, mixed-protocol resilience groups (cross-dialect failover), base-config auto-mapping.

**Queued:** v1.0.3 real `llmImpersonation` synthesisers; v1.2+ DLP guardrails, Bedrock, hot reload, RBAC.

The per-milestone changelog (acceptance criteria, what shipped when) lives in the DonkeyWork milestones — fetch via `mcp__donkeywork__milestones_list`. Don't track milestone history here.

## Load-bearing invariants (NEVER violate)

These are the rules that, if broken, silently break customers or destabilize the gateway. Treat them as hard.

1. **Unknown JSON fields round-trip back to the upstream provider intact.** Every provider model type embeds `DynamicProperties`. Polymorphic types have `UnknownX` fallback. Provider APIs evolve constantly — if we drop a field, customer requests subtly break and we won't see it in logs.

2. **The connector spool never blocks the request path.** `Spool.Enqueue` is non-blocking; if the per-track ring is full, drop on the floor and bump the track's `droppedRing` counter. The drain goroutine writes to disk asynchronously; the upload goroutines ship segments out of band. The client must never wait on connector backpressure — see [docs/spool.md → Loss policy](docs/spool.md#loss-policy). Disk-full on the spool root is equally non-blocking: the write fails, the record is lost, the request continues.

3. **The C# mock LLM at `~/Source/Repos/airia-llmock/` is NEVER committed to this repo.** Not even path references. The committed `docker-compose.yaml` points at `ghcr.io/andyjmorgan/sluice-mockllm` — produced from `cmd/mockllm/` once the Go rewrite ships (v0.1 task).

4. **Reporting and telemetry are separate channels.** OTel meters carry counters/histograms/gauges (ops, scrape/push). The connector spool carries end-of-pipeline `Record` payloads (audit, billing, downstream replay) shipped via `Connector.Upload` to operator-configured destinations. The .NET predecessor conflates them; we do not. A Grafana panel must never read records from S3; the equivalent OTel meter exists for every dimension a dashboard cares about. The optional central **telemetry service** (`cmd/telemetry`) keeps the two channels physically separate even as it converges them for the operator console: gen_ai spans + sluice meters arrive over **OTLP gRPC on `:8687`**, while audit-grade `Record` payloads arrive over the **HMAC-trusted Record webhook on `:8686`** (`POST /api/v1/ingest/record`). The console's `request_events` rows are written by a **single feed** — the OTLP gen_ai span, which stores the complete span in `span_event` and projects the filter columns (including the gateway facts the span now carries as `sluice.*` attributes) from it. The Record webhook no longer writes the entity; it lands a verbatim `cc.Record` blob in the `record` table, joined lazily by `correlation_id` only when an operator opens the inspector. The dashboard reads TimescaleDB continuous aggregates over `metric_points`. None of these paths read S3/spool, so the console still complies with this invariant. See [docs/telemetry-service.md](docs/telemetry-service.md) for the topology and [docs/telemetry-service-api.md](docs/telemetry-service-api.md) for the query surface.

5. **YAML schema accepts rules + resilience in v1.0 even though evaluation is off.** Locks the shape so v1.1 flips evaluation on without YAML migration.

6. **Credential header format lives in one place per `(provider, protocol)`.** Managed-mode credential resolution flows: per-protocol (or passthrough-family) `auth` `{header, format}` on the provider's `protocols` map → per-provider default in `auth.UpstreamCredentialHeader`. The destination builder (`cmd/gateway/destination.go::resolveCredentialHeaders`, with `credentialHeaderFor` as the mint helper) is the only mint site. Bypassing it — minting in auth, in a rule, in middleware — fragments the table and causes silent credential mismatches. OpenAI-compat surfaces on Anthropic and Gemini depend on this: same provider, different credential conventions per protocol.

7. **Transport is re-resolved from post-rule state via `selection.ResolveTarget`, never from an in-destination override table.** After rules run, the handler calls `selection.ResolveTarget(state.Protocol, provider, …)` (`cmd/gateway/handler.go`) to resolve a `selection.Target` on the **post-rule** protocol + provider; that Target carries the upstream base URL, protocol path, auth convention, default query, and the configuration's credential. `buildDestination` (`cmd/gateway/destination.go`) consumes the pre-resolved Target as the single credential mint site — it does **not** read `state.Provider`, look up an endpoint map, or carry a `changeProvider`/`changeUrl`/`changeApiKey` override table. Provider/model switching survives only as internal selection primitives (`providerSwitchActions` → `changeProvider` + `changeModelName`) that the resilience orchestrator applies per attempt before that final re-resolve; they are no longer authorable in rules. This is what makes the model-keyed redirect pattern work (a request arriving on `/openai/v1/chat/completions` whose post-rule provider is anthropic lands on anthropic's `chat_completions` endpoint with anthropic's credential + auth header). Don't reintroduce a v1-style endpoint map or override table in the destination builder — keep transport resolution in `selection`, on post-rule state.

8. **Tests reading captured records MUST sort by `(ts_ns, instance_id, seq)`.** Per-track drain into a sealed segment is in-order, but across spool tracks and across instances there is no global ordering guarantee — the upload workers run independently and the destination sees records interleaved. Tests that assert order in connector output (sealed segments, destination contents) need the stable composite sort key — never receive order. The pre-v1.1.17 form of this rule used `MatchedAt` / `Started` on NATS events; the underlying lesson is the same.

9. **The live `ResolvedConfig` is only ever published through `config.Store`.** Consumers (router, auth resolver, rules evaluator, forwarder, reporter, admin handlers) hold a `*config.Store`, never a `*ResolvedConfig`. Reads go `store.Snapshot()` at request top and use that snapshot for the rest of the request — never re-snapshot mid-handler. Writers (admin write endpoints) `Clone` → mutate clone → `RevalidateAndIndex` → `WriteConfig` → `store.Replace` in that order, and only that order (`WritePolicyYAML` is now a back-compat alias for `WriteConfig`, which routes every editable block back to its `SourceFiles` origin). Skipping the clone, mutating the live snapshot in place, or persisting before validation breaks the atomicity contract — in-flight requests would see torn state. See `internal/config/store.go` for the model and `internal/admin/rules_write.go::commitClone` for the canonical write path.

## Engineering standards

Standard modern Go applies (current stable, `go.mod` pins the min; `gofmt`/`goimports`/`context`-first/`%w`-wrapped errors/`errors.Is`+`As`/no panic outside `init`/mutexes for state, channels for communication/every goroutine bound to ctx). The non-obvious, project-specific constraints are below; the rest is in the *Coding Standards* design note.

- **Lint config** (`.golangci.yml`) enables: `errcheck`, `govet`, `staticcheck`, `revive`, `gosec`, `ineffassign`, `unconvert`, `unparam`, `gocritic`, `bodyclose`, `contextcheck`. CI fails on dirty `gofmt`/`goimports` diffs.
- **Comments:** follow the global `~/.claude/CLAUDE.md` comment-hygiene rules. Go specifics: godoc (`//`, starting with the identifier name) on every exported symbol and on every field of load-bearing types (`Forwarder`, `Spool`, `Resolver`, `Router`, `Server`); document sentinel errors, `//nolint` directives (with reason inline), and interface assertions. Why-not-what. **Never** write `///` XML-doc comments — that's a .NET artifact.

### Logging

- `log/slog` (stdlib) JSON handler only — no Zap/Zerolog/Logrus.
- Enriched context fields: `service`, `version`, `correlation_id`, `api_key_id`, `configuration`, `provider`, `endpoint`, `model`. Per-request logger on `context.Context` via `logging.FromContext(ctx)`.
- **Provider response bodies are never logged in full** — they flow through the connector spool when bindings allow; only metadata + correlation IDs hit logs.

### Dependencies

Keep the dep graph small. Approved deps:

- `gopkg.in/yaml.v3` — YAML
- `github.com/google/uuid` — UUIDs
- `github.com/knadh/koanf/v2` — layered config (YAML + env)
- `go.opentelemetry.io/otel` + exporters — OTel
- `github.com/prometheus/client_golang` — Prometheus registry the OTel→Prom bridge requires (`otelprom` needs a `prometheus.Registerer`); also supplies the `Go` + `Process` collectors on `/metrics`
- `github.com/klauspost/compress/zstd` — segment compression in the connector spool (`internal/spool/segment.go`)
- `github.com/aws/aws-sdk-go-v2/...` — S3 connector + STS AssumeRole; pulled by `internal/connector/s3/`
- `github.com/Azure/azure-sdk-for-go/sdk/azblob` + `github.com/Azure/azure-sdk-for-go/sdk/azidentity` — Azure Blob connector; pulled by `internal/connector/azureblob/`
- `github.com/testcontainers/testcontainers-go` — tests only (SeaweedFS + Azurite + Postgres spin-ups for connector integration tests)
- `go.uber.org/goleak` — tests only

Conditionally approved (decide on first use):

- `github.com/go-chi/chi/v5` — routing. Not currently imported, and unlikely to be — stdlib `http.ServeMux` covers current needs. Drop candidate.

Anything else needs justification in the PR description.

### What we deliberately avoid

- **DI containers** — explicit constructor injection only. Construct a `Server` struct with all its deps at startup; per-request state lives on `context.Context`.
- **`testify`** — stdlib `testing` is enough.
- **`init()` for non-trivial work** — registration of polymorphic factories is the only acceptable use.
- **Global mutable state** — package-level vars must be `const` or read-only after init.
- **`interface{}` / `any` in public APIs** — strong typing or generic interfaces; `any` is a code smell unless at a serialization boundary.
- **Reflection in hot paths** — `DynamicProperties` is borderline; the marshaller is the only place.
- **Magic struct tags beyond `json` / `yaml`** — no `validate:`, no `mapstructure:`. Validation is explicit code.
- **`gomock` / `mockery`** — hand-rolled interface stubs are 10 lines and clearer than generated mocks.

### Web console (SPA) standards

Same lean-and-mean posture as the Go side, applied to `web/`. The console is two build targets (gateway admin, telemetry) over one `src/` tree.

Approved deps (`web/package.json` is the allowlist — anything new needs justification in the PR description):

- `react` / `react-dom` / `react-router` — the framework; `MemoryRouter` only in test harnesses
- `recharts` — charts
- `lucide-react` — icons
- `tailwindcss` (+ `@tailwindcss/vite`, `tw-animate-css`) — styling; design tokens (`--bg-*`, `--text-*`, `--accent`, `--ok/--warn/--err/--violet`) only, never hex literals in components
- `radix-ui` primitives + `clsx`/`tailwind-merge`/`class-variance-authority` — the shadcn-style `components/ui` base
- `next-themes`, `sonner` — theme + toasts
- vite / typescript / eslint toolchain (dev)

What we deliberately avoid:

- **State-management libraries** (redux, zustand, jotai) — component state + module-level caches (see `fullSpanCache`, `bodyCache`) cover current needs.
- **Data-fetching layers** (react-query, swr, axios) — `apiFetch` in `@/lib/api` is the one HTTP path; lazy/LRU semantics live next to their feature (`@/lib/span-view`).
- **CSS-in-JS / component-kit creep** — new visuals compose `components/atoms` and the shared card/tab idioms (`telemetry/components/span-inspector-panes.tsx`). One design per concept: if a card/tab/table exists, reuse it — never fork a near-duplicate.
- **Hand-edited wire types** — `web/src/lib/generated/` comes from `make generate` (tygo, CI-enforced freshness). Contract changes start in `contracts/`, never in TS.
- **Render impurity** — no `Date.now()` / `Math.random()` / ref reads during render; time bounds resolve at fetch time, reset-on-change derives from keyed state instead of setState-in-effect (the eslint react-hooks rules are the enforcement; the repo carries a small documented baseline of legacy hits — add zero new ones).

Verification bar for `web/` PRs: `npx tsc --noEmit`, eslint clean on touched files, `npm run build:telemetry` (and `npm run build` when the admin SPA is touched). Visual changes get fixture-harness screenshots (headless Brave against a vite dev server with `@/lib/api` aliased to a mock) — UI is not done until it's been looked at.

## Project structure

Flat layout: public packages at the repo root, private under `internal/` (compiler-enforced privacy). Full tree + rationale is in the *Module Layout* design note. The load-bearing split:

- `cmd/` — `gateway` (data plane), `telemetry` (central telemetry service), `cli`, `mockllm`. One binary per dir.
- `internal/` — engines: `proxy`, `pipeline`, `middleware/{auth,bodycapture,rules,resilience,guardrails,forwarder}`, `config`, `keys`, `spool`, `connector`, `observability`, `routing`, `server`.
- `protocols/` (PUBLIC) — on-the-wire models, one per protocol a provider speaks: `openai/{chat,responses,models}`, `anthropic/{messages,models}`, `gemini/{content,models}`.
- `models/` (PUBLIC) — shared multimodal types + `dynamic.go` (`DynamicProperties`).
- `contracts/` (PUBLIC) — control-plane schemas: `rules`, `resilience`, `config`, `connector`.
- `web/` SPA source, `deploy/{docker,helm}`, `test/{e2e,fixtures,python,smoke}`, `.github/workflows/`.

### Layout rules

- **Schemas public, engines private.** Provider model types, rule contracts, configuration types live at the root. The evaluators that *consume* those schemas stay in `internal/`.
- **`cmd/<name>` produces binary `<name>`.** Default Go behavior. Docker image tags carry the brand; binaries don't.
- **`internal/` is reversible pre-v1.0-tag.** Once we tag v1.0 and external consumers may import `protocols/`, `models/`, `contracts/`, the public/private boundary becomes SemVer-load-bearing.

## Configuration model

- YAML directory at `SLUICE_CONFIG_DIR` (default `/etc/sluice/`)
- Loader scans all `*.yaml`, merges by top-level key, errors on duplicate keys
- Top-level keys: `providers`, `groups`, `configurations`, `api_keys`, `rules`, `connectors`, `admin`, `telemetry`
- **File contents are trusted** (mounted from k8s Secrets or filesystem-permissioned). No `${VAR}` or `env:` syntax inside YAML. Only file paths are env-overridable.
- API keys are flat references to named Configurations. Configurations carry rules + resilience + default upstream credentials.
- Two auth modes (see *Authentication & Auth Modes* note) — passthrough wins if both `X-Sluice-Configuration` header and Sluice-issued bearer are present.
- **Config edits via the admin write API apply live** — rules, providers, groups, configurations, api-keys, and connectors all have CRUD endpoints under `/admin/api/v1/config/...` that clone the snapshot, validate, persist back to each block's source file (`config.WriteConfig` routes to the block's `SourceFiles` origin, not a fixed `policy.yaml`), then publish through `config.Store.Replace` — no restart. The data plane reads each request's snapshot atomically, so mid-flight requests see either the pre-swap or post-swap config, never a torn mix. Direct YAML file edits on disk and the `admin` / `telemetry` blocks still require a process restart; v1.2+ adds `fsnotify`-based automatic reload for that path.

## Testing requirements

Unit (internal correctness) and E2E (wire contract through the real binary) are both mandatory. **A feature is not done without an E2E case proving it works through the binary.** Full strategy is in the *Testing Strategy* note; per-layer detail in `test/e2e/README.md`.

### Hard targets

1. **95% coverage** on `internal/` and public schema packages, enforced by CI (`make coverage`; `cmd/*`, `test/*`, generated code excluded).
2. **Every feature has an E2E case** against the mock LLM with destination-side assertions.
3. **Wire-compat suite** (`test/python/`, official OpenAI/Anthropic/Gemini SDKs) — any failure is a **release blocker**.

### Layers

| Layer | Where | How |
|---|---|---|
| Unit | `*_test.go` next to code | stdlib `testing`, table-driven `t.Run` |
| Integration | `test/e2e/` (`e2e` tag — no separate `integration` tag exists) | testcontainers-go (SeaweedFS for S3, Azurite for Azure Blob, Postgres for telemetry) |
| E2E | `test/e2e/` (`e2e` tag) | spawn `gateway` + mockllm, per-test tmp spool, `httptest.Server` webhook receivers. `make e2e` |
| Wire compat | `test/python/` | pytest + official SDKs vs spawned stack. `make py-compat`. Release-blocking. |
| Smoke | `test/smoke/` | pytest + SDKs vs **live deploy** (`SLUICE_BASE_URL`, `SLUICE_API_KEY`). `make smoke`; `SLUICE_SMOKE_QWEN=true` for cluster qwen redirect tests. |

### Non-obvious gotchas

- **Never hardcode real model names as negative/no-match probes.** `claude-haiku-4-5` / `gemini-2.0-flash-001` break when the policy library grows a rule matching that prefix (happened twice in v1.0.2). Use synthetic names like `nomatch-internal` / `unmapped-model`.
- **Tests reading captured records sort by `(ts_ns, instance_id, seq)`, never receive order** — see invariant #8.
- Stdlib `testing` only — hand-rolled stubs, no `gomock`/`mockery`/`testify`. `-race` everywhere; `goleak` at teardown. Fuzz every `UnmarshalJSON` + the YAML loader + route detection (corpora in `testdata/fuzz/`). Real-over-mock (testcontainers MinIO beats a fake `S3Putter`).

## Protocol contracts — perpetual maintenance

The `protocols/` packages model the on-the-wire shapes of OpenAI, Anthropic, and Gemini — a moving spec the codebase must continuously chase. (A provider speaks one or more protocols; `protocols/` models each protocol's vendor-flavored wire shape.) Field drops are **silent**: we forward, the provider responds, the client gets *something* subtly wrong, with no error to log and no metric to spike. Contract-level testing is the only catch. **Treat every PR touching `protocols/` as a wire-compat change.**

- **`DynamicProperties` + `UnknownX` safety net is non-negotiable** — every model type embeds `DynamicProperties`; every polymorphic base has an `UnknownX` fallback. A new struct without these is a regression (invariant #1).
- **Test surface per model type:** golden round-trips are inline table-driven cases in each `protocols/*/*_test.go` (byte-equivalent modulo key order); `test/fixtures/` holds E2E fixtures, not the per-type golden round-trips; fuzz on every `UnmarshalJSON` ("if it parses, it round-trips", ≥10min in CI); unknown-discriminator + unknown-field tests via `UnknownX` / `DynamicProperties.Extra`; a `TestX_AllExportedFieldsHaveJSONTag` reflection meta-test enforcing `json` tags.
- **When touching `protocols/`:** cite the source for any new field (docs link / captured payload / SDK PR), add a fixture if the shape is new, add a fuzz seed for non-trivial value spaces, update the `Unknown*` fallback for new concrete types, run `make py-compat` locally before pushing.
- **Drift early-warning:** scheduled `fixture-refresh.yaml` runs the compat suite against the latest published SDKs and files a `provider-drift` issue on failure.

## E2E requirements

E2E tests are **the spec**, not a nice-to-have. The harness (`test/e2e/harness/`: `PostJSON`/`PostStream`/`Get`/`ExpectEvent`/`ExpectNoEvent`, plus an in-process httptest webhook capture server) and the full `(provider, endpoint) × variant × auth` matrix live in `test/e2e/README.md`. Every case asserts:

- HTTP status correct; body shape round-trips via the typed `providers` package.
- Captured connector record matches the **post-rule** labels + body envelope; **no** record when the configuration has no `connector_bindings` or sampling/filter excludes the request.
- `X-Sluice-Correlation-Id` set on response; `X-Sluice-Session-Id` echoed when sent.

## Local dev

- `make dev` brings up the mock LLM via docker-compose and runs the gateway natively; the spool root defaults to `/var/lib/sluice/spool` (`SLUICE_SPOOL_ROOT`) — `export SLUICE_SPOOL_ROOT=./tmp/spool` for a repo-local path. docker-compose persists the spool in the named volume `sluice-spool` mounted at `/var/lib/sluice/spool`
- The C# mock LLM at `~/Source/Repos/airia-llmock/` is used pre-v0.1; `cmd/mockllm/` (Go) replaces it
- `docker-compose.dev.yaml` is gitignored and overlays the local C# mock image until the Go rewrite ships
- For captured-record introspection during dev, `zstd -dc "$SLUICE_SPOOL_ROOT"/records/<connector>/sealed/*.ndjson.zst | jq .` shows the records on disk (default root `/var/lib/sluice/spool`)
- `make e2e` runs the e2e matrix against a spawned binary (Docker required for connector integration containers)
- `make py-compat` runs the wire-compat suite against a spawned stack
- `SLUICE_API_KEY=sk_live_... make smoke` runs the post-deploy harness against `sluice.donkeywork.dev` (or `SLUICE_BASE_URL=...`). Use this after every cluster roll. `SLUICE_SMOKE_QWEN=true` enables the cluster-side qwen redirect tests.
- See the *Local Dev Setup + Mock LLM* note for the full setup

## PR discipline

**Hard rule — run the full surface before every commit and push.** `make lint && make coverage && make e2e` minimum; add `make py-compat` for anything touching request/response shape, the proxy, response-writer wrappers, or streaming. **Don't push hoping CI catches it** — PR #24 (broke the proxy's `http.Flusher` assertion; passed `go test` without `-tags=e2e`) and PR #44 (gofmt/errcheck in a new test file) both would have been caught locally. If `golangci-lint` is missing, `brew install golangci-lint`; for phantom errors against deleted sibling-worktree paths, `golangci-lint cache clean`.

PR titles + descriptions follow the global `~/.claude/CLAUDE.md` rules (semantic prefix, why-first, paste the URL). No emojis in code/docs unless asked.

**Merge on green means *green*, not *queued*.** This repo enforces no required status checks, so `gh pr merge --auto` merges **immediately** (it has no checks to wait on) — it is NOT "merge when CI passes". When asked to merge on green: poll `gh pr checks <n>` until **every** check has passed, then merge with `gh pr merge <n> --squash`. Never merge — or enable auto-merge — while any check is pending. (Caught on PR #241, where `--auto` landed the merge before CI even started.)

**Always ensure every action completes — never fire-and-forget.** After any state-changing action (push, merge, deploy, cluster apply, background job), confirm it actually finished and succeeded before reporting done or moving on: read the exit status, poll the resource to its terminal state, re-fetch and verify. A backgrounded command's `&& echo OK || echo FAIL` masks the real exit code — check the underlying result, not the wrapper.

**Flakes:** a flake is a test that fails then passes with no code change. On spotting one, check for an existing `flake: <test>: <cause>` issue (`gh issue list --state open --search 'flake: <test>'`); comment with the new run link if it exists, else open one (`--label flake`). Confirm with `go test -count=10 -race -run <Name> ./<pkg>` — if it reproduces every time it's a `bug:`, not a flake. Fixing it is a follow-up PR; the issue stays open until that merges.

**Stacked PRs:** branch each off its parent, not main. On parent merge, rebase onto main + `git push --force-with-lease` — dependents often carry the same test fix as the parent, so resolve conflicts to the on-main version. Confirm `git branch --show-current` before every commit; recover a misplaced commit with `git checkout <intended> && git cherry-pick <sha> && git checkout <parent> && git reset --hard <prior>`.

## Working style for AI assistants

Follow the global working-style rules (drive don't present, one decision at a time, push back, verify before citing, keep it tight). Repo-specific: **if the user requests a feature without a corresponding design note, propose the note first and stub it in DonkeyWork** — the design notes are the spec.

## Quick links

- Repo: `git@github.com:andyjmorgan/sluice-gateway.git`
- DonkeyWork project: `522d9204-c3b6-4719-b0c9-8ef91b968314`
- .NET predecessor (read-only reference): `~/Source/Repos/airia-ai-gateway`
- Mock LLM (temporary, never commit): `~/Source/Repos/airia-llmock`
