"""Smoke test: Gemini OpenAI-compat chat completions via slipspace-gateway.

v1.0.2 wires `/gemini/v1beta/openai/chat/completions` to Gemini's
OpenAI-shaped surface. Drive it with the OpenAI SDK pointed at
`{base_url}/v1`.
"""

from __future__ import annotations

import openai


def test_gemini_openai_compat_chat(base_url: str, api_key: str) -> None:
    client = openai.OpenAI(
        base_url=f"{base_url}/v1",
        api_key=api_key,
        max_retries=0,
        timeout=30,
    )
    resp = client.chat.completions.create(
        model="gemini-2.0-flash-001",
        messages=[{"role": "user", "content": "Reply with exactly one word: pong"}],
        max_tokens=8,
        temperature=0,
    )

    assert resp.id
    assert resp.choices, "no choices returned"
    msg = resp.choices[0].message
    assert msg.role == "assistant"
    assert msg.content and msg.content.strip()
    # Gemini's compat layer may not populate usage; only assert when present.
    if resp.usage is not None:
        assert resp.usage.total_tokens >= 0
