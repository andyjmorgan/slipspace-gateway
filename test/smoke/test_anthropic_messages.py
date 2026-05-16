"""Smoke test: Anthropic Messages API via sluice-gateway managed mode."""

from __future__ import annotations

import anthropic


def test_anthropic_messages(base_url: str, api_key: str) -> None:
    # The Anthropic SDK ships `x-api-key`, but sluice's managed-auth path keys
    # off `Authorization: Bearer ...`. Inject it via default_headers.
    client = anthropic.Anthropic(
        base_url=f"{base_url}/anthropic",
        api_key=api_key,
        default_headers={"Authorization": f"Bearer {api_key}"},
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
