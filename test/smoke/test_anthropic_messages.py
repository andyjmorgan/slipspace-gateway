"""Smoke test: Anthropic Messages API via slipspace-gateway managed mode.

Under v2 routing is model-keyed (no provider path prefixes): a claude-* model
on POST /v1/messages binds to the anthropic backend. Parametrized over both
inbound auth headers (v1.0.7):
  - bearer:    Authorization: Bearer sk_live_...   (SlipSpace's historical signal)
  - native:    x-api-key: sk_live_...              (vanilla Anthropic SDK default)
"""

from __future__ import annotations

import anthropic
import pytest


@pytest.mark.parametrize(
    "auth_header",
    ["bearer", "native-x-api-key"],
)
def test_anthropic_messages(
    base_url: str, api_key: str, auth_header: str
) -> None:
    # v1.0.7: vanilla anthropic SDK with just api_key= now works because
    # slipspace discovers the SlipSpace secret from `x-api-key`. Older clients
    # that injected `Authorization: Bearer` still work — covered as the
    # "bearer" parametrization.
    default_headers = (
        {"Authorization": f"Bearer {api_key}"} if auth_header == "bearer" else None
    )
    client = anthropic.Anthropic(
        base_url=base_url,
        api_key=api_key,
        default_headers=default_headers,
        max_retries=0,
        timeout=30,
    )
    resp = client.messages.create(
        model="claude-haiku-4-5",
        max_tokens=16,
        messages=[{"role": "user", "content": "Reply with exactly one word: pong"}],
    )

    assert resp.id
    assert resp.model.startswith("claude-haiku-4-5")
    assert resp.role == "assistant"
    assert resp.content, "no content blocks returned"
    assert resp.content[0].type == "text"
    assert resp.content[0].text.strip()
    assert resp.stop_reason in {"end_turn", "max_tokens", "stop_sequence"}
    assert resp.usage.input_tokens > 0
    assert resp.usage.output_tokens > 0
