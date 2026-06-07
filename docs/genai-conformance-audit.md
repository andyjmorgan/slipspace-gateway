# GenAI Semantic-Convention Conformance — v1.41.0

Status of the gateway's OpenTelemetry GenAI telemetry against the
**OpenTelemetry Semantic Conventions for Generative AI v1.41.0**
(`open-telemetry/semantic-conventions` tag `v1.41.0`; v1.41.1 is a
k8s-only patch with zero gen-ai changes, so v1.41.0 == latest for GenAI).

**Scope:** the GenAI **client** space for standard chat + content. The
gateway is a client of the upstream providers (OpenAI, Anthropic, Gemini).

**Verification:** independently audited against the pinned v1.41.0 spec by a
separate agent — **AGREE, conforms for the client chat/content space**, no
Required or Conditionally-Required element missing or mis-conditioned.

## Implemented

### Metrics (all 4 client metrics)
- `gen_ai.client.token.usage` (keyed by `gen_ai.token.type`)
- `gen_ai.client.operation.duration`
- `gen_ai.client.operation.time_to_first_chunk` — streaming only
- `gen_ai.client.operation.time_per_output_chunk` — per chunk after the first, via the proxy's per-flush `Observer.OnResponseChunk` hook

Attributes: `gen_ai.operation.name`, `gen_ai.provider.name`, `gen_ai.token.type`, `gen_ai.request.model`, `gen_ai.response.model`, `server.address`/`server.port`, `error.type`.

### Inference span (kind CLIENT, name `{operation.name} {request.model}`)
Every Required/CR/Recommended attribute: operation/provider/request.model, response id/model/finish_reasons, request params (temperature, top_p, top_k, max_tokens, frequency/presence penalty, stop_sequences, seed, choice.count, stream), output.type, conversation.id, agent.id (resolved from `X-Sluice-Agent-Id` / `X-Claude-Code-Agent-Id`; emitted when present), enduser.id (resolved from `X-Sluice-User-Id`; emitted when present — the GenAI semconv has no user attribute, so the end user rides the general `enduser` namespace), usage (input/output/cache_creation/cache_read/reasoning), response.time_to_first_chunk, server.address/port, error.type. Plus `openai.*` (request/response service_tier, system_fingerprint, api.type). The `openai.api.type` value comes from `cmd/gateway/handler.go::openAIAPIType`, which maps **only** the `chat` protocol to the well-known surface name `chat_completions`; every other protocol (`responses`, `embeddings`, …) passes through unchanged — there is no `responses`→`chat_completions` rewrite.

### Events (OTel logs pipeline)
- `gen_ai.client.operation.exception` — on every failure (status ≥ 400 or transport error).
- `gen_ai.client.inference.operation.details` — bounded prompt/response content as **structured** log values per the message JSON schema: `gen_ai.input.messages` / `gen_ai.output.messages` as `[{role, parts}]`, `gen_ai.system_instructions` as a bare parts array, `gen_ai.tool.definitions` normalised to `{type,name,description,parameters}`. Parts carry the well-known types `text` / `tool_call` / `tool_call_response`; media blocks pass through by type. Opt-in via `SLUICE_OTEL_CAPTURE_CONTENT` (default off), per-field capped, and credential-redacted (`internal/contentredact`). Emitted within the span context so records carry `trace_id`/`span_id`.

### Decisions (all agent-validated)
- **Client vantage** (not server) — the gateway times its outbound call; it doesn't generate tokens.
- `gen_ai.provider.name`: `gemini`→`gcp.gemini`; openai/anthropic unchanged.
- `gen_ai.operation.name` = `generate_content` for Gemini (not `chat`).
- `server.address`/`port` = the **upstream provider** host:port.
- `gen_ai.usage.input_tokens` is cache-inclusive (Anthropic page's computation); cache tokens emitted as `gen_ai.usage.cache_*.input_tokens` attributes (no cache *metric* exists in the spec).
- Bounded content (latest user turn, not full history) — spec permits truncation; full content stays in the connector spool (invariant #4). Within that bound the turn keeps its real parts (multimodal blocks by type, tool calls, tool results) rather than being flattened to one text part.
- System instructions are multi-source: all system/developer messages (OpenAI chat + Responses `input[]`) and every Anthropic `system` block, not just the first.
- Inbound W3C trace context extracted (gateway spans nest under a caller's trace); outbound, the operation-detail/exception log events are emitted under the request span so they correlate to it natively.

## Deliberately not done

- **Dropping `sluice.requests.total` and the `sluice.tokens.cache*` counters.** These are spec-legal additive extras under the `sluice.*` namespace, and they back the admin console's request/cache aggregation (the console reads metrics, not spans/events). The cache *data* also rides the `gen_ai.usage.cache_*` span/event attributes, but those are per-request, not aggregatable in the console — so dropping the counters would regress the console for zero conformance benefit. Kept.
- **OTLP-export integration test.** The export byte path is the OTel SDK's responsibility (upstream-tested); our emission logic is covered by in-memory recorders, the pipeline wiring is unit-tested, and `make e2e` covers the real binary.
- **Cosmetic:** the internal identifiers have since been renamed to match the wire string — `MetricTimeToFirstChunk` / `TimeToFirstChunk` (`internal/observability/meters.go`), emitting `gen_ai.client.operation.time_to_first_chunk`. The earlier "first byte" identifier names are gone.

## Out of scope (correctly not emitted)
Embeddings-specific attrs; agent/tool/workflow spans; `gen_ai.server.*` metrics (the gateway is a client, not the model server).
