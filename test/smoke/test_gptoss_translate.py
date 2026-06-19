"""Smoke test: cross-provider translation against the live gpt-oss model.

gpt-oss (Ollama, gpt-oss:20b) serves chat, responses, and messages natively and
reasons + does tool calls — which makes it a self-validating oracle for
translation: the same model answers a request routed straight to its chat
surface AND a request translated from Anthropic Messages onto that chat surface,
so the translated path is checkable against the direct path.

Three fake models, resolved by the prod `production` config's `gpt-oss*` bindings
and rewritten by model-keyed rules (see config-dev/policy.yaml):

- `gpt-oss-chat`          → changeModelName(gpt-oss:20b)                    — direct, no translation (oracle baseline)
- `gpt-oss-from-messages` → changeModelName(gpt-oss:20b) + translate:chat     — Anthropic Messages translated to chat and back
- `gpt-oss-from-chat`     → changeModelName(gpt-oss:20b) + translate:messages — OpenAI Chat translated to messages and back (reverse arm)

These rules live on the cluster, so the test skips unless pointed at a deploy
known to carry them. Set SLIPSPACE_SMOKE_GPTOSS=true to enable.
"""

from __future__ import annotations

import json
import os

import anthropic
import openai
import pytest

REAL_MODEL = "gpt-oss:20b"


@pytest.fixture(scope="session")
def gptoss_enabled() -> bool:
    return os.environ.get("SLIPSPACE_SMOKE_GPTOSS", "").lower() in {"true", "1", "yes"}


def test_gptoss_chat_direct(base_url: str, api_key: str, gptoss_enabled: bool) -> None:
    """Oracle baseline: OpenAI Chat client → gpt-oss chat surface, no translation."""
    if not gptoss_enabled:
        pytest.skip("SLIPSPACE_SMOKE_GPTOSS not set — cluster gpt-oss rules disabled")

    client = openai.OpenAI(base_url=f"{base_url}/v1", api_key=api_key, max_retries=0, timeout=60)
    resp = client.chat.completions.create(
        model="gpt-oss-chat",
        messages=[{"role": "user", "content": "Reply with exactly one word: pong"}],
        max_tokens=64,
        temperature=0,
    )
    assert resp.model == REAL_MODEL, f"model={resp.model!r}, want {REAL_MODEL!r} (changeModelName fired)"
    assert resp.choices and resp.choices[0].message.content.strip()


def test_gptoss_translate_messages_text(base_url: str, api_key: str, gptoss_enabled: bool) -> None:
    """Anthropic Messages client → translate→chat → gpt-oss → translate back.

    The anthropic SDK parsing the response is the differential: a malformed
    translation would raise instead of returning a Message.
    """
    if not gptoss_enabled:
        pytest.skip("SLIPSPACE_SMOKE_GPTOSS not set — cluster gpt-oss rules disabled")

    client = anthropic.Anthropic(base_url=base_url, api_key=api_key, max_retries=0, timeout=60)
    resp = client.messages.create(
        model="gpt-oss-from-messages",
        max_tokens=64,
        messages=[{"role": "user", "content": "Reply with exactly one word: pong"}],
    )
    assert resp.role == "assistant"
    assert resp.model == REAL_MODEL, f"model={resp.model!r}, want {REAL_MODEL!r}"
    assert resp.stop_reason
    text = "".join(b.text for b in resp.content if b.type == "text")
    assert text.strip(), f"empty translated text: {resp.content!r}"


def test_gptoss_translate_messages_tools(base_url: str, api_key: str, gptoss_enabled: bool) -> None:
    """Tool calls survive the messages↔chat translation against the real model."""
    if not gptoss_enabled:
        pytest.skip("SLIPSPACE_SMOKE_GPTOSS not set — cluster gpt-oss rules disabled")

    client = anthropic.Anthropic(base_url=base_url, api_key=api_key, max_retries=0, timeout=60)
    resp = client.messages.create(
        model="gpt-oss-from-messages",
        max_tokens=128,
        tools=[
            {
                "name": "get_weather",
                "description": "Get the current weather for a city.",
                "input_schema": {
                    "type": "object",
                    "properties": {"city": {"type": "string"}},
                    "required": ["city"],
                },
            }
        ],
        messages=[{"role": "user", "content": "What is the weather in San Francisco? Use the get_weather tool."}],
    )
    assert resp.model == REAL_MODEL
    tool_uses = [b for b in resp.content if b.type == "tool_use"]
    assert tool_uses, f"no tool_use block in translated response: {resp.content!r}"
    assert tool_uses[0].name == "get_weather"
    assert isinstance(tool_uses[0].input, dict)


def test_gptoss_translate_chat_text(base_url: str, api_key: str, gptoss_enabled: bool) -> None:
    """OpenAI Chat client → translate→messages → gpt-oss → translate back (reverse arm).

    The openai SDK parsing the response is the differential: a malformed
    translation would raise instead of returning a ChatCompletion.
    """
    if not gptoss_enabled:
        pytest.skip("SLIPSPACE_SMOKE_GPTOSS not set — cluster gpt-oss rules disabled")

    client = openai.OpenAI(base_url=f"{base_url}/v1", api_key=api_key, max_retries=0, timeout=60)
    resp = client.chat.completions.create(
        model="gpt-oss-from-chat",
        messages=[{"role": "user", "content": "Reply with exactly one word: pong"}],
        max_tokens=64,
        temperature=0,
    )
    assert resp.object == "chat.completion"
    assert resp.model == REAL_MODEL, f"model={resp.model!r}, want {REAL_MODEL!r}"
    assert resp.choices and resp.choices[0].finish_reason
    assert resp.choices[0].message.content.strip(), f"empty translated content: {resp.choices[0]!r}"


def test_gptoss_translate_chat_tools(base_url: str, api_key: str, gptoss_enabled: bool) -> None:
    """Tool calls survive the chat↔messages translation against the real model."""
    if not gptoss_enabled:
        pytest.skip("SLIPSPACE_SMOKE_GPTOSS not set — cluster gpt-oss rules disabled")

    client = openai.OpenAI(base_url=f"{base_url}/v1", api_key=api_key, max_retries=0, timeout=60)
    resp = client.chat.completions.create(
        model="gpt-oss-from-chat",
        max_tokens=128,
        tools=[
            {
                "type": "function",
                "function": {
                    "name": "get_weather",
                    "description": "Get the current weather for a city.",
                    "parameters": {
                        "type": "object",
                        "properties": {"city": {"type": "string"}},
                        "required": ["city"],
                    },
                },
            }
        ],
        messages=[{"role": "user", "content": "What is the weather in San Francisco? Use the get_weather tool."}],
    )
    assert resp.model == REAL_MODEL
    tool_calls = resp.choices[0].message.tool_calls
    assert tool_calls, f"no tool_calls in translated response: {resp.choices[0].message!r}"
    assert tool_calls[0].function.name == "get_weather"
    assert isinstance(json.loads(tool_calls[0].function.arguments), dict)
