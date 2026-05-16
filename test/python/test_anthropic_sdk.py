"""Wire-compat tests for the official `anthropic` Python SDK against sluice-gateway."""

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


def _client(
    gateway_url: str,
    *,
    api_key: str = API_KEY,
    headers: dict[str, str] | None = None,
) -> anthropic.Anthropic:
    """Build an Anthropic client pointed at sluice.

    v1.0.7: Sluice discovers the managed-mode key from `x-api-key` (the
    Anthropic SDK's native default), so no Authorization injection is
    required. Tests that need an explicit Authorization header can pass it
    via `headers`.
    """
    return anthropic.Anthropic(
        base_url=f"{gateway_url}/anthropic",
        api_key=api_key,
        default_headers=headers,
        max_retries=0,
    )


def _client_bare(
    gateway_url: str,
    *,
    api_key: str = API_KEY,
) -> anthropic.Anthropic:
    """Anthropic SDK pointed at the gateway root (no /anthropic prefix).

    Exercises v1.0.6's prefix_optional on anthropic.messages and v1.0.7's
    x-api-key discovery — a vanilla SDK with base_url=https://sluice...
    and api_key=sk_live_... must reach /v1/messages with no extra config.
    """
    return anthropic.Anthropic(
        base_url=gateway_url,
        api_key=api_key,
        max_retries=0,
    )


def test_messages_managed_bare_route(gateway_url: str, mockllm_url: str) -> None:
    """Same payload as test_messages_managed but via the prefix-optional bare route."""
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/messages",
        body={
            "id": "msg_pycompat_bare",
            "type": "message",
            "role": "assistant",
            "model": "claude-3-5-sonnet-latest",
            "content": [{"type": "text", "text": "pong"}],
            "stop_reason": "end_turn",
            "stop_sequence": None,
            "usage": {"input_tokens": 1, "output_tokens": 1},
        },
    )

    client = _client_bare(gateway_url)
    resp = client.messages.create(
        model="claude-3-5-sonnet-latest",
        max_tokens=64,
        messages=[{"role": "user", "content": "ping"}],
    )
    assert resp.id == "msg_pycompat_bare"
    assert resp.content[0].text == "pong"


def test_messages_managed(gateway_url: str, mockllm_url: str) -> None:
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/messages",
        body={
            "id": "msg_pycompat_1",
            "type": "message",
            "role": "assistant",
            "model": "claude-3-5-sonnet-latest",
            "content": [{"type": "text", "text": "pong"}],
            "stop_reason": "end_turn",
            "stop_sequence": None,
            "usage": {"input_tokens": 1, "output_tokens": 1},
        },
    )

    client = _client(gateway_url)
    resp = client.messages.create(
        model="claude-3-5-sonnet-latest",
        max_tokens=64,
        messages=[{"role": "user", "content": "ping"}],
    )
    assert resp.id == "msg_pycompat_1"
    assert resp.model == "claude-3-5-sonnet-latest"
    assert resp.content[0].text == "pong"
    assert resp.stop_reason == "end_turn"


def test_messages_with_image(gateway_url: str, mockllm_url: str) -> None:
    """Multimodal request: assert the SDK accepts the response after we route image content through."""
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/messages",
        body={
            "id": "msg_image",
            "type": "message",
            "role": "assistant",
            "model": "claude-3-5-sonnet-latest",
            "content": [{"type": "text", "text": "image acknowledged"}],
            "stop_reason": "end_turn",
            "stop_sequence": None,
            "usage": {"input_tokens": 5, "output_tokens": 3},
        },
    )

    client = _client(gateway_url)
    resp = client.messages.create(
        model="claude-3-5-sonnet-latest",
        max_tokens=128,
        messages=[
            {
                "role": "user",
                "content": [
                    {"type": "text", "text": "what is in this image?"},
                    {
                        "type": "image",
                        "source": {
                            "type": "base64",
                            "media_type": "image/png",
                            "data": "iVBORw0KGgo=",
                        },
                    },
                ],
            }
        ],
    )
    assert resp.content[0].text == "image acknowledged"


@pytest.mark.skip(
    reason=(
        "mockllm's writeStream wraps every chunk in `data: %s\\n\\n` and has no way to emit "
        "an `event:` line. Anthropic's SDK SSE decoder requires explicit `event:` lines and "
        "discards events without them. Re-enable once cmd/mockllm gains a StreamChunk.Event "
        "field (tracked in DonkeyWork as part of the streaming spec)."
    )
)
def test_messages_streaming(gateway_url: str, mockllm_url: str) -> None:
    chunks = [
        sse_chunk({
            "type": "message_start",
            "message": {
                "id": "msg_stream",
                "type": "message",
                "role": "assistant",
                "model": "claude-3-5-sonnet-latest",
                "content": [],
                "stop_reason": None,
                "stop_sequence": None,
                "usage": {"input_tokens": 1, "output_tokens": 0},
            },
        }),
        sse_chunk({
            "type": "content_block_start",
            "index": 0,
            "content_block": {"type": "text", "text": ""},
        }),
        sse_chunk({
            "type": "content_block_delta",
            "index": 0,
            "delta": {"type": "text_delta", "text": "hello"},
        }),
        sse_chunk({
            "type": "content_block_delta",
            "index": 0,
            "delta": {"type": "text_delta", "text": " world"},
        }),
        sse_chunk({"type": "content_block_stop", "index": 0}),
        sse_chunk({
            "type": "message_delta",
            "delta": {"stop_reason": "end_turn", "stop_sequence": None},
            "usage": {"output_tokens": 2},
        }),
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

    client = _client(gateway_url)
    seen_event_types: list[str] = []
    text_pieces: list[str] = []
    with client.messages.stream(
        model="claude-3-5-sonnet-latest",
        max_tokens=64,
        messages=[{"role": "user", "content": "ping"}],
    ) as stream:
        for event in stream:
            seen_event_types.append(event.type)
            if event.type == "content_block_delta" and getattr(event.delta, "text", None):
                text_pieces.append(event.delta.text)

    assert "message_start" in seen_event_types
    assert "content_block_start" in seen_event_types
    assert "content_block_delta" in seen_event_types
    assert "content_block_stop" in seen_event_types
    assert "message_delta" in seen_event_types
    assert "message_stop" in seen_event_types
    assert "".join(text_pieces) == "hello world"


def test_messages_with_tools(gateway_url: str, mockllm_url: str) -> None:
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/messages",
        body={
            "id": "msg_tools",
            "type": "message",
            "role": "assistant",
            "model": "claude-3-5-sonnet-latest",
            "content": [
                {
                    "type": "tool_use",
                    "id": "toolu_abc",
                    "name": "get_weather",
                    "input": {"city": "Dublin"},
                }
            ],
            "stop_reason": "tool_use",
            "stop_sequence": None,
            "usage": {"input_tokens": 5, "output_tokens": 12},
        },
    )

    client = _client(gateway_url)
    resp = client.messages.create(
        model="claude-3-5-sonnet-latest",
        max_tokens=128,
        messages=[{"role": "user", "content": "weather?"}],
        tools=[
            {
                "name": "get_weather",
                "description": "Get weather for a city",
                "input_schema": {
                    "type": "object",
                    "properties": {"city": {"type": "string"}},
                    "required": ["city"],
                },
            }
        ],
    )
    assert resp.stop_reason == "tool_use"
    assert resp.content[0].type == "tool_use"
    assert resp.content[0].name == "get_weather"
    assert resp.content[0].input == {"city": "Dublin"}


def test_list_models(gateway_url: str, mockllm_url: str) -> None:
    stage_response(
        mockllm_url,
        method="GET",
        path="/v1/models",
        body={
            "data": [
                {
                    "type": "model",
                    "id": "claude-3-5-sonnet-latest",
                    "display_name": "Claude 3.5 Sonnet",
                    "created_at": "2024-10-22T00:00:00Z",
                },
                {
                    "type": "model",
                    "id": "claude-3-5-haiku-latest",
                    "display_name": "Claude 3.5 Haiku",
                    "created_at": "2024-11-04T00:00:00Z",
                },
            ],
            "has_more": False,
            "first_id": "claude-3-5-sonnet-latest",
            "last_id": "claude-3-5-haiku-latest",
        },
    )

    client = _client(gateway_url)
    page = client.models.list()
    ids = [m.id for m in page.data]
    assert "claude-3-5-sonnet-latest" in ids
    assert "claude-3-5-haiku-latest" in ids


def test_passthrough_mode(gateway_url: str, mockllm_url: str) -> None:
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/messages",
        body={
            "id": "msg_pt",
            "type": "message",
            "role": "assistant",
            "model": "claude-3-5-sonnet-latest",
            "content": [{"type": "text", "text": "ok"}],
            "stop_reason": "end_turn",
            "stop_sequence": None,
            "usage": {"input_tokens": 1, "output_tokens": 1},
        },
    )

    client = _client(
        gateway_url,
        api_key="byok-token-xyz",
        headers={"X-Sluice-Configuration": "dev"},
    )
    resp = client.messages.create(
        model="claude-3-5-sonnet-latest",
        max_tokens=32,
        messages=[{"role": "user", "content": "passthrough"}],
    )
    assert resp.content[0].text == "ok"


def test_managed_invalid_key(gateway_url: str) -> None:
    client = _client(gateway_url, api_key="sk_not_real")
    with pytest.raises(anthropic.AuthenticationError):
        client.messages.create(
            model="claude-3-5-sonnet-latest",
            max_tokens=8,
            messages=[{"role": "user", "content": "x"}],
        )
