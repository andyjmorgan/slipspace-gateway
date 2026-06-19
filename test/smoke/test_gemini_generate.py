"""Smoke test: Gemini generateContent via slipspace-gateway managed mode.

Parametrized over both surface forms (v1.0.6) and both inbound auth headers
(v1.0.7):
  - prefixed:  POST /gemini/v1beta/models/{model}:generateContent  (namespaced)
  - bare:      POST /v1beta/models/{model}:generateContent         (vanilla SDK)
  - bearer:    Authorization: Bearer sk_live_...   (SlipSpace's historical signal)
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
    # because slipspace discovers the SlipSpace secret from `x-goog-api-key`. The
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
        # gemini-2.5-flash is a thinking model — reasoning tokens count against
        # the budget, so a tiny cap (e.g. 32) intermittently finishes MAX_TOKENS
        # with no text part. Give it real headroom for a one-word reply.
        config=types.GenerateContentConfig(max_output_tokens=512),
    )

    assert resp.candidates, "no candidates returned"
    cand = resp.candidates[0]
    assert cand.finish_reason in {
        types.FinishReason.STOP,
        types.FinishReason.MAX_TOKENS,
    }
    assert resp.usage_metadata and resp.usage_metadata.total_token_count > 0

    # gemini-2.5-flash is a thinking model: even with real headroom it can
    # occasionally spend the whole output budget on reasoning and return
    # MAX_TOKENS with no visible text part (#117). That's a model-side budget
    # outcome, not a gateway fault — the candidate, finish_reason, and usage
    # asserted above already prove the gateway forwarded and round-tripped the
    # request. Only demand a real text part when the model stopped on its own;
    # skip the content check on the empty-MAX_TOKENS path rather than flaking
    # the whole smoke run.
    if cand.finish_reason == types.FinishReason.MAX_TOKENS and not (
        cand.content and cand.content.parts
    ):
        pytest.skip(
            "gemini hit MAX_TOKENS before emitting a text part (reasoning budget); "
            "gateway path already verified"
        )

    assert cand.content and cand.content.parts, "no content parts returned"
    assert cand.content.parts[0].text and cand.content.parts[0].text.strip()
