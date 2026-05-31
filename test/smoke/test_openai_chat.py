"""Smoke test: OpenAI chat completions via sluice-gateway managed mode."""

from __future__ import annotations

import openai


def test_openai_chat_completion(base_url: str, api_key: str) -> None:
    client = openai.OpenAI(
        base_url=f"{base_url}/v1",
        api_key=api_key,
        max_retries=0,
        timeout=30,
    )
    resp = client.chat.completions.create(
        model="gpt-4o-mini",
        messages=[{"role": "user", "content": "Reply with exactly one word: pong"}],
        max_tokens=8,
        temperature=0,
    )

    assert resp.id
    assert resp.model.startswith("gpt-4o-mini")
    assert resp.choices, "no choices returned"
    msg = resp.choices[0].message
    assert msg.role == "assistant"
    assert msg.content and msg.content.strip()
    assert resp.usage and resp.usage.total_tokens > 0
