# SlipSpace Smoke Suite

Post-deploy liveness checks. Drives the official OpenAI, Anthropic, and Google
Gemini Python SDKs against a deployed slipspace-gateway with a real managed-mode
API key — meaning the gateway resolves the key to a configuration and swaps in
real upstream provider credentials before forwarding.

## TL;DR

```sh
SLIPSPACE_API_KEY=sk_live_... make smoke
```

Or, for the qwen redirect tests too (cluster-specific):

```sh
SLIPSPACE_API_KEY=sk_live_... SLIPSPACE_SMOKE_QWEN=true make smoke
```

Distinct from the wire-compat suite in `test/python/`:

| | `test/python/` (wire-compat) | `test/smoke/` (this dir) |
|---|---|---|
| Upstream | mockllm with canned responses | real OpenAI / Anthropic / Gemini |
| Gateway | spawned subprocess per session | already running at `SLIPSPACE_BASE_URL` |
| Purpose | catch SDK round-trip regressions pre-merge | confirm a live deploy is healthy |
| Cost | free | a handful of cheap tokens per run |

## Running

```sh
cd test/smoke
SLIPSPACE_API_KEY=sk_live_... uv run pytest -v
```

Or against a local stack:

```sh
SLIPSPACE_BASE_URL=http://127.0.0.1:8585 SLIPSPACE_API_KEY=sk_live_... uv run pytest -v
```

`SLIPSPACE_BASE_URL` defaults to `https://slipspace.donkeywork.dev`. Without
`SLIPSPACE_API_KEY`, all tests skip cleanly — safe to wire into CI before
secrets are configured.

## What it covers

Routing is model-keyed under v2 — on unprefixed routes the configuration's
bindings pick the backend from the model name. Route coverage is mixed:
`test_gemini_generate.py` is parametrized over both the prefixed
`/gemini/v1beta/models/{model}:generateContent` and the bare
`/v1beta/models/{model}:generateContent`, proving both are live, while the
OpenAI-compat cases (`test_anthropic_chat.py`, `test_gemini_chat.py`) point the
OpenAI SDK at the unprefixed `{base_url}/v1` and let the model-keyed bindings
land them on the anthropic / gemini OpenAI-compat surfaces.

| Test | Endpoint |
|---|---|
| `test_openai_chat.py` | `POST /v1/chat/completions` (gpt-*) |
| `test_openai_responses.py` | `POST /v1/responses` |
| `test_anthropic_messages.py` | `POST /v1/messages` (claude-*) |
| `test_anthropic_chat.py` | `POST /v1/chat/completions` with a claude-* model (OpenAI-compat surface on the anthropic backend) |
| `test_gemini_generate.py` | `POST /v1beta/models/{model}:generateContent` |
| `test_gemini_chat.py` | `POST /v1/chat/completions` with a gemini-* model (OpenAI-compat surface) |
| `test_changeprovider_redirect.py` | model-keyed binding redirect: claude-* / gemini-* models sent to the openai surface, routed by the configuration's model bindings |
| `test_qwen_redirect.py` | cluster-side qwen rules (opt-in via `SLIPSPACE_SMOKE_QWEN=true`) |
| `test_gptoss_translate.py` | model-keyed `translate`-action redirect onto the gpt-oss surface (opt-in via `SLIPSPACE_SMOKE_GPTOSS=true`) |

## Adding a new smoke

1. Create `test_<provider>_<surface>.py` next to the existing tests. Reuse the `base_url` and `api_key` fixtures from `conftest.py`.
2. Drive the official SDK with `base_url=f"{base_url}/v1"` (or bare `base_url` for the Anthropic/Gemini SDKs, which append their own version segment). Don't `requests.post` directly — using the SDK is what makes this a wire-compat check.
3. If the assertion depends on a rule or configuration that only exists in a specific deploy, gate it on an env var and skip cleanly when unset (see `test_qwen_redirect.py` for the pattern).
4. Update this table.
