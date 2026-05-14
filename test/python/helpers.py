"""Test helpers for staging mockllm responses and assembling SDK clients."""

from __future__ import annotations

import json
from typing import Any

import requests

# Matches config-dev/api_keys.yaml. Not a credential — it's checked into the
# repo so the compat suite has a known-good handshake.
API_KEY = "sk_dev_local_development_only_not_for_production"


def stage_response(
    mockllm_url: str,
    *,
    method: str,
    path: str,
    status: int = 200,
    body: dict[str, Any] | str | None = None,
    headers: dict[str, str] | None = None,
    streaming: bool = False,
    stream_chunks: list[dict[str, Any]] | None = None,
    request_body_contains: str | None = None,
) -> None:
    payload: dict[str, Any] = {
        "method": method,
        "path": path,
        "status": status,
        "headers": headers or {"content-type": "application/json"},
        "streaming": streaming,
    }
    if streaming:
        payload["stream_chunks"] = stream_chunks or []
    else:
        payload["body"] = body if isinstance(body, str) else json.dumps(body or {})
    if request_body_contains:
        payload["request_body_contains"] = request_body_contains

    resp = requests.post(f"{mockllm_url}/control/responses", json=payload, timeout=10)
    if resp.status_code >= 400:
        raise RuntimeError(f"stage_response failed: {resp.status_code} {resp.text}")


def clear_responses(mockllm_url: str) -> None:
    resp = requests.delete(f"{mockllm_url}/control/responses", timeout=10)
    if resp.status_code >= 400:
        raise RuntimeError(f"clear_responses failed: {resp.status_code} {resp.text}")


def sse_chunk(data: dict[str, Any] | str) -> dict[str, Any]:
    """Build a stream_chunks entry for mockllm. The mockllm wraps Data in `data: %s\\n\\n`."""
    return {"data": data if isinstance(data, str) else json.dumps(data)}
