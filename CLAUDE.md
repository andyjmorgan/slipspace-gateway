# Sluice Gateway — Claude Project Instructions

This file is the standing brief for any AI assistant (or human) working in this repo. Read it before touching code.

## What this is

Sluice is a slim, observable AI provider gateway in Go. It intercepts API calls to OpenAI, Anthropic, and Google Gemini, applies per-tenant policy (auth, rules, resilience, telemetry), and forwards to the upstream provider after credential substitution.

Repo: `git@github.com:andyjmorgan/sluice-gateway.git`

Two coexisting auth modes:
- **Managed:** client uses a Sluice-issued API key (`Authorization: Bearer sk_live_...`); gateway swaps in the upstream provider credentials before forwarding.
- **Passthrough:** client uses their own upstream token (e.g., Claude Code OAuth); gateway picks the policy via `X-Sluice-Configuration: <name>` and forwards the `Authorization` header verbatim.

Both modes resolve to a named **Configuration** — a reusable policy bundle (allowed endpoints, rules, resilience, upstream credentials). Many API keys can share one configuration.

## Where the canonical design lives

**DonkeyWork project `793a6cba-bd53-4b7e-8913-20fee7cb5f87`** ("Sluice Gateway") holds the source of truth: 14 design notes, 4 milestones, 35 tasks with acceptance criteria.

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
- **NATS Reporting (Envelope Pattern)** — large-object stash + 768 KB threshold
- **Telemetry Strategy (OTel)** — meters, scrape vs push
- **Testing Strategy** — 95% + E2E first-class + Python SDK compat
- **Coding Standards** — modern Go conventions
- **Authentication & Auth Modes** — managed + passthrough resolution flow
- **.NET → Go Translation Table** — pattern mapping from `airia-ai-gateway`
- **Local Dev Setup + Mock LLM** — docker-compose, mock LLM rules
- **HTTP Forwarder Wrapper Design** — the thin `httputil.ReverseProxy` layer

When in doubt, check the notes — they're the long-form. This file is the index.

## Phased scope

| Milestone | What ships |
|---|---|
| **v0.1 Foundation** | Repo scaffold, three `cmd/` binaries, Helm chart, mock LLM Go rewrite, CI with 95% coverage gate. No request forwarding. |
| **v1.0 Data Plane MVP** | Forwards OpenAI (chat/responses/models), Anthropic (messages/models), Gemini (generate_content/models). Streaming + non-streaming. NATS reporting. Rules + resilience schemas accepted in YAML but **not evaluated**. |
| **v1.1 Control Plane** | `api` REST binary + Web UI. CRUD over configurations & api keys. Rules + resilience evaluation **flip on**. |
| **v1.2+ Beyond** | Cross-provider translation, real DLP guardrails, AWS Bedrock, sibling repos (`sluice-a2a`, `sluice-mcp`), hot reload, RBAC. |

## Load-bearing invariants (NEVER violate)

These are the rules that, if broken, silently break customers or destabilize the gateway. Treat them as hard.

1. **Unknown JSON fields round-trip back to the upstream provider intact.** Every provider model type embeds `DynamicProperties`. Polymorphic types have `UnknownX` fallback. Provider APIs evolve constantly — if we drop a field, customer requests subtly break and we won't see it in logs.

2. **Reporting (NATS) never blocks the request path.** Publishes are non-blocking; if the queue is full or NATS is unreachable, drop and increment `gateway.events_dropped.total`. The client must never wait on the bus.

3. **The C# mock LLM at `~/Source/Repos/airia-llmock/` is NEVER committed to this repo.** Not even path references. The committed `docker-compose.yaml` points at `ghcr.io/andyjmorgan/sluice-mockllm` — produced from `cmd/mockllm/` once the Go rewrite ships (v0.1 task).

4. **Reporting and telemetry are separate channels.** OTel meters carry counters/histograms/gauges (ops, scrape/push). NATS carries end-of-pipeline event payloads (audit, billing, UI logs). The .NET predecessor conflates them; we do not.

5. **YAML schema accepts rules + resilience in v1.0 even though evaluation is off.** Locks the shape so v1.1 flips evaluation on without YAML migration.

## Engineering standards

### Go version

Current stable (1.23+). `go.mod` pins the minimum. Bump as new releases drop — we get language features (generics, range-over-func, structured iteration) and stdlib improvements (`log/slog`, improved `crypto/*`) for free.

### Style

- `gofmt` + `goimports` are non-negotiable; CI fails on dirty diffs
- `golangci-lint` config in `.golangci.yml` enables: `errcheck`, `govet`, `staticcheck`, `revive`, `gosec`, `ineffassign`, `unconvert`, `unparam`, `gocritic`, `bodyclose`, `contextcheck`

### Naming

- Packages: lowercase, no underscores, no plurals (`config`, not `configs`)
- Exported: `CamelCase`; unexported: `camelCase`
- Interfaces: verb or `-er` suffix when appropriate (`Forwarder`, `Publisher`); single-method interfaces named for the method (`Reader`, `Writer`)
- Acronyms keep case consistent: `HTTPClient`, not `HttpClient`; `apiKey`, not `aPIKey`
- Receiver names: short + consistent across all methods on the type

### Errors

- Wrap with `%w` to preserve the chain: `fmt.Errorf("publisher: enqueue id=%s: %w", id, err)`
- Sentinel errors exported as `Err*`: `var ErrConfigNotFound = errors.New("config: not found")`
- Use `errors.Is` and `errors.As` to inspect
- **No `panic` outside `init()` and truly unrecoverable scenarios.** Every public function returns an error if it can fail.
- **No naked error returns from public functions.** Always wrap with operation context.

### Context

- `context.Context` is the **first parameter** of every function that does I/O, can be cancelled, or carries request-scoped values
- Never store `context.Context` in a struct field
- Never pass `context.Background()` to a function that received a context
- Every long-running goroutine selects on `<-ctx.Done()`

### Comments

Comment density scales with how public-facing the symbol is. Comments are prose. Go has no structured-tag equivalent to `@param`/`<param>`/JSDoc; never invent one.

**Always comment:**
- Every exported type, function, method, field, and named constant. Godoc, starting with the identifier name. One sentence minimum on the synopsis line; add paragraphs separated by `//`-blank lines when behavior needs nuance.
- Every struct field — exported or unexported — on complex/load-bearing types (e.g., `Forwarder`, `Publisher`, `Resolver`, `Router`, `Server`). Field comment length scales with semantic complexity: trivial fields get a one-liner, fields carrying invariants or copy-on-write semantics get multiple lines.
- Sentinel errors (`var ErrX = errors.New(...)`) — describe when each fires.
- Compile-time interface assertions (`var _ Iface = (*Impl)(nil)`) — one line on why we want the assertion or what idiom we're enforcing.
- Tool directives (`//nolint:rulename`, build tags, `//go:generate`, etc.) — include the reason inline. `//nolint:contextcheck // shutdown ctx must outlive request ctx` not just `//nolint:contextcheck`.
- Package overview as `// Package X ...` on the `package` line of `doc.go` (or the primary file if there's no `doc.go`). A multi-line block describing what the package is for; longer for public packages, shorter for internal helpers.

**Don't comment:**
- Trivial unexported helpers whose name + signature already say everything.
- "What" the code does. The code already says what. Comments add **why**, constraints, gotchas, performance notes, invariants, or upstream-API quirks.
- Parameters individually. Describe subtle parameter semantics in the godoc prose ("ctx must be derived from the server context", "headers may be nil").
- Banner sections (`// ====== section ======`, `// --- region ---`). If a file is large enough to want banners, split it.

**Synopsis-line rules:**
- Comment is on the line directly above the declaration. No blank line between.
- Starts with the identifier name: `// Forward sends req upstream...`, not `// Sends a request...`.
- One sentence. Subsequent context goes in paragraphs below, separated by `//`-blank lines.
- For exported types, the synopsis should let a consumer decide whether to read further from godoc alone.

**Examples — get this right:**

```go
// Forward sends req upstream, applying the configured resilience policy.
// It blocks until either the upstream responds or ctx is cancelled.
// Streaming responses are written as chunks arrive.
func (f *Forwarder) Forward(ctx context.Context, req *Request) error {
```

```go
// Publisher publishes envelope messages to NATS. All Publish calls are
// non-blocking — if the worker queue is full or the broker is unreachable,
// the envelope is dropped and the drop counter is incremented.
type Publisher struct {
    // queue is the buffered channel between Publish() callers and worker
    // goroutines. Sized via Options.QueueSize; once full, Publish drops.
    queue chan Envelope

    // dropCnt counts events dropped on full queue or dispatch failure.
    // Exposed through Stats() for telemetry bridges.
    dropCnt atomic.Uint64

    workers  int
    threshold int
}
```

```go
// ErrPathCollision is returned when two providers claim the same fully-
// resolved route path. With prefix disambiguation, collisions only happen
// when two providers both have prefix_required=false sharing an
// accepted_path, or when two providers share both the same prefix and an
// accepted_path.
var ErrPathCollision = errors.New("config: route path claimed by multiple endpoints")
```

```go
// We drop on full rather than blocking — the request path must never
// stall on reporting backpressure.
select {
case p.queue <- env:
default:
    p.dropCnt.Add(1)
}
```

**Examples — get this wrong:**

```go
// bad — narrates what the code obviously does
// Forward forwards the request.
func (f *Forwarder) Forward(...) error {

// bad — line-by-line commentary
// Loop over each rule.
for _, rule := range rules {
    // Check if it matches.
    if rule.Condition.Matches(ctx) {
        // Apply the actions.
        for _, action := range rule.Actions { ... }
    }
}

// bad — XML doc / structured tags
/// <summary>Forward the request.</summary>
/// <param name="req">The request.</param>
```

**Don't write XML doc comments (`///`)** anywhere. That syntax is a .NET artifact. Go uses plain `//` godoc.

### Concurrency

- `context.Context` for cancellation — never ad-hoc `chan struct{}`
- `sync.Mutex` / `sync.RWMutex` for shared state; channels for **communication** between goroutines, not for protecting state
- Every `go func() { ... }()` has an obvious lifecycle bound to context cancellation, channel close, or `sync.WaitGroup`
- No `time.Sleep` in production paths
- Every goroutine wrapped with `recover()` that converts panic to a typed error

### Logging

- `log/slog` (stdlib) with a JSON handler — no Zap, no Zerolog, no Logrus
- Standard fields enriched on context: `service`, `version`, `correlation_id`, `api_key_id`, `configuration`, `provider`, `endpoint`, `model`
- Per-request logger lives on `context.Context`; retrieve via `logging.FromContext(ctx) *slog.Logger`
- **Provider response bodies are never logged in full** — they go to NATS reporting; only metadata + correlation IDs hit logs

### Dependencies

Keep the dep graph small. Approved deps for v1.0:

- `github.com/go-chi/chi/v5` — routing
- `github.com/nats-io/nats.go` — NATS client
- `github.com/vmihailenco/msgpack/v5` — envelope serialization
- `gopkg.in/yaml.v3` — YAML
- `github.com/google/uuid` — UUIDs
- `github.com/knadh/koanf/v2` — layered config (YAML + env)
- `go.opentelemetry.io/otel` + exporters — OTel
- `github.com/testcontainers/testcontainers-go` — tests only
- `go.uber.org/goleak` — tests only

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

## Project structure

Flat layout: public packages at the repo root, private under `internal/` (compiler-enforced privacy).

```
sluice-gateway/
  go.mod                            github.com/andyjmorgan/sluice-gateway
  go.sum
  README.md
  LICENSE
  CLAUDE.md                         this file
  Makefile
  .golangci.yml
  .gitignore
  docker-compose.yaml
  docker-compose.dev.yaml           gitignored — local mock LLM overlay

  cmd/
    gateway/                        data plane binary (v1.0)
    api/                            control plane REST (v1.1; stub returns 501 in v0.1)
    cli/                            key generation, config validation
    mockllm/                        Go replacement for the C# mock (v0.1 task)

  internal/                         private — compiler-enforced privacy
    proxy/                          thin httputil.ReverseProxy wrapper (~500 LOC)
    pipeline/                       Message types, channel-based middleware Chain
    middleware/
      auth/                         managed + passthrough resolution
      bodycapture/                  typed deserialization with DynamicProperties
      rules/                        v1.0 no-op evaluator; v1.1 turns on
      resilience/                   v1.0 single-attempt passthrough; v1.1 turns on
      guardrails/                   no-op middleware in v1.0
      forwarder/                    terminal — invokes proxy
    config/                         YAML loader, validation, merge
    keys/                           API key resolution
    bus/                            NATS publisher (data plane), subscriber (api)
    observability/                  slog + OTel wiring
    routing/                        path → (provider, endpoint) lookup
    server/                         http.Server + graceful shutdown

  providers/                        PUBLIC — request/response/streaming models
    openai/{chat,responses,models}
    anthropic/{messages,models}
    gemini/{content,models}

  models/                           PUBLIC — shared multimodal types
    message.go
    content/                        TextBlock, ImageBlock, ToolUseBlock, UnknownBlock
    dynamic.go                      DynamicProperties reflection helper

  contracts/                        PUBLIC — control-plane schemas
    rules/                          RuleContract, Condition, Action
    resilience/                     ResilienceConfig, CircuitBreakerConfig, Target
    config/                         GatewayConfig, Configuration, APIKey
    events/                         NATS event envelopes

  web/                              SPA source (v1.1)

  deploy/
    docker/Dockerfile               multi-stage scratch
    helm/sluice-gateway/            standalone chart, no parent

  test/
    e2e/
      harness/                      process lifecycle, NATS + mock LLM
      providers/                    per-provider × streaming × auth matrix
      streaming/
      reporting/
      shutdown/
    fixtures/                       captured real-provider JSON
    python/                         pytest with official SDKs

  .github/workflows/
    ci.yaml                         build, vet, lint, test, 95% coverage gate
    release.yaml                    tag → container image + helm chart
```

### Layout rules

- **Schemas public, engines private.** Provider model types, rule contracts, configuration types live at the root. The evaluators that *consume* those schemas stay in `internal/`.
- **`cmd/<name>` produces binary `<name>`.** Default Go behavior. Docker image tags carry the brand; binaries don't.
- **`internal/` is reversible pre-v1.0-tag.** Once we tag v1.0 and external consumers may import `providers/`, `models/`, `contracts/`, the public/private boundary becomes SemVer-load-bearing.

## Configuration model

- YAML directory at `SLUICE_CONFIG_DIR` (default `/etc/sluice/`)
- Loader scans all `*.yaml`, merges by top-level key, errors on duplicate keys
- Top-level keys: `gateway`, `providers`, `configurations`, `api_keys`
- **File contents are trusted** (mounted from k8s Secrets or filesystem-permissioned). No `${VAR}` or `env:` syntax inside YAML. Only file paths are env-overridable.
- API keys are flat references to named Configurations. Configurations carry rules + resilience + default upstream credentials.
- Two auth modes (see *Authentication & Auth Modes* note) — passthrough wins if both `X-Sluice-Configuration` header and Sluice-issued bearer are present.
- **No hot reload in v1.0/v1.1** — restart to apply config changes. v1.2+ adds `fsnotify`-based reload.

## Testing requirements

Two distinct concerns, both mandatory. Code without both is not done.

### Unit tests — verify functionality

Per-package, fast, focused. Unit tests verify the **internal correctness** of a function/type/middleware/orchestrator in isolation:

- Does this function return the right value for these inputs?
- Does this middleware mutate context correctly?
- Does this evaluator short-circuit on a terminating action?
- Does this state machine transition Closed → Open when threshold breached?

Live in `*_test.go` next to the code. Use stdlib `testing` with table-driven `t.Run` sub-tests. **95% coverage gate**, enforced in CI.

### E2E tests — verify the wire contract all the way through

Black-box, full binary, real-ish stack. E2E tests verify **end-to-end correctness** — what a real client sees when it talks to the gateway:

- Did the HTTP response shape match the upstream provider's?
- Did SSE stream chunks reach the client unbatched?
- Did the NATS side-channel event arrive with the right payload?
- Did graceful shutdown drain in-flight requests before exiting?

Live in `test/e2e/`. Build the `gateway` binary, spin up mock LLM + NATS via testcontainers, hit via real HTTP. **A feature is not done without an E2E case proving it works through the binary.**

### Hard targets

1. **95% unit test coverage** on `internal/` and the public schema packages, enforced by CI on every PR.
2. **Every feature has an E2E case** exercising the real binary against the mock LLM with NATS assertions.
3. **Wire-compat regression suite** in `test/python/` runs the official OpenAI, Anthropic, and Gemini SDKs against the gateway in CI. Any failure here is a release blocker.

### Layers

| Layer | Where | How |
|---|---|---|
| Unit | `*_test.go` next to code | stdlib `testing`, table-driven with `t.Run(name, func(t *testing.T) {...})` |
| Integration | `*_test.go` with `//go:build integration` tag | testcontainers-go for NATS, etc. |
| E2E | `test/e2e/` | spawn `gateway` binary via `exec.Command`, hit via real HTTP |
| Compatibility | `test/python/` | pytest with the official OpenAI, Anthropic, Gemini SDKs |

### Patterns

- **Table-driven tests** for parameterized cases — Go's idiom for what xUnit calls `[Theory]` + `[InlineData]`. Way more readable.
- **Hand-rolled interface stubs** for mocks. No `gomock` / `mockery` unless an interface grows large.
- **Real over mock when feasible.** A testcontainers NATS beats a fake `Publisher`.
- **`-race` everywhere.** `goleak` verifies no goroutine leaks at package teardown.
- **Fuzz tests** (`testing.F`) for JSON unmarshallers, YAML loader, route detection, envelope round-trip. Corpora committed under `testdata/fuzz/`.
- **Golden round-trip tests** for every provider model type using captured real-provider JSON in `test/fixtures/`.

### What we explicitly cover

- Provider model round-trips (unknown fields preserved byte-for-byte)
- Polymorphic dispatch (every concrete type + `UnknownX` fallback exercised)
- Pipeline composition (in-order execution, error propagation, context cancellation cleanup)
- NATS publisher (non-blocking drop semantics, large-payload stash, envelope round-trip)
- Config loader (every edge case + fuzz)
- Auth resolution (every code path of both modes)
- Route detection (every accepted_path pattern + fuzz)
- Streaming (SSE parsing, partial events, malformed chunks, client disconnect mid-stream)
- Graceful shutdown (drain timeout, in-flight completion, hard-kill on overrun)

### Coverage gate

`make coverage` parses `coverage.out`, computes per-package coverage, fails if any package under `internal/` or any public schema package is below 95%. Excluded: `cmd/*` (entrypoints are wiring), `test/*`, generated code.

## Provider contracts — perpetual maintenance, stringent discipline

The `providers/` packages model the on-the-wire shapes of OpenAI, Anthropic, and Gemini. **This is the part of the codebase that will require the most ongoing work** — the providers add fields, new event types, new content block shapes, and new endpoints on their own schedule, and we have to keep up or silently break customers.

Treat the provider contracts as a moving spec that the codebase must continuously chase. Two things follow:

### 1. The `DynamicProperties` + `UnknownX` safety net is non-negotiable

Every provider model type embeds `DynamicProperties` so unknown fields round-trip. Every polymorphic base type has an `UnknownX` fallback registered so unknown discriminators round-trip. **No exceptions.** A new struct without these is a regression.

### 2. The provider packages get the most test surface

Per provider model type:

- **Golden round-trip tests** — captured real-provider JSON in `test/fixtures/<provider>/`, parsed → re-marshalled → byte-equivalent (modulo key ordering, which we normalize). Add a fixture whenever you see a real-world payload our types haven't handled.
- **Fuzz tests** (`testing.F`) on every `UnmarshalJSON` — property: *"if it parses, it round-trips."* Fuzz survives ≥10 minutes in CI before merge.
- **Unknown-discriminator tests** per polymorphic base — synthesize a JSON payload with a `type` (or `role`) value we haven't modeled, confirm it round-trips via `UnknownX`.
- **Unknown-field tests** per concrete type — synthesize a JSON payload with a field we haven't modeled, confirm it survives via `DynamicProperties.Extra`.
- **Tagged-field discovery meta-test** — a `TestX_AllFieldsTagged` that uses reflection to enforce every exported field on a provider type has a `json` tag. Catches "forgot to tag a new field" before it ships.

### 3. Fixture freshness is a CI concern

A scheduled CI job (`fixture-refresh.yaml`) periodically:

- Runs the Python SDK compat suite against the latest published OpenAI, Anthropic, Gemini SDKs
- On failure, files an issue tagged `provider-drift`

This is the early-warning system. If wire compat regresses, we hear about it within hours, not from a customer.

### 4. PR discipline for provider changes

When touching `providers/`:

- Include the source for any new field (provider docs link, captured payload, SDK PR)
- Add a fixture if the shape is new
- Add a fuzz seed if the field has a non-trivial value space
- Update the `Unknown*` fallback if you added a new concrete type
- Run the Python compat suite locally before pushing

### Why this matters operationally

Provider field drops are silent: the gateway forwards a request, the provider returns a response, the client gets *something* — but if we stripped a field on the way out, the client's output is subtly wrong. There's no error to log, no metric to spike. The only way to catch it is contract-level testing.

**Treat every PR that touches `providers/` like a wire-compatibility change. Round-trip every fixture; run the Python suite; ask "what real-world payload haven't I seen yet?"**

## E2E requirements

E2E tests are **the spec**, not a nice-to-have.

### Harness

`test/e2e/harness/`:

```go
func New(t *testing.T) *Harness {
    h := &Harness{}
    h.NATS = startNATS(t)
    h.MockLLM = startMockLLM(t)
    h.Gateway = startGateway(t, gatewayConfig{
        ProvidersUpstream: h.MockLLM.URL,
        NATSURL:           h.NATS.URL,
    })
    t.Cleanup(h.Stop)
    return h
}
```

Test helpers:
- `PostJSON(t, path, body, headers)` — synchronous JSON
- `PostStream(t, path, body, headers)` — returns an SSE reader
- `ExpectEvent(t, subject, timeout)` — assert a NATS event arrives
- `ExpectNoEvent(t, subject, window)` — assert silence

### The v1.0 matrix

For each combination of:

- `(provider, endpoint)` ∈ {`openai.chat_completions`, `openai.responses`, `openai.models`, `anthropic.messages`, `anthropic.models`, `gemini.generate_content`, `gemini.models`}
- variant ∈ {`streaming`, `non-streaming`, `success`, `error_4xx`, `error_5xx`, `malformed_response`, `slow_response`, `client_disconnect_mid_stream`}
- auth ∈ {`managed_valid`, `managed_invalid`, `managed_disabled`, `passthrough_valid`, `passthrough_unknown_config`, `endpoint_not_allowed`}

Assert:
- HTTP response status correct
- Response body shape correct (round-trip via typed providers package)
- `gateway.request` NATS event published with correct payload
- `gateway.unmapped` event published when an unknown field is in the mock response
- No NATS event when `reporting.enabled: false`
- `X-Sluice-Correlation-Id` set on response
- `X-Sluice-Session-Id` echoed when sent

### Python SDK compatibility

`test/python/` exercises the official OpenAI, Anthropic, and Google Gemini SDKs against the gateway. Failures here are tagged "wire compatibility regression" — a different class of bug from internal unit-test failures, and a release blocker.

## Local dev

- `make dev` brings up gateway + mock LLM + NATS via docker-compose
- The C# mock LLM at `~/Source/Repos/airia-llmock/` is used pre-v0.1; `cmd/mockllm/` (Go) replaces it
- `docker-compose.dev.yaml` is gitignored and overlays the local C# mock image until the Go rewrite ships
- `nats sub "gateway.>"` watches live events during dev (install: `brew install nats-io/nats-tools/nats`)
- See the *Local Dev Setup + Mock LLM* note for the full setup

## PR discipline

- **Semantic PR titles** with a conventional commit prefix: `fix:`, `feat:`, `chore:`, `refactor:`, `docs:`, `test:`, `perf:`, `ci:`, `build:`. Lowercase after the prefix, imperative mood, under 70 characters.
- Lead the description with **why** — one line of motivation
- Then bullets: what changed and why, not a play-by-play of files touched
- Always paste the PR URL when you create one (draft or otherwise)
- No emojis anywhere in code or docs unless explicitly requested

## Working style for AI assistants

When making suggestions in this repo:

- **Drive, don't present.** Lead with a recommendation and the main tradeoff. Don't dump three options unless asked.
- **One decision at a time.** Don't ask multiple questions in parallel; sequence them.
- **Push back when something feels off.** The user doesn't want a yes-machine.
- **Verify before recommending.** If you cite a function or file, confirm it exists in the current tree.
- **Keep responses tight.** A one-sentence answer beats a paragraph.

If the user requests a feature without a corresponding design note, propose the note first and stub it in donkeywork. The design notes are the spec.

## Quick links

- Repo: `git@github.com:andyjmorgan/sluice-gateway.git`
- DonkeyWork project: `793a6cba-bd53-4b7e-8913-20fee7cb5f87`
- .NET predecessor (read-only reference): `~/Source/Repos/airia-ai-gateway`
- Mock LLM (temporary, never commit): `~/Source/Repos/airia-llmock`
