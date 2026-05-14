"""Wire-compat tests for the official `google-genai` Python SDK against sluice-gateway.

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


def _client(
    gateway_url: str,
    *,
    api_key: str = API_KEY,
    extra_headers: dict[str, str] | None = None,
) -> genai.Client:
    """Build a Gemini client that talks Authorization-bearer to sluice.

    The google-genai SDK only sends `x-goog-api-key` by default. Sluice's
    managed-auth path keys off `Authorization: Bearer ...`, so we inject
    it via HttpOptions.headers.
    """
    headers = {"Authorization": f"Bearer {api_key}"}
    if extra_headers:
        headers.update(extra_headers)
    return genai.Client(
        api_key=api_key,
        http_options=types.HttpOptions(
            base_url=f"{gateway_url}/gemini",
            headers=headers,
        ),
    )


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
            base_url=f"{gateway_url}/gemini",
            headers={
                "Authorization": "Bearer byok-token-xyz",
                "X-Sluice-Configuration": "dev",
            },
        ),
    )
    resp = client.models.generate_content(model="gemini-2.0-flash", contents="passthrough")
    assert resp.candidates[0].content.parts[0].text == "ok"
