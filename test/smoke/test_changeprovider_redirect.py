"""Smoke test: model-keyed changeProvider redirect.

Customer points the OpenAI SDK at the gateway's openai surface
(`{base_url}/v1`) and calls with a claude-* or gemini-*
model. A rule in policy.yaml fires on the model name and rewrites
state.Provider, transparently redirecting to anthropic's or
gemini's OpenAI-compat chat endpoint. The customer's SDK
doesn't know — it just gets back a parseable chat.completion
response.
"""

from __future__ import annotations

import openai
import pytest


def _openai_client(base_url: str, api_key: str) -> openai.OpenAI:
    return openai.OpenAI(
        base_url=f"{base_url}/v1",
        api_key=api_key,
        max_retries=0,
        timeout=30,
    )


def test_claude_model_routes_to_anthropic(base_url: str, api_key: str) -> None:
    client = _openai_client(base_url, api_key)
    resp = client.chat.completions.create(
        model="claude-haiku-4-5",
        messages=[{"role": "user", "content": "Reply with exactly one word: pong"}],
        max_tokens=8,
        temperature=0,
    )

    assert resp.id
    # Anthropic stamps its own model name on the response.
    assert resp.model.startswith("claude-haiku-4-5")
    assert resp.choices
    msg = resp.choices[0].message
    assert msg.role == "assistant"
    assert msg.content and msg.content.strip()


def test_gemini_model_routes_to_gemini(base_url: str, api_key: str) -> None:
    client = _openai_client(base_url, api_key)
    try:
        resp = client.chat.completions.create(
            model="gemini-2.0-flash-001",
            messages=[{"role": "user", "content": "Reply with exactly one word: pong"}],
            max_tokens=8,
            temperature=0,
        )
    except openai.APIStatusError as e:
        # Surface a clearer message for the common live-deploy failure mode.
        pytest.fail(f"gemini OpenAI-compat redirect failed: {e}")

    assert resp.id
    assert resp.choices
    msg = resp.choices[0].message
    assert msg.role == "assistant"
    assert msg.content and msg.content.strip()
