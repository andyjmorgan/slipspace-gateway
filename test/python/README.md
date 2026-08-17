# Python SDK Compatibility Suite

Drives the official **OpenAI**, **Anthropic**, and **Google Gemini** Python
SDKs against a running slipspace-gateway. Failures here are tagged
"wire-compatibility regression" and are a release blocker.

## Prerequisites

- Go toolchain (to build `gateway` + `mockllm` from source on demand)
- Python 3.10+
- Either `uv` (preferred) or stdlib `venv` + `pip`

## How the stack is wired

`conftest.py` starts a session-scoped subprocess stack:

| Component | How |
|---|---|
| `mockllm` | `subprocess.Popen` of `/tmp/slipspace-mockllm` (always rebuilt from `cmd/mockllm`) on a random port |
| `gateway` | `subprocess.Popen` of `/tmp/slipspace-gateway` (always rebuilt from `cmd/gateway`) with a materialized `config-dev/` snapshot |

The `mockllm`/`gateway` binaries are ALWAYS rebuilt via `go build` (incremental,
cache-backed) at fixture setup — not just "if missing". The previous "if exists,
skip" short-circuit was removed (#287) because a stale `/tmp` binary silently
passed forward wire-compat tests while 501'ing the reverse path. The output
paths can be overridden via `SLIPSPACE_GATEWAY_BIN` and `SLIPSPACE_MOCKLLM_BIN`
env vars.

Each test stages canned responses via the mockllm control API
(`POST/DELETE /control/responses`) and the suite clears them per-test via an
autouse fixture.

## Running locally

From the repo root:

```sh
make py-compat
```

Or, equivalently:

```sh
cd test/python
uv run pytest -v
```

If `uv` is not available, fall back to:

```sh
cd test/python
python3 -m venv .venv
source .venv/bin/activate
pip install -e .
pytest -v
```

## File layout

```
test/python/
├── pyproject.toml          ruff + pytest + SDK floor versions
├── conftest.py             stack lifecycle (mockllm + gateway subprocesses)
├── helpers.py              canned-response staging + API key constant
├── test_openai_sdk.py
├── test_openai_compat_chat.py
├── test_anthropic_sdk.py
├── test_gemini_sdk.py
├── test_session_id_header.py
├── test_translate_sdk.py
├── test_translate_reverse_sdk.py
└── README.md
```

## Why subprocess and not docker-compose

The mock is published as `ghcr.io/andyjmorgan/slipspace-mockllm` by
`.github/workflows/release.yaml`, but the compat suite deliberately spawns the
locally built Go binary instead: it must test the tree under change, not the
last released image. `conftest.py` always rebuilds `cmd/gateway` and
`cmd/mockllm` before the session (`test/python/conftest.py:88-103` — the
`_ensure_binary` helper; the `go build` subprocess call is at lines 98-102, invoked
from the two call sites at lines 118-119). The Go E2E
harness in `test/e2e/harness/` follows the same pattern.

## SDK version policy

SDK versions are pinned with `>=`, not `==`. CI catches drift naturally — a
new SDK release that breaks the wire contract will surface here within a
release cycle.

## Adding a new test

1. Stage a canned response via `helpers.stage_response(...)`
2. Build a client pointed at `f"{gateway_url}/<prefix>"`
3. Drive the SDK normally
4. Assert on the Pydantic / dataclass model the SDK parses the response into
