"""Wire-compat tests for the official `google-genai` Python SDK against slipspace-gateway.

The Gemini streaming endpoint (`streamGenerateContent`) is not yet listed in
the dev config's `accepted_paths`, so this file omits a streaming test. When
streaming routing is added, follow the non-stream pattern below.
"""

from __future__ import annotations

import pytest
from google import genai
from google.genai import types

from helpers import API_KEY, stage_response


@pytest.fixture
def gateway_url(stack: dict[str, str]) -> str:
    return stack["gateway_url"]


@pytest.fixture
def mockllm_url(stack: dict[str, str]) -> str:
    return stack["mockllm_url"]


# The SDK's default API version moved from v1beta to v1 in google-genai 3.0.0
# (https://github.com/googleapis/python-genai/blob/main/CHANGELOG.md), and the
# gateway must serve both (https://ai.google.dev/gemini-api/docs/api-versions).
# Every client here pins `api_version` explicitly so the suite covers both path
# shapes regardless of which one the installed SDK defaults to (issue #631).
V1BETA = "v1beta"
V1 = "v1"


def _client(
    gateway_url: str,
    *,
    api_key: str = API_KEY,
    extra_headers: dict[str, str] | None = None,
    api_version: str = V1BETA,
) -> genai.Client:
    """Build a Gemini client pointed at slipspace.

    v1.0.7: SlipSpace discovers the managed-mode key from `x-goog-api-key` (the
    google-genai SDK's native default), so no Authorization injection is
    required. Tests that need extra inbound headers can pass them via
    `extra_headers`.
    """
    headers = extra_headers or {}
    return genai.Client(
        api_key=api_key,
        http_options=types.HttpOptions(
            base_url=gateway_url,
            headers=headers,
            api_version=api_version,
        ),
    )


def _client_bare(
    gateway_url: str, *, api_key: str = API_KEY, api_version: str = V1BETA
) -> genai.Client:
    """google-genai SDK pointed at the gateway root (no /gemini prefix).

    Exercises v1.0.6's prefix_optional on gemini.generate_content and
    v1.0.7's x-goog-api-key discovery — a vanilla SDK with
    base_url=https://slipspace... and api_key=sk_live_... must reach
    /{api_version}/... with no extra config.
    """
    return genai.Client(
        api_key=api_key,
        http_options=types.HttpOptions(base_url=gateway_url, api_version=api_version),
    )


def test_generate_content_managed_bare_route(gateway_url: str, mockllm_url: str) -> None:
    """Same payload as test_generate_content_managed but via the bare route."""
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1beta/models/gemini-2.0-flash:generateContent",
        body={
            "candidates": [
                {
                    "content": {"parts": [{"text": "pong"}], "role": "model"},
                    "finishReason": "STOP",
                    "index": 0,
                    "safetyRatings": [],
                }
            ],
            "promptFeedback": {"safetyRatings": []},
            "usageMetadata": {
                "promptTokenCount": 1,
                "candidatesTokenCount": 1,
                "totalTokenCount": 2,
            },
            "modelVersion": "gemini-2.0-flash",
        },
    )

    client = _client_bare(gateway_url)
    resp = client.models.generate_content(
        model="gemini-2.0-flash",
        contents="ping",
    )

    assert resp.candidates[0].content.parts[0].text == "pong"
    assert resp.candidates[0].finish_reason == types.FinishReason.STOP


def test_generate_content_managed(gateway_url: str, mockllm_url: str) -> None:
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1beta/models/gemini-2.0-flash:generateContent",
        body={
            "candidates": [
                {
                    "content": {
                        "parts": [{"text": "pong"}],
                        "role": "model",
                    },
                    "finishReason": "STOP",
                    "index": 0,
                    "safetyRatings": [],
                }
            ],
            "promptFeedback": {"safetyRatings": []},
            "usageMetadata": {
                "promptTokenCount": 1,
                "candidatesTokenCount": 1,
                "totalTokenCount": 2,
            },
            "modelVersion": "gemini-2.0-flash",
        },
    )

    client = _client(gateway_url)
    resp = client.models.generate_content(
        model="gemini-2.0-flash",
        contents="ping",
    )

    assert resp.candidates[0].content.parts[0].text == "pong"
    assert resp.candidates[0].finish_reason == types.FinishReason.STOP


def test_generate_content_with_tools(gateway_url: str, mockllm_url: str) -> None:
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1beta/models/gemini-2.0-flash:generateContent",
        body={
            "candidates": [
                {
                    "content": {
                        "parts": [
                            {
                                "functionCall": {
                                    "name": "get_weather",
                                    "args": {"city": "Dublin"},
                                }
                            }
                        ],
                        "role": "model",
                    },
                    "finishReason": "STOP",
                    "index": 0,
                    "safetyRatings": [],
                }
            ],
            "usageMetadata": {
                "promptTokenCount": 1,
                "candidatesTokenCount": 1,
                "totalTokenCount": 2,
            },
            "modelVersion": "gemini-2.0-flash",
        },
    )

    client = _client(gateway_url)
    resp = client.models.generate_content(
        model="gemini-2.0-flash",
        contents="weather?",
        config=types.GenerateContentConfig(
            tools=[
                types.Tool(
                    function_declarations=[
                        types.FunctionDeclaration(
                            name="get_weather",
                            description="Get weather for a city",
                            parameters=types.Schema(
                                type=types.Type.OBJECT,
                                properties={"city": types.Schema(type=types.Type.STRING)},
                                required=["city"],
                            ),
                        )
                    ]
                )
            ],
        ),
    )

    parts = resp.candidates[0].content.parts
    assert parts[0].function_call is not None
    assert parts[0].function_call.name == "get_weather"
    assert dict(parts[0].function_call.args) == {"city": "Dublin"}


def test_list_models(gateway_url: str, mockllm_url: str) -> None:
    stage_response(
        mockllm_url,
        method="GET",
        path="/v1beta/models",
        body={
            "models": [
                {
                    "name": "models/gemini-2.0-flash",
                    "version": "001",
                    "displayName": "Gemini 2.0 Flash",
                    "description": "Fast multimodal model",
                    "inputTokenLimit": 1048576,
                    "outputTokenLimit": 8192,
                    "supportedGenerationMethods": ["generateContent", "streamGenerateContent"],
                },
                {
                    "name": "models/gemini-2.0-pro",
                    "version": "001",
                    "displayName": "Gemini 2.0 Pro",
                    "description": "High-quality multimodal model",
                    "inputTokenLimit": 1048576,
                    "outputTokenLimit": 8192,
                    "supportedGenerationMethods": ["generateContent", "streamGenerateContent"],
                },
            ]
        },
    )

    client = _client(gateway_url)
    names = [m.name for m in client.models.list()]
    assert "models/gemini-2.0-flash" in names
    assert "models/gemini-2.0-pro" in names


def test_managed_invalid_key(gateway_url: str, mockllm_url: str) -> None:
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1beta/models/gemini-2.0-flash:generateContent",
        body={},
    )
    client = _client(gateway_url, api_key="sk_not_real")
    with pytest.raises(genai.errors.APIError) as exc_info:
        client.models.generate_content(model="gemini-2.0-flash", contents="x")
    assert exc_info.value.code == 401


def test_passthrough_mode(gateway_url: str, mockllm_url: str) -> None:
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1beta/models/gemini-2.0-flash:generateContent",
        body={
            "candidates": [
                {
                    "content": {"parts": [{"text": "ok"}], "role": "model"},
                    "finishReason": "STOP",
                    "index": 0,
                }
            ],
            "modelVersion": "gemini-2.0-flash",
            "usageMetadata": {"promptTokenCount": 1, "candidatesTokenCount": 1, "totalTokenCount": 2},
        },
    )

    client = genai.Client(
        api_key="byok-token-xyz",
        http_options=types.HttpOptions(
            base_url=gateway_url,
            headers={
                "Authorization": "Bearer byok-token-xyz",
                "X-Slipspace-Configuration": "dev",
            },
            api_version=V1BETA,
        ),
    )
    resp = client.models.generate_content(model="gemini-2.0-flash", contents="passthrough")
    assert resp.candidates[0].content.parts[0].text == "ok"


def test_generate_content_api_version_v1(gateway_url: str, mockllm_url: str) -> None:
    """google-genai 3.0.0 default: POST /v1/models/{model}:generateContent.

    The canned response is staged on the v1 path only — if the gateway 404'd
    the route (pre-#631) or silently rewrote v1 to v1beta upstream, the mock
    would have nothing to serve and the SDK would raise.
    """
    stage_response(
        mockllm_url,
        method="POST",
        path="/v1/models/gemini-2.0-flash:generateContent",
        body={
            "candidates": [
                {
                    "content": {"parts": [{"text": "pong-v1"}], "role": "model"},
                    "finishReason": "STOP",
                    "index": 0,
                }
            ],
            "usageMetadata": {
                "promptTokenCount": 1,
                "candidatesTokenCount": 1,
                "totalTokenCount": 2,
            },
            "modelVersion": "gemini-2.0-flash",
        },
    )

    client = _client_bare(gateway_url, api_version=V1)
    resp = client.models.generate_content(model="gemini-2.0-flash", contents="ping")

    assert resp.candidates[0].content.parts[0].text == "pong-v1"
    assert resp.candidates[0].finish_reason == types.FinishReason.STOP


def test_list_models_api_version_v1(gateway_url: str, mockllm_url: str) -> None:
    """google-genai 3.0.0 default: GET /v1/models — the OpenAI listing path.

    The dev configuration binds the openai `models` family before gemini's, so
    the SDK's x-goog-api-key header is what must steer the request to the
    Gemini family. A Gemini-shaped body on the shared mock path proves the SDK
    parsed it as its own catalogue.
    """
    stage_response(
        mockllm_url,
        method="GET",
        path="/v1/models",
        body={
            "models": [
                {
                    "name": "models/gemini-2.0-flash",
                    "version": "001",
                    "displayName": "Gemini 2.0 Flash",
                    "supportedGenerationMethods": ["generateContent", "streamGenerateContent"],
                },
            ]
        },
    )

    client = _client(gateway_url, api_version=V1)
    names = [m.name for m in client.models.list()]
    assert names == ["models/gemini-2.0-flash"]
