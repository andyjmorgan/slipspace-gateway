"""Smoke test: OpenAI Responses API via sluice-gateway managed mode."""

from __future__ import annotations

import openai


def test_openai_responses(base_url: str, api_key: str) -> None:
    client = openai.OpenAI(
        base_url=f"{base_url}/v1",
        api_key=api_key,
        max_retries=0,
        timeout=30,
    )
    resp = client.responses.create(
        model="gpt-4o-mini",
        input="Reply with exactly one word: pong",
        max_output_tokens=32,
    )

    assert resp.id
    assert resp.model.startswith("gpt-4o-mini")
    # status is "completed" on success; "incomplete" is also acceptable for a
    # smoke check (e.g. when max_output_tokens cuts the reply short).
    assert resp.status in {"completed", "incomplete"}
    assert resp.output, "no output items returned"
