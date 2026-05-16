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

Two YAML files live in `SLUICE_CONFIG_DIR` (default `/etc/sluice/`):

- **`providers.yaml`** — operator-owned route table. One entry per provider; one entry per endpoint under each provider. Per-endpoint `auth_header` / `auth_format` overrides let a single provider expose multiple credential conventions.
- **`policy.yaml`** — configurations, API keys, rule library, resilience library. The control plane (v1.1) will write this exclusively.

See [config-dev/](config-dev/) for a working example. Server-level configuration (`SLUICE_*` env vars) is documented in [CLAUDE.md](CLAUDE.md).

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
