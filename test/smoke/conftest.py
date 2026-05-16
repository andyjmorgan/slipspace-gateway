"""Smoke-suite fixtures.

These tests drive the real OpenAI / Anthropic / Gemini Python SDKs against a
deployed sluice-gateway. They differ from the wire-compat suite in
`test/python/`:

- No mockllm, no canned responses — upstreams are the real providers.
- No subprocess spawn — the gateway is assumed to be running at SLUICE_BASE_URL.
- API key is provided via env; tests skip with a clear message if it's missing,
  so the suite is safe to run from CI without leaking credentials.

Environment:
    SLUICE_BASE_URL   default https://sluice.donkeywork.dev (no trailing slash)
    SLUICE_API_KEY    required — a managed-mode sk_live_... key
"""

from __future__ import annotations

import os

import pytest

DEFAULT_BASE_URL = "https://sluice.donkeywork.dev"


@pytest.fixture(scope="session")
def base_url() -> str:
    return os.environ.get("SLUICE_BASE_URL", DEFAULT_BASE_URL).rstrip("/")


@pytest.fixture(scope="session")
def api_key() -> str:
    key = os.environ.get("SLUICE_API_KEY")
    if not key:
        pytest.skip("SLUICE_API_KEY not set — skipping smoke suite")
    return key
