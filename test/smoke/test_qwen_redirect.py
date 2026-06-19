"""Smoke test: cluster-side qwen model routing via v2 bindings.

The prod cluster's config binds the coder model off the openai chat surface
to a local Ollama-backed resilience group:

- `qwen2.5-coder:7b` → `qwen-load-balance` group (qwen-ollama in-cluster +
  qwen-ollama-standalone at `192.168.69.21:11434`)

This binding lives ONLY on the cluster (it doesn't ship in `config-dev/`),
so the test skips cleanly when the harness isn't pointed at a deploy known
to carry it. Set `SLIPSPACE_SMOKE_QWEN=true` to enable.
"""

from __future__ import annotations

import os

import openai
import pytest


@pytest.fixture(scope="session")
def qwen_enabled() -> bool:
    return os.environ.get("SLIPSPACE_SMOKE_QWEN", "").lower() in {"true", "1", "yes"}


QWEN_MODELS = [
    pytest.param("qwen2.5-coder:7b", id="ollama-loadbalance"),
]


@pytest.mark.parametrize("model", QWEN_MODELS)
def test_qwen_changeprovider_redirect(
    base_url: str,
    api_key: str,
    qwen_enabled: bool,
    model: str,
) -> None:
    """Qwen model on the openai chat surface → v2 binding routes to the local group.

    Asserts the response carries the qwen model name (NOT an openai model),
    proving the (chat, qwen2.5-coder:7b) binding selected the qwen-load-balance
    group and the gateway hit the private qwen backend rather than OpenAI.
    """
    if not qwen_enabled:
        pytest.skip("SLIPSPACE_SMOKE_QWEN not set — cluster-specific rule check disabled")

    client = openai.OpenAI(
        base_url=f"{base_url}/v1",
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
