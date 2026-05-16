"""Smoke test: cluster-side `changeProvider` rules redirecting qwen models.

The prod cluster's `policy.yaml` carries two model-keyed rules added during
the v1.0.1 cycle that route qwen models off the openai surface to local
vLLM / Ollama backends:

- `qwen2.5-coder:7b` → `qwen-ollama` provider at `192.168.69.21:11434`
- `qwen3-coder` → `qwen-vllm` provider at the in-cluster vLLM Service

These rules live ONLY on the cluster (they don't ship in `config-dev/`),
so the tests skip cleanly when the harness isn't pointed at a deploy
known to carry them. Set `SLUICE_SMOKE_QWEN=true` to enable.
"""

from __future__ import annotations

import os

import openai
import pytest


@pytest.fixture(scope="session")
def qwen_enabled() -> bool:
    return os.environ.get("SLUICE_SMOKE_QWEN", "").lower() in {"true", "1", "yes"}


QWEN_MODELS = [
    pytest.param("qwen2.5-coder:7b", id="ollama"),
    pytest.param("qwen3-coder", id="vllm"),
]


@pytest.mark.parametrize("model", QWEN_MODELS)
def test_qwen_changeprovider_redirect(
    base_url: str,
    api_key: str,
    qwen_enabled: bool,
    model: str,
) -> None:
    """Qwen model name on the openai chat surface → cluster rule redirects upstream.

    Asserts the response carries the qwen model name (NOT an openai model),
    proving the changeProvider rule fired and the gateway hit the private
    qwen backend rather than OpenAI's servers.
    """
    if not qwen_enabled:
        pytest.skip("SLUICE_SMOKE_QWEN not set — cluster-specific rule check disabled")

    client = openai.OpenAI(
        base_url=f"{base_url}/openai/v1",
        api_key=api_key,
        max_retries=0,
        timeout=30,
    )
    resp = client.chat.completions.create(
        model=model,
        messages=[{"role": "user", "content": "Reply with exactly one word: pong"}],
        max_tokens=8,
        temperature=0,
    )

    assert resp.id
    assert resp.model == model, (
        f"response model = {resp.model!r}, want {model!r} — "
        "redirect rule didn't fire or hit the wrong upstream"
    )
    assert resp.choices
    msg = resp.choices[0].message
    assert msg.role == "assistant"
    assert msg.content and msg.content.strip()
