# E2E tests

E2E tests are **the spec**, not a nice-to-have. They are black-box: build the `gateway` binary, spin up the mock LLM via subprocess, hit the gateway over real HTTP, and assert on what a real client (and the configured connectors) actually see. **A feature is not done without an E2E case proving it works through the binary.**

Run with `make e2e` (build tag `e2e`; Docker required for the connector integration containers — MinIO, Azurite).

## Harness

`test/e2e/harness/` owns process lifecycle. `New(t)` allocates a per-test temp spool dir, starts the mock LLM, starts the gateway pointed at it, and registers cleanup:

```go
func New(t *testing.T) *Harness {
    h := &Harness{}
    h.SpoolRoot = t.TempDir()
    h.MockLLM = startMockLLM(t)
    h.Gateway = startGateway(t, gatewayConfig{
        ProvidersUpstream: h.MockLLM.URL,
        SpoolRoot:         h.SpoolRoot,
    })
    t.Cleanup(h.Stop)
    return h
}
```

Test helpers:

- `PostJSON(t, path, body, headers)` — synchronous JSON request.
- `PostStream(t, path, body, headers)` — returns an SSE reader.
- `ReadSealedRecords(t, connector)` — decompress + ndjson-parse the connector's sealed segments.
- `ExpectRecord(t, connector, predicate, timeout)` — poll for a record matching a predicate.
- `WebhookReceiver(t)` — spin up a local `httptest.Server` that captures POSTs for assertion.

Tests reading captured records **sort by `(ts_ns, instance_id, seq)`, never receive order** — see load-bearing invariant #8 in `CLAUDE.md`.

## The matrix

For each combination of:

- `(provider, endpoint)` ∈ {`openai.chat_completions`, `openai.responses`, `openai.models`, `anthropic.messages`, `anthropic.models`, `gemini.generate_content`, `gemini.models`}
- variant ∈ {`streaming`, `non-streaming`, `success`, `error_4xx`, `error_5xx`, `malformed_response`, `slow_response`, `client_disconnect_mid_stream`}
- auth ∈ {`managed_valid`, `managed_invalid`, `managed_disabled`, `passthrough_valid`, `passthrough_unknown_config`}

Assert:

- HTTP response status correct.
- Response body shape correct — round-trip via the typed `providers` package.
- Captured connector record on configured `connectors:` matches the **post-rule** labels and the captured body envelope.
- **No** record when the configuration has no `connector_bindings`, or when sampling/filter excludes the request.
- `X-Sluice-Correlation-Id` set on response.
- `X-Sluice-Session-Id` echoed when sent.

## Wire-compat (Python SDK)

`test/python/` exercises the official OpenAI, Anthropic, and Google Gemini SDKs against the gateway (`make py-compat`). Failures here are tagged "wire compatibility regression" — a different class of bug from internal unit-test failures, and a **release blocker**.
