# sluice-gateway

A slim, observable AI provider gateway in Go. Intercepts API calls to OpenAI, Anthropic, and Google Gemini, applies per-tenant policy (auth, rules, resilience, telemetry), and forwards to the upstream provider after credential substitution.

Two coexisting auth modes:

- **Managed** — client uses a Sluice-issued key (`Authorization: Bearer sk_live_…`); gateway swaps in the upstream provider credentials before forwarding.
- **Passthrough** — client uses their own upstream token (e.g., Claude Code OAuth); gateway picks the policy via `X-Sluice-Configuration: <name>` and forwards the `Authorization` header verbatim.

## Endpoint catalogue

The gateway exposes a per-provider URL surface. Every endpoint can be reached via the prefixed form (`/<provider>/…`), and the default provider (currently OpenAI) is also reachable bare.

| Provider | Endpoint | Inbound path | Upstream wire shape |
|---|---|---|---|
| `openai` | `chat_completions` | `/openai/v1/chat/completions` *(also `/v1/chat/completions`)* | OpenAI chat completions |
| `openai` | `responses` | `/openai/v1/responses` *(also `/v1/responses`)* | OpenAI responses |
| `openai` | `models` | `/openai/v1/models` *(also `/v1/models`)* | OpenAI models list |
| `anthropic` | `messages` | `/anthropic/v1/messages` | Anthropic native messages |
| `anthropic` | `chat_completions` | `/anthropic/v1/chat/completions` | Anthropic's OpenAI-compat chat surface[^compat] |
| `anthropic` | `models` | `/anthropic/v1/models` | Anthropic models list |
| `gemini` | `generate_content` | `/gemini/v1beta/models/{model}:generateContent` | Gemini native |
| `gemini` | `chat_completions` | `/gemini/v1beta/openai/chat/completions` | Gemini's OpenAI-compat chat surface[^compat] |
| `gemini` | `models` | `/gemini/v1beta/models` | Gemini models list |

[^compat]: Both providers accept OpenAI-shaped `chat.completions` requests but expect `Authorization: Bearer <key>` rather than their native `x-api-key` / `x-goog-api-key`. The gateway applies the right format automatically (per-endpoint `auth_header` / `auth_format` in `providers.yaml`). OpenAI-surface traffic can also be transparently redirected to either provider via a model-keyed `changeProvider` rule — see [config-dev/policy.yaml](config-dev/policy.yaml) for a working example. Pick by customer ergonomics: direct route gives a clean per-provider base URL; rule-based redirect keeps the OpenAI SDK pointed at a single base URL.

## Quickstart

```sh
# Bring up the gateway + mockllm + nats.
make dev

# Run the wire-compat suite (spawns its own stack against the mock).
make py-compat

# Run end-to-end against the live binary (Docker required for testcontainers).
make e2e

# Smoke tests against a deployed instance.
SLUICE_API_KEY=sk_live_... uv run --project test/smoke pytest -v
```

## Configuration

Three YAML files live in `SLUICE_CONFIG_DIR` (default `/etc/sluice/`):

- **`providers.yaml`** — operator-owned route table. One entry per provider; one entry per endpoint under each provider. Per-endpoint `auth_header` / `auth_format` overrides let a single provider expose multiple credential conventions.
- **`policy.yaml`** — configurations, API keys, rule library, resilience library. The control plane (v1.1) will write this exclusively.
- **`admin.yaml`** *(optional, v1.1)* — gates the management console. Off by default. When enabled, the gateway starts a second listener on `bind_addr` serving the embedded SPA at `/` and the control-plane API under `/api/v1/*`. Username is hardcoded to `admin`; the password is read from `SLUICE_ADMIN_PASSWORD` if set, otherwise from the yaml `password` field.

See [config-dev/](config-dev/) for working examples. Server-level configuration (`SLUICE_*` env vars) is documented in [CLAUDE.md](CLAUDE.md).

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
make dev-compose          # builds + starts gateway, mockllm, nats
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
| `4222` / `8222` | `4222` / `8222` | NATS / monitoring |

### SPA hot-reload against a running gateway

For SPA-only iteration without rebuilding the image, leave `make dev-compose` running and start the Vite dev server in a second terminal:

```sh
make web-dev   # Vite on :5180/admin, proxies /admin/api/v1 to localhost:8081
```

Open `http://localhost:5180`. Changes to `web/src/**` reload instantly; the compose-served gateway continues serving the API.

### Pure-Go dev loop (no compose for the gateway)

```sh
make dev   # docker compose up -d mockllm nats; go run ./cmd/gateway
```

`config-dev/admin.yaml` has the console enabled on `127.0.0.1:8081` with the placeholder password. To iterate on Go code without rebuilding an image, this is the fastest path.

## Where the canonical design lives

The full design — module layout, configuration schema, rule schema, resilience schema, telemetry, NATS reporting envelope, .NET → Go translation, testing strategy — lives in a DonkeyWork project. The 14 design notes are the long-form; [CLAUDE.md](CLAUDE.md) is the standing brief for any agent or human working in this repo.

## Repo layout

```
cmd/
  gateway/      data plane binary
  api/          control plane REST (v1.1, stub in v1.0)
  cli/          key generation, config validation
  mockllm/      Go mock LLM for tests + local dev
internal/       compiler-enforced private
providers/      public — request/response/streaming models per provider
contracts/      public — control-plane schemas (rules, resilience, config, events)
deploy/         dockerfile, helm chart
test/
  e2e/          black-box matrix against the real binary
  python/       wire-compat: official SDKs against spawned stack
  smoke/        live-deploy liveness checks
```

## License

See [LICENSE](LICENSE).
