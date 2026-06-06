"""Cross-SDK tests for sluice-gateway correlation/session header semantics.

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
            "X-Sluice-Session-Id": "sess-abc-123",
        },
        data=json.dumps({"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "x"}]}),
        timeout=15,
    )
    assert resp.status_code == 200, resp.text
    assert resp.headers.get("X-Sluice-Session-Id") == "sess-abc-123"


def test_agent_id_echoed_when_sent_via_default_header(gateway_url: str, mockllm_url: str) -> None:
    # Sent under the shipped client default header; the gateway resolves it and
    # echoes it under the authoritative Sluice agent header.
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
    assert resp.headers.get("X-Sluice-Agent-Id") == "agt-abc-123"


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
    assert resp.headers.get("X-Sluice-Agent-Id") is None


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
    correlation = resp.headers.get("X-Sluice-Correlation-Id")
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
            "X-Sluice-Correlation-Id": sent,
        },
        data=json.dumps({"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "x"}]}),
        timeout=15,
    )
    assert resp.status_code == 200, resp.text
    assert resp.headers.get("X-Sluice-Correlation-Id") == sent
