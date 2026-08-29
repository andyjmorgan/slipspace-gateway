# E2E tests

E2E tests are **the spec**, not a nice-to-have. They are black-box: build the `gateway` binary, spin up the mock LLM via subprocess, hit the gateway over real HTTP, and assert on what a real client (and the configured connectors) actually see. **A feature is not done without an E2E case proving it works through the binary.**

Run with `make e2e` (build tag `e2e`; Docker required for the integration containers — SeaweedFS (S3), Azurite (Azure Blob), and Postgres (the arbiter suite, `test/e2e/arbiter/shared_postgres_test.go`)).

## Harness

`test/e2e/harness/` owns process lifecycle. `New(t)` delegates to `NewWithOptions(t, Options{})`, which starts the in-process capture server, the mock LLM, and the gateway, then registers cleanup:

```go
func New(t *testing.T) *Harness {
    t.Helper()
    return NewWithOptions(t, Options{})
}

// NewWithOptions (harness.go:108) — abridged:
h.startCaptureServer()
h.startMockLLM(t, repoRoot)
h.startGateway(t, repoRoot) // allocates the per-test temp spool dir
t.Cleanup(h.Stop)
```

The mock is wired into the gateway by materializing a per-test copy of `config-dev/` into a temp dir (`os.MkdirTemp("", "slipspace-e2e-config-*")`, harness.go:483) and string-replacing the compose alias `mockllm:5555` with the live mock's address (harness.go:521) — there is no `gatewayConfig` struct and no `ProvidersUpstream` field. The harness exposes the running addresses as `h.MockLLMURL` and `h.GatewayURL`.

Test helpers:

- `(*Harness).PostJSON(path, body, headers)` — synchronous JSON request.
- `(*Harness).PostStream(path, body, headers)` — returns an SSE reader.
- `(*Harness).Get(path, headers)` — synchronous GET request.
- `(*Harness).ExpectEvent(subject, timeout)` — poll for a captured connector record matching `subject`.
- `(*Harness).ExpectNoEvent(subject, window)` — assert no matching record arrives within `window`.

Mockllm control (`harness/mockllm.go`):

- `(*Harness).StageMockResponse(c)` / `(*Harness).ResetMockResponses()` — stage and clear canned upstream responses.
- `(*Harness).FetchMockState()` / `(*Harness).MockRequestCount()` — read the mock's staged state and request counter.
- `(*Harness).FetchCapturedRequests()` / `(*Harness).LastCapturedRequest()` — inspect what the gateway forwarded upstream.

Process + endpoints (`harness/harness.go`):

- `h.GatewayURL` / `h.MockLLMURL` — the running addresses (struct fields); `(*Harness).PromURL()` — base URL of the gateway's Prometheus scrape endpoint.
- `(*Harness).SendGatewaySignal(sig)` / `(*Harness).WaitGatewayExit(timeout)` / `(*Harness).Stop()` — signal, await, and tear down the spawned gateway.

The harness runs its own in-process capture server and translates each connector `Record` into an event via `emitRecord` — there is no standalone `WebhookReceiver(t)`, `ReadSealedRecords`, or `ExpectRecord`. Beyond `New(t)`, `NewWithOptions(t, opts)` constructs a harness with non-default `Options`. For session-scoped mockllm staging, `(*Harness).NewSession(t)` returns a `Session` with `Stage`, `Post`, `PostStream`, and `Captured`.

Tests reading captured records **sort by `(ts_ns, instance_id, seq)`, never receive order** — see load-bearing invariant #8 in `CLAUDE.md`.

## The matrix

The grid below is the **coverage target**, not an enumerated cross product — the suite samples it (204 e2e test functions today) rather than running every cell:

- `(provider, endpoint)` ∈ {`openai.chat_completions`, `openai.responses`, `openai.models`, `anthropic.messages`, `anthropic.models`, `gemini.generate_content`, `gemini.models`}
- variant ∈ {`streaming`, `non-streaming`, `success`, `error_4xx`, `error_5xx`, `malformed_response`, `slow_response`, `client_disconnect_mid_stream`}
- auth ∈ {`managed_valid`, `managed_invalid`, `managed_disabled`, `passthrough_valid`, `passthrough_unknown_config`}

Each variant is covered by a representative case rather than once per `(provider, endpoint)`: `errors/errors_test.go` owns `error_4xx` / `error_5xx` / `malformed_response`, and `streaming/streaming_test.go` owns `slow_response` and `client_disconnect_mid_stream`. `managed_disabled` has **no** black-box case — the config-dev fixture ships no disabled key, so `auth/auth_test.go` approximates it with an absent `Authorization` header and the real disabled-key assertion lives in the in-process `cmd/gateway/gateway_test.go`.

Assert:

- HTTP response status correct.
- Response body shape correct — round-trip via the typed `protocols/` packages (`protocols/openai`, `protocols/anthropic`, `protocols/gemini`).
- Captured connector record on configured `connectors:` matches the **post-rule** labels and the captured body envelope.
- **No** record when the configuration has no `connector_bindings`, or when sampling/filter excludes the request.
- `X-Slipspace-Correlation-Id` set on response.
- `X-Slipspace-Session-Id` echoed when sent.

## Wire-compat (Python SDK)

`test/python/` exercises the official OpenAI, Anthropic, and Google Gemini SDKs against the gateway (`make py-compat`). Failures here are tagged "wire compatibility regression" — a different class of bug from internal unit-test failures, and a **release blocker**.
