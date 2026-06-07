"""Native-SDK wire-compat for cross-provider translation.

The official `anthropic` Python SDK is the client. Each test points it at the
gateway with the `x-sluice-translate: chat` probe header, which fires the
config-dev `translate-messages-to-chat` rule: the inbound Anthropic Messages
request is translated to OpenAI Chat and routed to anthropic's chat endpoint,
the mock returns a NATURAL OpenAI Chat response, and the gateway translates it
back to Anthropic Messages.

This is the strongest differential we have: the SDK's own response models parse
the translated bytes, so a malformed translation surfaces as an SDK validation
error, not a silent shape drift.
"""

from __future__ import annotations

import anthropic
import pytest

from helpers import API_KEY, sse_chunk, stage_response


@pytest.fixture
def gateway_url(stack: dict[str, str]) -> str:
    return stack["gateway_url"]


@pytest.fixture
def mockllm_url(stack: dict[str, str]) -> str:
    return stack["mockllm_url"]


def _xlate_client(gateway_url: str) -> anthropic.Anthropic:
    """Anthropic SDK pointed at the gateway with the translate probe header."""
    return anthropic.Anthropic(
        base_url=gateway_url,
        api_key=API_KEY,
        default_headers={"x-sluice-translate": "chat"},
        max_retries=0,
    )


def test_translate_non_streaming(gateway_url: str, mockllm_url: str) -> None:
    # Mock returns a natural OpenAI Chat response on the chat endpoint the
    # translation routes to.
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/chat/completions",
        body={
            "id": "chatcmpl-1",
            "object": "chat.completion",
            "model": "gpt-4o",
            "choices": [
                {
                    "index": 0,
                    "message": {"role": "assistant", "content": "hello from openai"},
                    "finish_reason": "stop",
                }
            ],
            "usage": {"prompt_tokens": 5, "completion_tokens": 3, "total_tokens": 8},
        },
    )

    client = _xlate_client(gateway_url)
    resp = client.messages.create(
        model="claude-3-5-sonnet-latest",
        max_tokens=16,
        messages=[{"role": "user", "content": "hi"}],
    )

    # The SDK parsed the translated response into its Message model.
    assert resp.role == "assistant"
    assert resp.content[0].type == "text"
    assert resp.content[0].text == "hello from openai"
    assert resp.stop_reason == "end_turn"
    assert resp.usage.input_tokens == 5
    assert resp.usage.output_tokens == 3


def test_translate_tool_use(gateway_url: str, mockllm_url: str) -> None:
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/chat/completions",
        body={
            "id": "chatcmpl-2",
            "object": "chat.completion",
            "model": "gpt-4o",
            "choices": [
                {
                    "index": 0,
                    "message": {
                        "role": "assistant",
                        "content": None,
                        "tool_calls": [
                            {
                                "id": "call_1",
                                "type": "function",
                                "function": {
                                    "name": "get_weather",
                                    "arguments": '{"city":"SF"}',
                                },
                            }
                        ],
                    },
                    "finish_reason": "tool_calls",
                }
            ],
            "usage": {"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5},
        },
    )

    client = _xlate_client(gateway_url)
    resp = client.messages.create(
        model="claude-3-5-sonnet-latest",
        max_tokens=16,
        tools=[
            {
                "name": "get_weather",
                "description": "look up weather",
                "input_schema": {
                    "type": "object",
                    "properties": {"city": {"type": "string"}},
                },
            }
        ],
        messages=[{"role": "user", "content": "weather?"}],
    )

    assert resp.stop_reason == "tool_use"
    tool_uses = [b for b in resp.content if b.type == "tool_use"]
    assert len(tool_uses) == 1
    assert tool_uses[0].name == "get_weather"
    assert tool_uses[0].input == {"city": "SF"}


def test_translate_error(gateway_url: str, mockllm_url: str) -> None:
    # An OpenAI error on the chat endpoint must come back as a well-formed
    # Anthropic error — the SDK parses status + envelope into a typed exception.
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/chat/completions",
        status=429,
        body={"error": {"message": "slow down", "type": "rate_limit_exceeded"}},
    )

    client = _xlate_client(gateway_url)
    with pytest.raises(anthropic.RateLimitError) as exc:
        client.messages.create(
            model="claude-3-5-sonnet-latest",
            max_tokens=16,
            messages=[{"role": "user", "content": "hi"}],
        )
    # The SDK decoded the translated Anthropic error envelope.
    assert "slow down" in str(exc.value)


def test_translate_streaming(gateway_url: str, mockllm_url: str) -> None:
    # Mock returns a natural OpenAI Chat SSE stream.
    chunks = [
        sse_chunk(
            {
                "id": "c1",
                "model": "gpt-4o",
                "choices": [{"index": 0, "delta": {"role": "assistant"}}],
            }
        ),
        sse_chunk({"choices": [{"index": 0, "delta": {"content": "hi "}}]}),
        sse_chunk({"choices": [{"index": 0, "delta": {"content": "stream"}}]}),
        sse_chunk({"choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]}),
        sse_chunk("[DONE]"),
    ]
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/chat/completions",
        streaming=True,
        stream_chunks=chunks,
        headers={"content-type": "text/event-stream"},
    )

    client = _xlate_client(gateway_url)
    text_pieces: list[str] = []
    with client.messages.stream(
        model="claude-3-5-sonnet-latest",
        max_tokens=16,
        messages=[{"role": "user", "content": "hi"}],
    ) as stream:
        for event in stream:
            if event.type == "content_block_delta" and event.delta.type == "text_delta":
                text_pieces.append(event.delta.text)
        # get_final_message forces the SDK to validate the full event lifecycle
        # (message_start .. message_stop) and assemble the terminal Message.
        final = stream.get_final_message()

    assert "".join(text_pieces) == "hi stream"
    assert final.role == "assistant"
    assert final.content[0].text == "hi stream"
    assert final.stop_reason == "end_turn"
