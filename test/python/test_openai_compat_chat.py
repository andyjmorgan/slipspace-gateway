"""Wire-compat: official OpenAI SDK against the OpenAI-compat chat surfaces
that v1.0.2 wires on Anthropic and Gemini.

The SDK is identical to `test_openai_sdk.py`; the only difference is the
`base_url` — pointing at the bare gateway `{gateway}/v1`; model-keyed bindings route claude-* to anthropic and gemini-* to gemini.
The gateway's per-endpoint `auth_header: Authorization, auth_format: "Bearer {key}"`
override means upstream sees `Authorization: Bearer ...` rather than the
provider-native `x-api-key` / `x-goog-api-key`.
"""

from __future__ import annotations

import openai
import pytest

from helpers import API_KEY, stage_response


@pytest.fixture
def gateway_url(stack: dict[str, str]) -> str:
    return stack["gateway_url"]


@pytest.fixture
def mockllm_url(stack: dict[str, str]) -> str:
    return stack["mockllm_url"]


def _canned_chat(model: str, content: str, id_: str) -> dict:
    return {
        "id": id_,
        "object": "chat.completion",
        "created": 1700000000,
        "model": model,
        "choices": [
            {
                "index": 0,
                "message": {"role": "assistant", "content": content},
                "finish_reason": "stop",
            }
        ],
        "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
    }


def test_anthropic_openai_compat_chat(gateway_url: str, mockllm_url: str) -> None:
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/chat/completions",
        body=_canned_chat("claude-haiku-4-5", "pong-anth", "chatcmpl-anth-pycompat"),
    )

    client = openai.OpenAI(
        base_url=f"{gateway_url}/v1",
        api_key=API_KEY,
        max_retries=0,
    )
    resp = client.chat.completions.create(
        model="claude-haiku-4-5",
        messages=[{"role": "user", "content": "ping"}],
    )

    assert resp.id == "chatcmpl-anth-pycompat"
    assert resp.choices[0].message.content == "pong-anth"
    assert resp.choices[0].finish_reason == "stop"


def test_gemini_openai_compat_chat(gateway_url: str, mockllm_url: str) -> None:
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1beta/openai/chat/completions",
        body=_canned_chat("gemini-2.0-flash-001", "pong-gem", "chatcmpl-gem-pycompat"),
    )

    client = openai.OpenAI(
        base_url=f"{gateway_url}/v1",
        api_key=API_KEY,
        max_retries=0,
    )
    resp = client.chat.completions.create(
        model="gemini-2.0-flash-001",
        messages=[{"role": "user", "content": "ping"}],
    )

    assert resp.id == "chatcmpl-gem-pycompat"
    assert resp.choices[0].message.content == "pong-gem"
    assert resp.choices[0].finish_reason == "stop"
