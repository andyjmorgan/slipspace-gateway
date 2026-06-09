# Local Development

This page is the developer's reference for running sluice-gateway against a local mock LLM, navigating the `make` target surface, and understanding how the five test layers (unit, integration, e2e, wire-compat, smoke) compose. Anything you need to know to take a freshly cloned checkout and either iterate on Go code, drive the SPA in dev mode, or reproduce a CI failure locally is on this page.

The shape of production is documented in [`deployment.md`](./deployment.md); this page is its peer for the inner loop.

---

## Table of contents

1. [What this doc covers](#what-this-doc-covers)
2. [Quickest path to a running gateway](#quickest-path-to-a-running-gateway)
3. [The dev compose topology](#the-dev-compose-topology)
4. [The `config-dev/` bundle](#the-config-dev-bundle)
5. [Mock LLM](#mock-llm)
6. [Make target reference](#make-target-reference)
7. [Testing layers](#testing-layers)
8. [Developer workflow: before every commit](#developer-workflow-before-every-commit)
9. [Inspecting captured connector segments](#inspecting-captured-connector-segments)
10. [Common pitfalls](#common-pitfalls)
11. [Cross-references](#cross-references)

---

## What this doc covers

Three concerns:

1. **How to run sluice-gateway locally** — a native `go run` against a containerised mock LLM, or the full production-shaped image via docker-compose.
2. **What the test layers are and when to invoke each** — unit, integration, e2e, wire-compat, smoke. They are not interchangeable; each catches a different class of regression.
3. **The `make` target surface** — one canonical command per workflow, all the env wiring baked into the Makefile so nobody has to remember which `SLUICE_*` vars need to be set.

The committed `docker-compose.yaml`, `config-dev/`, and the in-repo `cmd/mockllm` binary together make a one-command bootstrap. No external services, no real provider credentials, no host-mounted secrets — every credential in `config-dev/policy.yaml` is a literal placeholder marked `*-mock` or `sk_dev_*`.

---

## Quickest path to a running gateway

From a fresh clone:

```sh
make dev
```

That single target brings up the mock LLM as a container, then runs the gateway natively via `go run ./cmd/gateway` so edits hot-reload on the next request through ctrl-C / re-run. The data plane binds on `:8585`, Prometheus scrape on `:9090`, admin console (off by default in native mode, enabled in compose mode) on `:8081`. The connector spool root defaults to `/var/lib/sluice/spool/` (`config.DefaultSpoolRoot`, `internal/config/env.go`) in **both** modes — the Makefile's `DEV_ENV` block does not override `SLUICE_SPOOL_ROOT`, so native `make dev` writes there too. To land segments under the working tree for easy inspection, set `SLUICE_SPOOL_ROOT=./tmp/spool` yourself when you run the gateway.

```mermaid
flowchart LR
    Dev[make dev] --> Compose[docker compose up -d<br/>mockllm]
    Dev --> GoRun[go run ./cmd/gateway<br/>SLUICE_CONFIG_DIR=./config-dev]
    Compose --> Mock[mockllm:5555<br/>compose network]
    GoRun --> Data[":8585<br/>data plane"]
    GoRun --> Prom[":9090<br/>prometheus"]
    GoRun --> Mock
    GoRun --> Spool["/var/lib/sluice/spool/<br/>connector segments<br/>(override via SLUICE_SPOOL_ROOT)"]
```

Send your first request:

```sh
curl http://127.0.0.1:8585/openai/v1/chat/completions \
  -H "Authorization: Bearer sk_dev_local_development_only_not_for_production" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}'
```

The mock LLM has no canned response staged by default; tests stage responses via its `/control/responses` endpoint (see [Mock LLM](#mock-llm)) — for ad-hoc curl, an empty pool returns a synthetic default.

To exercise the production-shaped image (SPA bundled, gateway + admin console behind one binary), use `make dev-compose` instead. Slower iteration (image rebuild on Go or SPA changes) but matches what ships.

---

## The dev compose topology

`docker-compose.yaml` is the committed baseline. Three services:

```mermaid
flowchart TB
    subgraph host[Host machine]
        Curl[curl / SDK / browser]
    end
    subgraph compose[docker compose network]
        GW[gateway<br/>image: sluice-gateway:dev<br/>:8585 :8081 :9090]
        Mock[mockllm<br/>image: sluice-mockllm:dev<br/>:5555 - internal]
        Spool[(sluice-spool<br/>named volume)]
    end
    Curl -- "8585 / 8081 / 9090" --> GW
    GW -- "http://mockllm:5555" --> Mock
    GW -- "/var/lib/sluice/spool" --> Spool
```

Two overlay files extend the baseline:

| Overlay | Committed? | Purpose |
|---|---|---|
| `docker-compose.dev.yaml` | gitignored | Per-developer mock-LLM override. Build a local `cmd/mockllm` from this repo, or substitute the legacy C# mock from a sibling workspace. Apply via `make dev-with-overlay`. The sample is checked in at `docker-compose.dev.yaml.example` — copy and edit. |
| `docker-compose.real.yaml` | committed | Real-provider overlay. Swaps the `config-dev` mount for the generated `config-dev.real/` tree (produced by `scripts/dev-real-config.sh` from `.env`), drops the mock LLM from `depends_on`, and adds the `host.docker.internal` alias so the gateway can reach a host-side kubectl port-forward for qwen-ollama. Apply via `make dev-real`. |

The base compose's gateway service mounts `./config-dev` read-only into `/etc/sluice` and overlays a compose-specific `admin.yaml` on top that binds the admin listener to `0.0.0.0` (the native-mode default is loopback). Override the admin password per developer with `SLUICE_ADMIN_PASSWORD` in your shell env.

---

## The `config-dev/` bundle

`config-dev/` is the local-dev configuration tree the gateway picks up via `SLUICE_CONFIG_DIR`. Three files, all loaded as a single merged tree:

| File | Purpose |
|---|---|
| `admin.yaml` | Admin-console listener config (`enabled`, `bind_addr`, dev-only `password`). |
| `providers.yaml` | Upstream provider connections — base URLs (all point at `http://mockllm:5555` in dev), per-protocol paths + auth, and `passthrough` families. No credentials. |
| `policy.yaml` | Configurations (`dev`, `production`) with their `credentials`, `bindings`, `passthrough_bindings`, `rule_names`, and `tags`; api keys; transform rules. |

The provider base URLs in `config-dev/providers.yaml` all point at `http://mockllm:5555` so every provider lookup resolves to the mock LLM. The e2e harness rewrites this string at materialisation time to the dynamically-assigned port of its spawned mockllm instance.

### `admin.yaml`

```yaml
admin:
  enabled: true
  bind_addr: "127.0.0.1:8081"
  password: "sluice-gateway"
```

`SLUICE_ADMIN_PASSWORD` wins over `password:` when both are set; the env var is the production pattern. Username is always `admin` (not configurable). See [`admin-console.md`](./admin-console.md) for the rest.

### `policy.yaml`

This file is **v2-shaped** (`contracts/config/model.go`): a configuration carries `credentials` (one key per provider), `bindings` (the router as data — `(protocol, models)` → `provider` or `group`), `passthrough_bindings` (path-pattern families), `rule_names`, and `tags`. The v1 `upstream_credentials` / `resilience_name` fields and the routing actions (`changeProvider` / `changeUrl` / `useResiliencePolicy`) are gone — routing is bindings, and rules are pure request/response transforms (tags, header sets, body rewrites). It loads two configurations (`dev`, `production`) plus their api keys and rules:

```yaml
configurations:
  dev:
    credentials:
      openai: sk-dev-mock
      anthropic: sk-ant-dev-mock
      gemini: dev-mock

    bindings:
      - { protocol: chat, models: ["gpt-*", "o1*", "o3*"], provider: openai }
      - { protocol: responses, models: ["gpt-*"], provider: openai }
      - { protocol: chat, models: ["claude-*"], provider: anthropic }
      - { protocol: messages, models: ["claude-*"], provider: anthropic }
      - { protocol: chat, models: ["gemini-*"], provider: gemini }
      - { protocol: generate_content, models: ["gemini-*"], provider: gemini }

    passthrough_bindings:
      - { family: messages_batches, provider: anthropic }
      - { family: models, provider: openai }
      - { family: models, provider: gemini }

    rule_names:
      - tag-openai-chat
      - redact-emails

    tags:
      tier: dev

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev"
    configuration: dev
    enabled: true

rules:
  # Transform-only: condition on the resolved provider + protocol, attach a tag.
  - name: tag-openai-chat
    condition:
      type: group
      logicalOperator: And
      children:
        - { type: provider, operator: Equals, expectedProvider: openai }
        - { type: protocol, operator: Equals, expectedProtocol: chat }
    actions:
      - { type: addTag, tag: surface:openai-chat }
    behavior: continue

  - name: redact-emails
    condition: { type: provider, operator: Equals, expectedProvider: openai }
    actions:
      - { type: setHeader, headerName: X-Sluice-Redacted, headerAction: Set, headerValue: emails }
    behavior: continue
```

For a failover / load-balance example, define a top-level `groups:` block and point a binding at it with `group:` instead of `provider:` — see [`resilience.md`](./resilience.md) and the [FAQ](./faq.md#how-do-i-send-one-model-to-several-providers-with-failover-or-load-balancing). The api-key secret `sk_dev_local_development_only_not_for_production` is the one the e2e harness hardcodes; treat it as a known-good fixture, not a credential. Same for `sk_dev_replica_*` and the literal `ollama` secret — every value in the committed `policy.yaml` is intentionally non-sensitive.

### `providers.yaml`

Six providers in dev: `openai`, `anthropic`, `gemini`, `gpt-oss`, `qwen36`, `qwen-ollama` — all pointing at the mock LLM in dev. In the v2 model each provider is a connection: a `base_url`, a `protocols` map (each protocol declaring its upstream `path` and per-protocol `auth` header + format), optional `required_headers` / `query`, and optional `passthrough` families for path-pattern surfaces (e.g. `messages_batches`, `models`). Providers carry **no** credentials — the configuration supplies the key per provider. See [`providers.md`](./providers.md) and [`configuration-model.md`](./configuration-model.md) for the full schema.

### What's *not* in `config-dev/`

No `gateway.yaml`. Server-level configuration (HTTP bind, spool root, log level, drain timeout) flows in through `SLUICE_*` env vars — the Makefile's `DEV_ENV` block is the canonical set for native runs. The compose service inlines the same set in its `environment:` block. See [`environment-variables.md`](./environment-variables.md) for the full list.

---

## Mock LLM

`cmd/mockllm` is a Go HTTP server that impersonates OpenAI, Anthropic, and Gemini. It replaces a legacy C# mock that used to live in a sibling workspace (`~/Source/Repos/airia-llmock/`) — the Go rewrite is the committed source of truth and the only mock that's published as a container image (`ghcr.io/andyjmorgan/sluice-mockllm`, though the dev compose still builds from source).

### Invocation

```sh
go run ./cmd/mockllm --port 5555 --responses test/fixtures/openai-chat.yaml
```

Two flags:

| Flag | Default | Notes |
|---|---|---|
| `--port` | `5555` | TCP port to listen on. Bind is `0.0.0.0`. |
| `--responses` | unset | Optional YAML or JSON file of canned responses to seed the global pool at startup. File extension picks the decoder (`.yaml` / `.yml` / `.json`). |

`LOG_LEVEL` env var (debug / info / warn / error) controls the slog level.

### Canned response shape

Each entry in the responses file is a `CannedResponse`:

```yaml
- method: POST
  path: /v1/chat/completions
  request_body_contains: "gpt-4o-mini"
  status: 200
  headers:
    Content-Type: application/json
  body: '{"id":"chatcmpl-1","object":"chat.completion","choices":[...]}'

- method: POST
  path: /v1/chat/completions
  streaming: true
  stream_chunks:
    - data: 'data: {"id":"1","choices":[{"delta":{"content":"hello"}}]}'
      delay_ms: 50
    - data: 'data: [DONE]'

# Resilience-testing primitives:
- method: POST
  path: /v1/chat/completions
  status: 503
  max_responses: 2          # pop after 2 matches
  delay_ms: 100             # pre-status-line delay

- method: POST
  path: /v1/chat/completions
  behavior: close           # hijack and close TCP without writing status
```

Empty fields wildcard (`method: ""` matches any method, `path: ""` matches any path). The first entry whose predicates all match wins; entries with `max_responses` set decrement on each match and pop at zero.

### Control plane

The mock LLM exposes `/control/responses` (POST to stage, DELETE to clear), `/control/state` (GET the current pool), and `/control/captured` (GET the snapshot of inbound requests). The e2e harness and the wire-compat suite both drive this surface — they stage responses per-test and clear them via an autouse fixture.

The mock LLM listens on the compose network alias `mockllm:5555` and is **not** published to the host by default — the `ports` mapping in `docker-compose.yaml` is commented out, so the gateway reaches it over the compose network but the host can't. Uncomment that mapping to curl this control surface (or any mock endpoint) on host `:5555`.

Sessions are scoped via the `X-Sluice-Session-Id` header: stage a response with `?session=<id>`, send a gateway request that echoes the same session header, and the mock matches per-session first then falls back to the global pool. Lets one mockllm process serve multiple independent scenarios concurrently — important for parallel tests in the same run.

---

## Make target reference

Every target in the `Makefile`, in the order they appear there:

| Target | Wraps | Env / inputs | Test layer | Notes |
|---|---|---|---|---|
| `all` | `lint vet test` | — | unit | The default. Matches what CI runs on every PR (minus e2e). |
| `build` | `go build ./...` | `NO_WEB=1` skips the SPA bundle dependency | — | `make build` rebuilds the SPA via `make web` first. Use `NO_WEB=1` to skip when you've already built it. |
| `web` | `npm run build` in `web/` | — | — | Vite build of the **admin console** SPA into `internal/admin/webdist/`. Clears generated files before invoking; preserves the committed `placeholder.html` and `.gitignore`. |
| `web-telemetry` | `npm run build:telemetry` in `web/` | — | — | Vite build of the **telemetry console** SPA into `internal/telemetry/server/webdist/`. Same emptyOutDir-off convention as `web`. Not declared in `.PHONY`. |
| `web-install` | `npm install --silent` in `web/` | — | — | Idempotent; `web`, `web-telemetry`, and `web-dev` depend on it. |
| `web-dev` | `npm run dev` in `web/` | — | — | Vite dev server with HMR. Proxies `/admin/api/v1` to the gateway admin listener on `:8081` (Vite dev server on `:5180`). Pair with a running `make dev-compose` for the full loop. |
| `vet` | `go vet ./...` | — | unit | Cheap; runs in `all` and in CI. |
| `fmt` | `go fmt ./...` + `goimports -local github.com/andyjmorgan/sluice-gateway` | — | — | Local convenience. CI fails on dirty diffs. |
| `lint` | `golangci-lint run ./...` | — | unit | Non-negotiable before commit. Install with `brew install golangci-lint` if missing. |
| `test` | `go test -race -coverprofile=coverage.out -covermode=atomic` | — | unit | Skips `web/node_modules`. Race detector on. |
| `coverage` | `test` + `scripts/coverage-gate.sh coverage.out 95` | — | unit + gate | Same as `test`, then fails if total coverage is under 95%. |
| `dev` | `docker compose up -d mockllm` + `go run ./cmd/gateway` | `SLUICE_CONFIG_DIR=./config-dev`, `SLUICE_HTTP_BIND=0.0.0.0:8585`, `SLUICE_PROMETHEUS_BIND=0.0.0.0:9090`, `SLUICE_LOG_LEVEL=debug` (the Makefile `DEV_ENV` block — note it does **not** set `SLUICE_SPOOL_ROOT`, so the spool falls back to `/var/lib/sluice/spool`) | — | The fast inner loop. Container infra + native gateway. |
| `dev-with-overlay` | `docker compose -f docker-compose.yaml -f docker-compose.dev.yaml up -d` + `go run ./cmd/gateway` | same as `dev` | — | Requires `docker-compose.dev.yaml` (copy from `.example`). |
| `dev-compose` | `docker compose up -d --build` | — | — | Builds the gateway image with the SPA embedded and brings up all three services. Slow iteration; matches production shape. Pair with `make web-dev` for SPA-only hot reload. |
| `dev-compose-down` | `docker compose down` | — | — | Tears down the compose stack. |
| `dev-real` | `scripts/dev-real-config.sh` + `docker compose -f docker-compose.yaml -f docker-compose.real.yaml --env-file .env up -d --no-deps --build gateway` | `.env` with `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY` | — | Real-upstream stack. Generates `config-dev.real/` from `.env` and reaches OpenAI / Anthropic / Gemini directly. For the qwen path you also need a `kubectl port-forward` on host port `11434`. |
| `dev-real-down` | `docker compose -f docker-compose.yaml -f docker-compose.real.yaml down` | — | — | Tears down the real-upstream stack. |
| `e2e` | `go test -tags=e2e -race -count=1 -timeout=5m ./test/e2e/...` | `TESTCONTAINERS_RYUK_DISABLED=true` | e2e | Spawns the gateway + mockllm binaries per test, plus a tmp spool dir per test. Webhook subsuites use a local `httptest.Server` as the receiver. Requires Docker. |
| `py-compat` | builds `/tmp/sluice-gateway` + `/tmp/sluice-mockllm`, then `uv run pytest -v` in `test/python/` | — | wire-compat | Runs the official OpenAI / Anthropic / Gemini SDKs against a spawned stack. Release-blocking. |
| `smoke` | `uv run pytest -v` in `test/smoke/` | `SLUICE_API_KEY=$KEY` (required), `SLUICE_BASE_URL` (optional, defaults to `https://sluice.donkeywork.dev`), `SLUICE_SMOKE_QWEN=true` (optional) | smoke | Post-deploy harness against a live gateway. Without `SLUICE_API_KEY` everything skips. |
| `clean` | `rm -f coverage.out coverage.html` | — | — | Drops coverage artefacts. |
| `tools` | `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest` | — | — | One-shot installer for the lint toolchain. |

Total: 21 `.PHONY` targets, plus `web-telemetry` (a real target that isn't declared phony).

---

## Testing layers

Five layers, each catching a different class of regression. A feature is not done until both unit and e2e cover it.

```mermaid
flowchart TB
    subgraph fast[Pre-commit, fast]
        Unit[Unit<br/>per-package, 95% gate<br/>make coverage]
        Integ[Integration<br/>//go:build integration<br/>testcontainers-bound]
    end
    subgraph slow[Pre-commit, slower]
        E2E[E2E<br/>tag: e2e<br/>spawned binary + mockllm<br/>make e2e]
        PyCompat[Wire-compat<br/>official OpenAI/Anthropic/Gemini SDKs<br/>make py-compat]
    end
    subgraph post[Post-deploy]
        Smoke[Smoke<br/>live gateway + real provider creds<br/>SLUICE_API_KEY=... make smoke]
    end
    Unit --> E2E
    Integ --> E2E
    E2E --> PyCompat
    PyCompat --> Smoke
```

### Unit — verify internal correctness

Per-package, fast, focused. `*_test.go` next to the code under test. Stdlib `testing` with table-driven `t.Run` sub-tests. Verifies the internal behaviour of a single function / type / middleware / orchestrator in isolation:

- Does this function return the right value for these inputs?
- Does this middleware mutate context correctly?
- Does this evaluator short-circuit on a terminating action?
- Does this state machine transition `Closed → Open` when the threshold is breached?

`make coverage` runs every `*_test.go` under the repo (excluding `web/node_modules`), produces `coverage.out`, then invokes `scripts/coverage-gate.sh` which fails if total coverage falls below 95%. The gate counts every package — including the public schema packages at the repo root — and refuses to merge a PR that regresses below the floor.

Race detector is always on (`-race`). Goroutine leak detection via `goleak` runs in tests that own background workers.

### Integration — verify with real dependencies

`*_test.go` with the `//go:build integration` build tag. testcontainers-bound — these tests bring up the dependencies the code under test needs (e.g. a MinIO container for the S3 connector), run the code against them, and assert against the dependency's state. Slower than unit (container startup overhead) but cheap relative to e2e (no spawned binaries).

Integration tests live alongside the package they test; they're separated from unit by the build tag, not by directory. The `internal/connector/s3/` MinIO-backed round-trip tests are a canonical example.

### E2E — verify the wire contract end-to-end

`test/e2e/`, build tag `e2e`. Each test:

1. Materialises a per-test spool directory in tmp
2. Spawns `cmd/mockllm` as a subprocess on a random port
3. Materialises a tmp copy of `config-dev/` with the mockllm port substituted in (and per-test `connectors:` overlays where the suite needs them)
4. Spawns `cmd/gateway` as a subprocess pointing at the tmp config and the per-test spool root
5. Drives real HTTP against the gateway, asserts on the response, captured spool records, and Prometheus scrape

The harness lives in `test/e2e/harness/`. Sub-suites cover providers (`providers/`), streaming (`streaming/`), rules (`rules/`), resilience (`resilience/`), the S3 and Azure connectors (`connector_s3/`, `connector_azure/`), record reporting (`reporting/`), telemetry (`telemetry/`), auth (`auth/`), correlation (`correlation/`), errors (`errors/`), admin (`admin/`), and shutdown (`shutdown/`); `types/` holds shared fixtures. Webhook receivers spin up a local `httptest.Server` per test; the `SLUICE_WEBHOOK_ALLOW_PRIVATE=true` env var is set on the spawned gateway so the runtime SSRF guard accepts the loopback target.

`make e2e` runs the whole suite with the race detector and a 5-minute timeout. `TESTCONTAINERS_RYUK_DISABLED=true` keeps the reaper container from interfering on developer machines. Docker is required.

**A feature is not done without an e2e case proving it works through the binary.**

### Wire-compat — verify the SDKs see what they expect

`test/python/`, driven by `make py-compat`. Uses the official OpenAI, Anthropic, and Google Gemini Python SDKs against a spawned gateway + mockllm stack. The SDKs do their normal Pydantic / dataclass parsing of the response — if the gateway has subtly broken the on-the-wire shape, the SDK errors and the test fails.

This is the wire-compatibility regression early-warning. Any failure here is a release blocker.

Build artefacts at `/tmp/sluice-gateway` and `/tmp/sluice-mockllm`. `conftest.py` in `test/python/` starts a session-scoped subprocess stack and stages canned responses via the mock LLM's control plane per-test. The stack reuses across tests in a single pytest invocation — `make py-compat` is a clean session each time.

`SLUICE_GATEWAY_BIN` and `SLUICE_MOCKLLM_BIN` override the built-binary paths if you want to point at a custom build.

### Smoke — verify a live deploy is healthy

`test/smoke/`, driven by `make smoke`. Distinct from wire-compat: this suite talks to a *live* gateway and a *real* provider. The gateway resolves the managed-mode key to a configuration and swaps in real OpenAI / Anthropic / Gemini credentials before forwarding.

```sh
SLUICE_API_KEY=$SLUICE_API_KEY make smoke
SLUICE_API_KEY=$SLUICE_API_KEY SLUICE_BASE_URL=http://127.0.0.1:8585 make smoke
SLUICE_API_KEY=$SLUICE_API_KEY SLUICE_SMOKE_QWEN=true make smoke
```

`SLUICE_BASE_URL` defaults to `https://sluice.donkeywork.dev`. Without `SLUICE_API_KEY`, every test skips cleanly — safe to wire into CI before secrets are configured. **Never echo the real key in chat, PR text, or commit messages — reference as `$SLUICE_API_KEY`.**

Coverage spans: OpenAI chat + responses, Anthropic messages + chat-compat, Gemini generate + chat-compat, model-keyed routing (the v2 binding selection that lands `claude-*` / `gemini-*` on their providers), and (opt-in via `SLUICE_SMOKE_QWEN=true`) the cluster-side qwen redirect tests.

---

## Developer workflow: before every commit

Hard rule:

```sh
make lint && make coverage && make e2e
```

`make py-compat` is also required when your change touches request / response shape, the proxy, response-writer wrappers, streaming, or anything the official SDKs interact with on the wire.

`make e2e` is roughly 30-60 seconds once testcontainers are warm — small price relative to the cost of pushing a regression. Don't push hoping CI catches it. The committed CI workflow runs the same set; the only reason to lean on it is when you've already passed locally.

If `golangci-lint` isn't installed (`command -v golangci-lint` returns nothing), install it once: `brew install golangci-lint` or `make tools`. If it reports phantom errors against deleted sibling-worktree paths, run `golangci-lint cache clean`.

---

## Inspecting captured connector segments

Connector segments are ndjson.zst files under `<spool-root>/records/<connector>/sealed/` (`internal/spool/spool.go::Spool` builds the per-connector tree under `Options.Root`). The spool root defaults to `/var/lib/sluice/spool/` in both modes; for `make dev-compose` it lives on the `sluice-spool` named volume. To inspect segments from a native `make dev` run, point the spool at the working tree first (`SLUICE_SPOOL_ROOT=./tmp/spool`), then zstd-decompress and pipe through `jq`. Each line is one [`Record`](../contracts/connector/record.go):

```sh
# native make dev, with SLUICE_SPOOL_ROOT=./tmp/spool exported for the gateway
zstd -dc ./tmp/spool/records/<connector>/sealed/*.ndjson.zst | jq .

# default spool root (native make dev without an override) — may need sudo to read
zstd -dc /var/lib/sluice/spool/records/<connector>/sealed/*.ndjson.zst | jq .

# from inside the compose container's volume
docker compose exec gateway sh -c 'zstd -dc /var/lib/sluice/spool/records/*/sealed/*.ndjson.zst' | jq .
```

If sealed segments aren't accumulating, the uploader is shipping them faster than rotation creates them — look at `deadletter/` instead. If `active/` has a file but `sealed/` is empty, the rotation thresholds haven't fired yet (default 64 MiB / 60 s; lower the connector's `rotation` block for tighter feedback). See [spool.md](spool.md) for the full lifecycle reference.

For the live tail of requests in real time, use the admin console's live-messages pane (`http://localhost:8081/admin`). That pane is in-process and lossy on restart, but renders without unzipping anything.

---

## Common pitfalls

**testcontainers port collision.** Occasionally `make e2e` fails with `bind: address already in use` when two parallel suites race on free-port assignment. Re-run the affected suite individually:

```sh
go test -tags=e2e -race -count=1 -timeout=5m ./test/e2e/resilience/...
```

Not worth tracking down — the assignment is best-effort and a retry almost always passes.

**Stale `golangci-lint` cache pointing at deleted sibling worktrees.** If `make lint` reports errors against paths that no longer exist (typically from a removed worktree), clear the cache:

```sh
golangci-lint cache clean
```

**Two mockllm sources.** `ghcr.io/andyjmorgan/sluice-mockllm` is the published image referenced by the committed `docker-compose.yaml`; `cmd/mockllm` is the local Go source. They're the same binary — the image is built from `cmd/mockllm` via `deploy/docker/Dockerfile.mockllm`. If you're iterating on the mock itself, use `make dev-with-overlay` and the `docker-compose.dev.yaml.example` overlay to build the image from your local tree. If you're just consuming the mock, the published image is the safe default.

**Hardcoding `claude-haiku-4-5` or `gemini-2.0-flash-001` as no-match probes.** Tests that pick a real model name to assert "no rule matches" break when the policy library grows a rule that matches that prefix (happened twice during v1.0.2). Use synthetic names like `nomatch-internal` or `unmapped-model` instead.

**Forgetting to sort captured records.** Tests that read records from a connector's spool segments or destination must sort by `(ts_ns, instance_id, seq)` before asserting order — segments enqueue in arrival order per-track but record-level ordering across spool tracks is not stable. See [observability.md → Connector-captured records](observability.md#connector-captured-records) and load-bearing invariant #8 in `CLAUDE.md`.

---

## Cross-references

- [`deployment.md`](./deployment.md) — production deployment topology, image, Helm chart, multi-pod considerations
- [`admin-console.md`](./admin-console.md) — admin listener, SPA, control-plane API, auth shape
- [`environment-variables.md`](./environment-variables.md) — every `SLUICE_*` env var the gateway reads
- [`configuration-model.md`](./configuration-model.md) — full YAML schema for providers, configurations, api keys, rules, resilience
