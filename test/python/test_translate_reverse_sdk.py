"""Native-SDK wire-compat for the REVERSE cross-provider translation arm.

The official `openai` Python SDK is the client. Each test points it at the
gateway with the `x-slipspace-translate: messages` probe header, which fires the
config-dev `translate-chat-to-messages` rule: the inbound OpenAI Chat request is
translated to Anthropic Messages and routed to anthropic's messages endpoint,
the mock returns a NATURAL Anthropic Messages response, and the gateway
translates it back to OpenAI Chat.

This is the strongest differential we have for this direction: the OpenAI SDK's
own response models parse the translated bytes, so a malformed translation
surfaces as an SDK validation error, not a silent shape drift.
"""

from __future__ import annotations

import json

import openai
import pytest

from helpers import API_KEY, sse_chunk, stage_response


@pytest.fixture
def gateway_url(stack: dict[str, str]) -> str:
    return stack["gateway_url"]


@pytest.fixture
def mockllm_url(stack: dict[str, str]) -> str:
    return stack["mockllm_url"]


def _xlate_client(gateway_url: str) -> openai.OpenAI:
    """OpenAI SDK pointed at the gateway with the translate probe header."""
    return openai.OpenAI(
        base_url=f"{gateway_url}/v1",
        api_key=API_KEY,
        default_headers={"x-slipspace-translate": "messages"},
        max_retries=0,
    )


def test_reverse_translate_non_streaming(gateway_url: str, mockllm_url: str) -> None:
    # Mock returns a natural Anthropic Messages response on the messages endpoint
    # the translation routes to.
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/messages",
        body={
            "id": "msg_1",
            "type": "message",
            "role": "assistant",
            "model": "claude-x",
            "content": [{"type": "text", "text": "hello from anthropic"}],
            "stop_reason": "end_turn",
            "usage": {"input_tokens": 5, "output_tokens": 3},
        },
    )

    client = _xlate_client(gateway_url)
    resp = client.chat.completions.create(
        model="claude-3-5-sonnet-latest",
        messages=[{"role": "user", "content": "hi"}],
    )

    # The SDK parsed the translated response into its ChatCompletion model.
    assert resp.object == "chat.completion"
    assert resp.choices[0].message.role == "assistant"
    assert resp.choices[0].message.content == "hello from anthropic"
    assert resp.choices[0].finish_reason == "stop"
    assert resp.usage.prompt_tokens == 5
    assert resp.usage.completion_tokens == 3


def test_reverse_translate_tool_use(gateway_url: str, mockllm_url: str) -> None:
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/messages",
        body={
            "id": "msg_2",
            "type": "message",
            "role": "assistant",
            "model": "claude-x",
            "content": [
                {
                    "type": "tool_use",
                    "id": "tu_1",
                    "name": "get_weather",
                    "input": {"city": "SF"},
                }
            ],
            "stop_reason": "tool_use",
            "usage": {"input_tokens": 3, "output_tokens": 2},
        },
    )

    client = _xlate_client(gateway_url)
    resp = client.chat.completions.create(
        model="claude-3-5-sonnet-latest",
        tools=[
            {
                "type": "function",
                "function": {
                    "name": "get_weather",
                    "description": "look up weather",
                    "parameters": {
                        "type": "object",
                        "properties": {"city": {"type": "string"}},
                    },
                },
            }
        ],
        messages=[{"role": "user", "content": "weather?"}],
    )

    assert resp.choices[0].finish_reason == "tool_calls"
    tool_calls = resp.choices[0].message.tool_calls
    assert len(tool_calls) == 1
    assert tool_calls[0].function.name == "get_weather"
    assert json.loads(tool_calls[0].function.arguments) == {"city": "SF"}


def test_reverse_translate_error(gateway_url: str, mockllm_url: str) -> None:
    # An Anthropic error on the messages endpoint must come back as a well-formed
    # OpenAI error — the SDK parses status + envelope into a typed exception.
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/messages",
        status=429,
        body={"type": "error", "error": {"type": "rate_limit_error", "message": "slow down"}},
    )

    client = _xlate_client(gateway_url)
    with pytest.raises(openai.RateLimitError) as exc:
        client.chat.completions.create(
            model="claude-3-5-sonnet-latest",
            messages=[{"role": "user", "content": "hi"}],
        )
    # The SDK decoded the translated OpenAI error envelope.
    assert "slow down" in str(exc.value)


def test_reverse_translate_streaming(gateway_url: str, mockllm_url: str) -> None:
    # Mock returns a natural Anthropic Messages SSE stream (no [DONE] sentinel —
    # the translator synthesises the OpenAI [DONE] at end of stream).
    chunks = [
        sse_chunk(
            {
                "type": "message_start",
                "message": {
                    "id": "m",
                    "type": "message",
                    "role": "assistant",
                    "model": "claude-x",
                    "content": [],
                    "usage": {"input_tokens": 4, "output_tokens": 0},
                },
            }
        ),
        sse_chunk({"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}),
        sse_chunk({"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "hi "}}),
        sse_chunk({"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "stream"}}),
        sse_chunk({"type": "content_block_stop", "index": 0}),
        sse_chunk({"type": "message_delta", "delta": {"stop_reason": "end_turn"}, "usage": {"output_tokens": 2}}),
        sse_chunk({"type": "message_stop"}),
    ]
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/messages",
        streaming=True,
        stream_chunks=chunks,
        headers={"content-type": "text/event-stream"},
    )

    client = _xlate_client(gateway_url)
    text_pieces: list[str] = []
    finish = None
    for chunk in client.chat.completions.create(
        model="claude-3-5-sonnet-latest",
        messages=[{"role": "user", "content": "hi"}],
        stream=True,
    ):
        if not chunk.choices:
            continue
        delta = chunk.choices[0].delta
        if delta.content:
            text_pieces.append(delta.content)
        if chunk.choices[0].finish_reason:
            finish = chunk.choices[0].finish_reason

    assert "".join(text_pieces) == "hi stream"
    assert finish == "stop"
