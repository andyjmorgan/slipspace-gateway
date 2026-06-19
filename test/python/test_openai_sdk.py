"""Wire-compat tests for the official `openai` Python SDK against slipspace-gateway."""

from __future__ import annotations

import time

import openai
import pytest

from helpers import API_KEY, sse_chunk, stage_response


@pytest.fixture
def gateway_url(stack: dict[str, str]) -> str:
    return stack["gateway_url"]


@pytest.fixture
def mockllm_url(stack: dict[str, str]) -> str:
    return stack["mockllm_url"]


def _client(gateway_url: str, *, api_key: str = API_KEY, headers: dict[str, str] | None = None) -> openai.OpenAI:
    return openai.OpenAI(
        base_url=f"{gateway_url}/v1",
        api_key=api_key,
        default_headers=headers,
        max_retries=0,
    )


def test_chat_completion_managed(gateway_url: str, mockllm_url: str) -> None:
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/chat/completions",
        body={
            "id": "chatcmpl-pycompat-1",
            "object": "chat.completion",
            "created": 1700000000,
            "model": "gpt-4o-mini",
            "choices": [
                {
                    "index": 0,
                    "message": {"role": "assistant", "content": "pong"},
                    "finish_reason": "stop",
                }
            ],
            "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
        },
    )

    client = _client(gateway_url)
    resp = client.chat.completions.create(
        model="gpt-4o-mini",
        messages=[{"role": "user", "content": "ping"}],
    )

    assert resp.id == "chatcmpl-pycompat-1"
    assert resp.model == "gpt-4o-mini"
    assert resp.choices[0].message.content == "pong"
    assert resp.choices[0].finish_reason == "stop"
    assert resp.usage is not None and resp.usage.total_tokens == 2


def test_chat_completion_streaming(gateway_url: str, mockllm_url: str) -> None:
    chunks = [
        sse_chunk({
            "id": "chatcmpl-stream",
            "object": "chat.completion.chunk",
            "created": 1700000000,
            "model": "gpt-4o-mini",
            "choices": [{"index": 0, "delta": {"role": "assistant"}, "finish_reason": None}],
        }),
        sse_chunk({
            "id": "chatcmpl-stream",
            "object": "chat.completion.chunk",
            "created": 1700000000,
            "model": "gpt-4o-mini",
            "choices": [{"index": 0, "delta": {"content": "hi "}, "finish_reason": None}],
        }),
        sse_chunk({
            "id": "chatcmpl-stream",
            "object": "chat.completion.chunk",
            "created": 1700000000,
            "model": "gpt-4o-mini",
            "choices": [{"index": 0, "delta": {"content": "there"}, "finish_reason": None}],
        }),
        sse_chunk({
            "id": "chatcmpl-stream",
            "object": "chat.completion.chunk",
            "created": 1700000000,
            "model": "gpt-4o-mini",
            "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}],
        }),
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

    client = _client(gateway_url)
    stream = client.chat.completions.create(
        model="gpt-4o-mini",
        messages=[{"role": "user", "content": "stream"}],
        stream=True,
    )

    pieces: list[str] = []
    finish = None
    for event in stream:
        if event.choices:
            delta = event.choices[0].delta
            if delta and delta.content:
                pieces.append(delta.content)
            if event.choices[0].finish_reason:
                finish = event.choices[0].finish_reason

    assert "".join(pieces) == "hi there"
    assert finish == "stop"


def test_chat_completion_tool_use(gateway_url: str, mockllm_url: str) -> None:
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/chat/completions",
        body={
            "id": "chatcmpl-tool",
            "object": "chat.completion",
            "created": 1700000000,
            "model": "gpt-4o-mini",
            "choices": [
                {
                    "index": 0,
                    "message": {
                        "role": "assistant",
                        "content": None,
                        "tool_calls": [
                            {
                                "id": "call_abc",
                                "type": "function",
                                "function": {
                                    "name": "get_weather",
                                    "arguments": '{"city":"Dublin"}',
                                },
                            }
                        ],
                    },
                    "finish_reason": "tool_calls",
                }
            ],
            "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
        },
    )

    client = _client(gateway_url)
    resp = client.chat.completions.create(
        model="gpt-4o-mini",
        messages=[{"role": "user", "content": "weather?"}],
        tools=[
            {
                "type": "function",
                "function": {
                    "name": "get_weather",
                    "description": "Get the weather",
                    "parameters": {
                        "type": "object",
                        "properties": {"city": {"type": "string"}},
                        "required": ["city"],
                    },
                },
            }
        ],
    )

    msg = resp.choices[0].message
    assert msg.tool_calls is not None
    assert msg.tool_calls[0].function.name == "get_weather"
    assert msg.tool_calls[0].function.arguments == '{"city":"Dublin"}'
    assert resp.choices[0].finish_reason == "tool_calls"


def test_responses_api(gateway_url: str, mockllm_url: str) -> None:
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/responses",
        body={
            "id": "resp_pycompat_1",
            "object": "response",
            "created_at": 1700000000,
            "status": "completed",
            "model": "gpt-4o-mini",
            "output": [
                {
                    "type": "message",
                    "id": "msg_1",
                    "role": "assistant",
                    "status": "completed",
                    "content": [
                        {"type": "output_text", "text": "ok", "annotations": []}
                    ],
                }
            ],
            "usage": {
                "input_tokens": 1,
                "output_tokens": 1,
                "total_tokens": 2,
                "input_tokens_details": {"cached_tokens": 0},
                "output_tokens_details": {"reasoning_tokens": 0},
            },
        },
    )

    client = _client(gateway_url)
    resp = client.responses.create(model="gpt-4o-mini", input="hello")
    assert resp.id == "resp_pycompat_1"
    assert resp.status == "completed"
    assert resp.output[0].type == "message"


def test_list_models(gateway_url: str, mockllm_url: str) -> None:
    stage_response(
        mockllm_url,
        method="GET",
        path="/v1/models",
        body={
            "object": "list",
            "data": [
                {"id": "gpt-4o-mini", "object": "model", "created": 1700000000, "owned_by": "openai"},
                {"id": "gpt-4o", "object": "model", "created": 1700000000, "owned_by": "openai"},
            ],
        },
    )

    client = _client(gateway_url)
    models = client.models.list()
    ids = [m.id for m in models.data]
    assert "gpt-4o-mini" in ids and "gpt-4o" in ids


def test_passthrough_mode(gateway_url: str, mockllm_url: str) -> None:
    """Send a non-SlipSpace bearer + X-Slipspace-Configuration → upstream sees the raw bearer."""
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/chat/completions",
        body={
            "id": "chatcmpl-pt",
            "object": "chat.completion",
            "created": 1700000000,
            "model": "gpt-4o-mini",
            "choices": [
                {
                    "index": 0,
                    "message": {"role": "assistant", "content": "ok"},
                    "finish_reason": "stop",
                }
            ],
            "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
        },
    )

    client = _client(
        gateway_url,
        api_key="customer-supplied-token",
        headers={"X-Slipspace-Configuration": "dev"},
    )
    resp = client.chat.completions.create(
        model="gpt-4o-mini",
        messages=[{"role": "user", "content": "passthrough"}],
    )
    assert resp.choices[0].message.content == "ok"


def test_managed_invalid_key(gateway_url: str) -> None:
    client = _client(gateway_url, api_key="sk_not_a_real_key")
    with pytest.raises(openai.AuthenticationError):
        client.chat.completions.create(
            model="gpt-4o-mini",
            messages=[{"role": "user", "content": "noop"}],
        )


def test_unknown_passthrough_configuration(gateway_url: str) -> None:
    client = _client(
        gateway_url,
        api_key="customer-supplied-token",
        headers={"X-Slipspace-Configuration": "does-not-exist"},
    )
    with pytest.raises(openai.PermissionDeniedError):
        client.chat.completions.create(
            model="gpt-4o-mini",
            messages=[{"role": "user", "content": "noop"}],
        )


def test_streaming_chunks_arrive_incrementally(gateway_url: str, mockllm_url: str) -> None:
    chunks = [
        sse_chunk({
            "id": "stream-timing",
            "object": "chat.completion.chunk",
            "created": 1700000000,
            "model": "gpt-4o-mini",
            "choices": [{"index": 0, "delta": {"content": f"piece{i} "}, "finish_reason": None}],
            "delay_ms": 50,
        })
        for i in range(3)
    ]
    chunks.append(
        sse_chunk({
            "id": "stream-timing",
            "object": "chat.completion.chunk",
            "created": 1700000000,
            "model": "gpt-4o-mini",
            "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}],
        })
    )
    chunks.append(sse_chunk("[DONE]"))

    for c in chunks[:3]:
        c["delay_ms"] = 50

    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/chat/completions",
        streaming=True,
        stream_chunks=chunks,
        headers={"content-type": "text/event-stream"},
    )

    client = _client(gateway_url)
    start = time.monotonic()
    timings: list[float] = []
    stream = client.chat.completions.create(
        model="gpt-4o-mini",
        messages=[{"role": "user", "content": "x"}],
        stream=True,
    )
    for event in stream:
        if event.choices and event.choices[0].delta and event.choices[0].delta.content:
            timings.append(time.monotonic() - start)

    assert len(timings) >= 2
    assert max(timings) - min(timings) > 0.04, "stream chunks did not arrive incrementally"
