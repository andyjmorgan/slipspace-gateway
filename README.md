# sluice-gateway

A slim, observable AI provider gateway in Go. Intercepts API calls to OpenAI, Anthropic, and Google Gemini, applies per-tenant policy (auth, rules, resilience, telemetry), and forwards to the upstream provider after credential substitution.

Two coexisting auth modes:

- **Managed** — client uses a Sluice-issued key (`Authorization: Bearer sk_live_…`); gateway swaps in the upstream provider credentials before forwarding.
- **Passthrough** — client uses their own upstream token (e.g., Claude Code OAuth); gateway picks the policy via `X-Sluice-Configuration: <name>` and forwards the `Authorization` header verbatim.

## Protocol surface

The gateway is keyed by **protocol, not by provider**. The inbound path identifies the wire shape (the *protocol*); the upstream provider is chosen separately by the matched configuration's bindings (protocol + model) and can be overridden by a `changeProvider` rule. There is no `/<provider>/…` URL prefix — clients send the bare provider-native path and point their SDK at a single gateway base URL. Path → protocol mapping lives in [`internal/selection/protocol.go::ProtocolForPath`](internal/selection/protocol.go).

| Protocol | Inbound path(s) | Wire shape |
|---|---|---|
| `chat` | `/v1/chat/completions` *(also `/chat/completions`)* | OpenAI chat completions — also the OpenAI-compat surface for Anthropic and Gemini[^compat] |
| `responses` | `/v1/responses` *(also `/responses`)* | OpenAI Responses |
| `messages` | `/v1/messages` *(also `/messages`)* | Anthropic native Messages |
| `generate_content` | `/v1beta/models/{model}:generateContent` *(also `:streamGenerateContent`)* | Gemini native |
| `embeddings` | `/v1/embeddings` *(also `/embeddings`)* | Embeddings (model rides in the body; routed via catch-all bindings) |

Any path that does **not** match one of the generative protocols above (provider model-lists, Anthropic message batches, and other opaque surfaces) falls through to per-configuration **passthrough** matching, where a configuration's passthrough families claim the path by method + pattern and forward it verbatim. See [docs/routing.md](docs/routing.md) for the full selection algorithm.

[^compat]: Anthropic and Gemini both accept OpenAI-shaped `chat.completions` requests on the `chat` protocol but expect `Authorization: Bearer <key>` rather than their native `x-api-key` / `x-goog-api-key`. The gateway applies the right credential format automatically whenever `chat` traffic resolves onto one of those providers — whether through a binding or a model-keyed `changeProvider` rule. See [config-dev/policy.yaml](config-dev/policy.yaml) for a working redirect example.

## Quickstart

```sh
# Bring up the gateway + mockllm.
make dev

# Run the wire-compat suite (spawns its own stack against the mock).
make py-compat

# Run end-to-end against the live binary (Docker required for testcontainers).
make e2e

# Smoke tests against a deployed instance.
SLUICE_API_KEY=sk_live_... uv run --project test/smoke pytest -v
```

## Configuration

YAML lives in `SLUICE_CONFIG_DIR` (default `/etc/sluice/`). The loader merges **every** `*.yaml` file in the directory by top-level block key — filenames are a convention, not a constraint. The conventional split:

- **`providers.yaml`** — the `providers` block. One entry per upstream provider: base URL, which protocols it speaks, and per-protocol auth (plus optional passthrough families for opaque surfaces). Providers are connections, not credential holders — the per-configuration `credentials` supply the key.
- **`policy.yaml`** — `configurations`, `api_keys`, `rules`, `groups` (resilience targets, formerly `resilience_policies`), and `connectors`. Configurations carry the `bindings` that select a provider or group per (protocol, model). The admin write API edits the rules block live (`POST/PUT/DELETE /admin/api/v1/config/rules`) and persists changes atomically; every other block is YAML-only and applies on restart.
- **`admin.yaml`** *(optional, v1.1)* — gates the management console. Off by default. When enabled, the gateway starts a second listener on `bind_addr` serving the embedded SPA at `/` and the control-plane API under `/api/v1/*`. Username is hardcoded to `admin`; the password is read from `SLUICE_ADMIN_PASSWORD` if set, otherwise from the yaml `password` field.

End-of-request records can be shipped to external destinations (S3, Azure Blob, webhook) via the `connectors:` block in `policy.yaml`. Records are buffered on a disk spool (default `/var/lib/sluice/spool`) and uploaded out of band; see [docs/connectors.md](docs/connectors.md), [docs/connector-bindings.md](docs/connector-bindings.md), and [docs/spool.md](docs/spool.md).

An optional **central telemetry service** (`cmd/telemetry`) collects gen_ai OTLP spans/meters and HMAC-trusted Record webhooks from one or more gateways into Postgres and serves an operator console identical to the gateway's own dashboard. It is a separate binary with its own YAML config and two listeners (HTTP `:8686`, OTLP gRPC `:8687`). See [docs/telemetry-service.md](docs/telemetry-service.md), [docs/telemetry-service-api.md](docs/telemetry-service-api.md), and [docs/telemetry-webhook.md](docs/telemetry-webhook.md).

See [config-dev/](config-dev/) for working examples and [docs/](docs/) for the operator + developer reference suite. Server-level configuration (`SLUICE_*` env vars) is documented in [docs/environment-variables.md](docs/environment-variables.md).

## Management console (v1.1)

The console is a Vite + React + shadcn SPA embedded into the gateway binary via `//go:embed`. Source lives in [`web/`](web/); build output lands at `internal/admin/webdist/`.

```sh
# Build the SPA into the gateway's embed FS.
make web

# Or build everything (SPA + binary) in one go.
make build
```

### Local dev — full stack from docker-compose

The fastest way to exercise the SPA + gateway together end-to-end:

```sh
make dev-compose          # builds + starts gateway, mockllm
# open http://localhost:8081/admin and sign in:
#   username: admin
#   password: sluice-gateway   (override via SLUICE_ADMIN_PASSWORD env)
make dev-compose-down     # tear it down
```

The gateway image bakes the SPA in at build time. Override the operator password by exporting `SLUICE_ADMIN_PASSWORD=...` before `make dev-compose`. Ports exposed on the host:

| Host port | Container port | Surface |
|---|---|---|
| `8585` | `8585` | Data plane (provider proxy) |
| `8081` | `8081` | Admin console (SPA + `/api/v1`) |
| `9090` | `9090` | Prometheus scrape |

### SPA hot-reload against a running gateway

For SPA-only iteration without rebuilding the image, leave `make dev-compose` running and start the Vite dev server in a second terminal:

```sh
make web-dev   # Vite on :5180/admin, proxies /admin/api/v1 to localhost:8081
```

Open `http://localhost:5180`. Changes to `web/src/**` reload instantly; the compose-served gateway continues serving the API.

### Pure-Go dev loop (no compose for the gateway)

```sh
make dev   # docker compose up -d mockllm; go run ./cmd/gateway
```

`config-dev/admin.yaml` has the console enabled on `127.0.0.1:8081` with the placeholder password. To iterate on Go code without rebuilding an image, this is the fastest path.

## Where the canonical design lives

The full design — module layout, configuration schema, rule schema, resilience schema, telemetry, connector + spool architecture, .NET → Go translation, testing strategy — lives in a DonkeyWork project. The design notes are the long-form; [CLAUDE.md](CLAUDE.md) is the standing brief for any agent or human working in this repo.

## Repo layout

```
cmd/
  gateway/      data plane binary
  telemetry/    central telemetry service (OTLP + HMAC Record-webhook ingest, Postgres, console)
  cli/          key generation, config validation
  mockllm/      Go mock LLM for tests + local dev
internal/       compiler-enforced private
providers/      public — request/response/streaming models per provider
contracts/      public — control-plane schemas (rules, resilience, config, connector)
deploy/         dockerfile, helm chart
test/
  e2e/          black-box matrix against the real binary
  python/       wire-compat: official SDKs against spawned stack
  smoke/        live-deploy liveness checks
```

## License

See [LICENSE](LICENSE).
