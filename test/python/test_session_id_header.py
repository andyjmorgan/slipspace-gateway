"""Cross-SDK tests for slipspace-gateway correlation/session header semantics.

These tests use raw HTTP (not the SDKs) because they assert on response headers
that the SDKs don't surface ergonomically. The SDK-shaped tests live in the
per-provider files; this file is the cross-cutting header contract.
"""

from __future__ import annotations

import json

import pytest
import requests

from helpers import API_KEY, stage_response


@pytest.fixture
def gateway_url(stack: dict[str, str]) -> str:
    return stack["gateway_url"]


@pytest.fixture
def mockllm_url(stack: dict[str, str]) -> str:
    return stack["mockllm_url"]


def _stage_chat_ok(mockllm_url: str) -> None:
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/chat/completions",
        body={
            "id": "chatcmpl-hdr",
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


def test_session_id_echoed_when_sent(gateway_url: str, mockllm_url: str) -> None:
    _stage_chat_ok(mockllm_url)
    resp = requests.post(
        f"{gateway_url}/v1/chat/completions",
        headers={
            "Authorization": f"Bearer {API_KEY}",
            "Content-Type": "application/json",
            "X-Slipspace-Session-Id": "sess-abc-123",
        },
        data=json.dumps({"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "x"}]}),
        timeout=15,
    )
    assert resp.status_code == 200, resp.text
    assert resp.headers.get("X-Slipspace-Session-Id") == "sess-abc-123"


def test_claude_agent_id_echoed_as_conversation(gateway_url: str, mockllm_url: str) -> None:
    # X-Claude-Code-Agent-Id is a subagent thread, not a named agent: it
    # resolves onto the conversation axis (echoed under X-Slipspace-Thread-Id) and
    # must NOT populate the named-agent header.
    _stage_chat_ok(mockllm_url)
    resp = requests.post(
        f"{gateway_url}/v1/chat/completions",
        headers={
            "Authorization": f"Bearer {API_KEY}",
            "Content-Type": "application/json",
            "X-Claude-Code-Agent-Id": "agt-abc-123",
        },
        data=json.dumps({"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "x"}]}),
        timeout=15,
    )
    assert resp.status_code == 200, resp.text
    assert resp.headers.get("X-Slipspace-Thread-Id") == "agt-abc-123"
    assert resp.headers.get("X-Slipspace-Agent-Id") is None


def test_named_agent_id_echoed_when_sent(gateway_url: str, mockllm_url: str) -> None:
    # gen_ai.agent.id is reserved for a named agent: only the authoritative
    # X-Slipspace-Agent-Id feeds it.
    _stage_chat_ok(mockllm_url)
    resp = requests.post(
        f"{gateway_url}/v1/chat/completions",
        headers={
            "Authorization": f"Bearer {API_KEY}",
            "Content-Type": "application/json",
            "X-Slipspace-Agent-Id": "reviewer",
        },
        data=json.dumps({"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "x"}]}),
        timeout=15,
    )
    assert resp.status_code == 200, resp.text
    assert resp.headers.get("X-Slipspace-Agent-Id") == "reviewer"


def test_codex_subagent_echoed(gateway_url: str, mockllm_url: str) -> None:
    # Codex subagent: Session-Id is the bundle root, Thread-Id the subagent
    # thread; both are echoed under their SlipSpace headers.
    _stage_chat_ok(mockllm_url)
    resp = requests.post(
        f"{gateway_url}/v1/chat/completions",
        headers={
            "Authorization": f"Bearer {API_KEY}",
            "Content-Type": "application/json",
            "Session-Id": "codex-sess-1",
            "Thread-Id": "codex-thread-2",
            "X-Codex-Parent-Thread-Id": "codex-sess-1",
        },
        data=json.dumps({"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "x"}]}),
        timeout=15,
    )
    assert resp.status_code == 200, resp.text
    assert resp.headers.get("X-Slipspace-Session-Id") == "codex-sess-1"
    assert resp.headers.get("X-Slipspace-Thread-Id") == "codex-thread-2"


def test_agent_id_not_echoed_when_absent(gateway_url: str, mockllm_url: str) -> None:
    _stage_chat_ok(mockllm_url)
    resp = requests.post(
        f"{gateway_url}/v1/chat/completions",
        headers={
            "Authorization": f"Bearer {API_KEY}",
            "Content-Type": "application/json",
        },
        data=json.dumps({"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "x"}]}),
        timeout=15,
    )
    assert resp.status_code == 200, resp.text
    assert resp.headers.get("X-Slipspace-Agent-Id") is None


def test_user_id_echoed_when_sent(gateway_url: str, mockllm_url: str) -> None:
    # There is no shipped client default for user id, so it is sent under the
    # authoritative SlipSpace header; the gateway resolves and echoes it.
    _stage_chat_ok(mockllm_url)
    resp = requests.post(
        f"{gateway_url}/v1/chat/completions",
        headers={
            "Authorization": f"Bearer {API_KEY}",
            "Content-Type": "application/json",
            "X-Slipspace-User-Id": "user-abc-123",
        },
        data=json.dumps({"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "x"}]}),
        timeout=15,
    )
    assert resp.status_code == 200, resp.text
    assert resp.headers.get("X-Slipspace-User-Id") == "user-abc-123"


def test_user_id_not_echoed_when_absent(gateway_url: str, mockllm_url: str) -> None:
    _stage_chat_ok(mockllm_url)
    resp = requests.post(
        f"{gateway_url}/v1/chat/completions",
        headers={
            "Authorization": f"Bearer {API_KEY}",
            "Content-Type": "application/json",
        },
        data=json.dumps({"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "x"}]}),
        timeout=15,
    )
    assert resp.status_code == 200, resp.text
    assert resp.headers.get("X-Slipspace-User-Id") is None


def test_correlation_id_generated_when_absent(gateway_url: str, mockllm_url: str) -> None:
    _stage_chat_ok(mockllm_url)
    resp = requests.post(
        f"{gateway_url}/v1/chat/completions",
        headers={
            "Authorization": f"Bearer {API_KEY}",
            "Content-Type": "application/json",
        },
        data=json.dumps({"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "x"}]}),
        timeout=15,
    )
    assert resp.status_code == 200, resp.text
    correlation = resp.headers.get("X-Slipspace-Correlation-Id")
    assert correlation, "gateway must generate a correlation id when client omits it"
    assert len(correlation) >= 8


def test_correlation_id_echoed_when_sent(gateway_url: str, mockllm_url: str) -> None:
    _stage_chat_ok(mockllm_url)
    sent = "test-correlation-7c3"
    resp = requests.post(
        f"{gateway_url}/v1/chat/completions",
        headers={
            "Authorization": f"Bearer {API_KEY}",
            "Content-Type": "application/json",
            "X-Slipspace-Correlation-Id": sent,
        },
        data=json.dumps({"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "x"}]}),
        timeout=15,
    )
    assert resp.status_code == 200, resp.text
    assert resp.headers.get("X-Slipspace-Correlation-Id") == sent
