"""Smoke test: Gemini generateContent via sluice-gateway managed mode.

Parametrized over both surface forms (v1.0.6):
  - prefixed:  POST /gemini/v1beta/models/{model}:generateContent  (namespaced)
  - bare:      POST /v1beta/models/{model}:generateContent         (vanilla SDK)

Both must succeed on the live gateway.
"""

from __future__ import annotations

import pytest
from google import genai
from google.genai import types


@pytest.mark.parametrize(
    "prefix",
    ["/gemini", ""],
    ids=["prefixed", "bare-prefix-optional"],
)
def test_gemini_generate_content(base_url: str, api_key: str, prefix: str) -> None:
    # google-genai ships `x-goog-api-key`; sluice keys managed auth off the
    # Authorization bearer, so we inject it via HttpOptions.headers.
    client = genai.Client(
        api_key=api_key,
        http_options=types.HttpOptions(
            base_url=f"{base_url}{prefix}",
            headers={"Authorization": f"Bearer {api_key}"},
        ),
    )
    resp = client.models.generate_content(
        model="gemini-2.5-flash",
        contents="Reply with exactly one word: pong",
        config=types.GenerateContentConfig(max_output_tokens=32),
    )

    assert resp.candidates, "no candidates returned"
    cand = resp.candidates[0]
    assert cand.content and cand.content.parts, "no content parts returned"
    assert cand.content.parts[0].text and cand.content.parts[0].text.strip()
    assert cand.finish_reason in {
        types.FinishReason.STOP,
        types.FinishReason.MAX_TOKENS,
    }
    assert resp.usage_metadata and resp.usage_metadata.total_token_count > 0
