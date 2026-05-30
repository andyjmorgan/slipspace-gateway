"""Smoke test: Gemini generateContent via sluice-gateway managed mode.

Parametrized over both surface forms (v1.0.6) and both inbound auth headers
(v1.0.7):
  - prefixed:  POST /gemini/v1beta/models/{model}:generateContent  (namespaced)
  - bare:      POST /v1beta/models/{model}:generateContent         (vanilla SDK)
  - bearer:    Authorization: Bearer sk_live_...   (Sluice's historical signal)
  - native:    x-goog-api-key: sk_live_...         (vanilla google-genai default)

Together: 4 cases per smoke run. All must succeed on the live gateway.
"""

from __future__ import annotations

import pytest
from google import genai
from google.genai import types


@pytest.mark.parametrize(
    "auth_header",
    ["bearer", "native-x-goog-api-key"],
)
def test_gemini_generate_content(
    base_url: str, api_key: str, auth_header: str
) -> None:
    # v1.0.7: vanilla google-genai client with just api_key= now works
    # because sluice discovers the Sluice secret from `x-goog-api-key`. The
    # "bearer" variant exercises the historical Authorization path.
    extra_headers = (
        {"Authorization": f"Bearer {api_key}"} if auth_header == "bearer" else {}
    )
    client = genai.Client(
        api_key=api_key,
        http_options=types.HttpOptions(
            base_url=base_url,
            headers=extra_headers,
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
