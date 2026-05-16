# Sluice Smoke Suite

Post-deploy liveness checks. Drives the official OpenAI, Anthropic, and Google
Gemini Python SDKs against a deployed sluice-gateway with a real managed-mode
API key — meaning the gateway resolves the key to a configuration and swaps in
real upstream provider credentials before forwarding.

Distinct from the wire-compat suite in `test/python/`:

| | `test/python/` (wire-compat) | `test/smoke/` (this dir) |
|---|---|---|
| Upstream | mockllm with canned responses | real OpenAI / Anthropic / Gemini |
| Gateway | spawned subprocess per session | already running at `SLUICE_BASE_URL` |
| Purpose | catch SDK round-trip regressions pre-merge | confirm a live deploy is healthy |
| Cost | free | a handful of cheap tokens per run |

## Running

```sh
cd test/smoke
SLUICE_API_KEY=sk_live_... uv run pytest -v
```

Or against a local stack:

```sh
SLUICE_BASE_URL=http://127.0.0.1:8080 SLUICE_API_KEY=sk_live_... uv run pytest -v
```

`SLUICE_BASE_URL` defaults to `https://sluice.donkeywork.dev`. Without
`SLUICE_API_KEY`, all tests skip cleanly — safe to wire into CI before
secrets are configured.

## What it covers

| Test | Endpoint |
|---|---|
| `test_openai_chat.py` | `POST /openai/v1/chat/completions` |
| `test_openai_responses.py` | `POST /openai/v1/responses` |
| `test_anthropic_messages.py` | `POST /anthropic/v1/messages` |
| `test_anthropic_chat.py` | `POST /anthropic/v1/chat/completions` (OpenAI-compat surface) |
| `test_gemini_generate.py` | `POST /gemini/v1beta/models/{model}:generateContent` |
| `test_gemini_chat.py` | `POST /gemini/v1beta/openai/chat/completions` (OpenAI-compat surface) |
