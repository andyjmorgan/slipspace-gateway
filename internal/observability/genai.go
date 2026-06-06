package observability

// OpenTelemetry GenAI semantic-convention attribute keys, operation
// names, and token types, plus the Sluice-namespaced extras that ride
// alongside them.
//
// Naming discipline (load-bearing — see the design note "OTel GenAI
// Conformance"): the gen_ai.* namespace is owned by the OTel spec. We
// only ever emit keys and enum values the spec defines; we never mint a
// new key or value inside gen_ai.*. Everything Sluice-specific lives
// under sluice.*; where another stable OTel convention already models a
// dimension (http.*, server.*) we reuse it rather than inventing.
//
// These keys are declared as our own constants because the gen_ai.*
// namespace is still Development-status in the spec and the generated Go
// semconv module lags it. We track the latest revision and adopt changes
// on each dependency bump; a rename here moves in lockstep with any
// downstream Collector routing or dashboard queries that reference it.

// GenAI span/metric attribute keys (spec-defined — do not extend).
const (
	// AttrGenAIOperationName is the coarse operation type (chat,
	// embeddings, ...). Required on inference spans and server metrics.
	AttrGenAIOperationName = "gen_ai.operation.name"

	// AttrGenAIProviderName is the upstream provider (openai, anthropic,
	// gcp.gemini). Required; replaces Sluice's former "provider" label.
	AttrGenAIProviderName = "gen_ai.provider.name"

	// AttrGenAIRequestModel is the model named on the inbound request.
	AttrGenAIRequestModel = "gen_ai.request.model"

	// AttrGenAIResponseModel is the model the provider reported serving.
	AttrGenAIResponseModel = "gen_ai.response.model"

	// AttrGenAIResponseID and AttrGenAIResponseFinishReasons are Recommended
	// response descriptors parsed from the provider response body/stream.
	AttrGenAIResponseID            = "gen_ai.response.id"
	AttrGenAIResponseFinishReasons = "gen_ai.response.finish_reasons"

	// AttrGenAITokenType keys the gen_ai.client.token.usage histogram by
	// direction. The spec enum is input|output only — cache tokens have
	// no value here and ride sluice.* counters instead.
	AttrGenAITokenType = "gen_ai.token.type" //nolint:gosec // G101 false positive: OTel attribute key, not a credential

	// AttrGenAIConversationID is the spec home for a session/conversation
	// identifier. Populated by the session-bundling work; declared here so
	// the span builder has a single key reference once that lands.
	AttrGenAIConversationID = "gen_ai.conversation.id"

	// AttrGenAIUsageInputTokens and AttrGenAIUsageOutputTokens are the
	// spec gen_ai.usage.* span attributes carrying per-request token
	// counts. Span-only — the metric form is the token-usage histogram.
	AttrGenAIUsageInputTokens  = "gen_ai.usage.input_tokens"  //nolint:gosec // G101 false positive: attribute key, not a credential
	AttrGenAIUsageOutputTokens = "gen_ai.usage.output_tokens" //nolint:gosec // G101 false positive: attribute key, not a credential

	// AttrGenAIUsageCacheCreationInputTokens and
	// AttrGenAIUsageCacheReadInputTokens are the spec span/event attributes
	// for provider-managed prompt-cache tokens (defined on the GenAI
	// Anthropic page). gen_ai.usage.input_tokens is the cache-inclusive
	// total. The spec has no cache metric — cache counts live as these
	// attributes; the former sluice.tokens.cache* counters are dropped in
	// favour of them (conformance: our overlap with OTel gives way to OTel).
	// Billing aggregation continues to ride the connector Record, not a
	// meter (invariant #4).
	AttrGenAIUsageCacheCreationInputTokens = "gen_ai.usage.cache_creation.input_tokens" //nolint:gosec // G101 false positive: attribute key, not a credential
	AttrGenAIUsageCacheReadInputTokens     = "gen_ai.usage.cache_read.input_tokens"     //nolint:gosec // G101 false positive: attribute key, not a credential

	// AttrGenAIUsageReasoningOutputTokens is the Recommended count of
	// reasoning/thinking tokens, emitted when the provider reports it
	// (OpenAI reasoning_tokens, Gemini thoughtsTokenCount).
	AttrGenAIUsageReasoningOutputTokens = "gen_ai.usage.reasoning.output_tokens" //nolint:gosec // G101 false positive: attribute key, not a credential

	// AttrGenAIRequestStream marks whether the request used streaming.
	// Conditionally required by the spec when the request is streaming.
	AttrGenAIRequestStream = "gen_ai.request.stream"

	// AttrGenAIResponseTimeToFirstChunk is the span-side companion to the
	// time_to_first_chunk metric: wire time (seconds) to the first response
	// chunk. Recommended on streaming requests only.
	AttrGenAIResponseTimeToFirstChunk = "gen_ai.response.time_to_first_chunk"

	// Request sampling parameters parsed from the inbound body. The penalty,
	// temperature, top_p, top_k, max_tokens, and stop_sequences attributes
	// are Recommended; choice.count is Conditionally Required (present and
	// != 1); seed is Conditionally Required (present); output.type is
	// Conditionally Required (when an output format is requested).
	AttrGenAIRequestTemperature      = "gen_ai.request.temperature"
	AttrGenAIRequestTopP             = "gen_ai.request.top_p"
	AttrGenAIRequestTopK             = "gen_ai.request.top_k"
	AttrGenAIRequestMaxTokens        = "gen_ai.request.max_tokens" //nolint:gosec // G101 false positive: attribute key, not a credential
	AttrGenAIRequestFrequencyPenalty = "gen_ai.request.frequency_penalty"
	AttrGenAIRequestPresencePenalty  = "gen_ai.request.presence_penalty"
	AttrGenAIRequestStopSequences    = "gen_ai.request.stop_sequences"
	AttrGenAIRequestSeed             = "gen_ai.request.seed"
	AttrGenAIRequestChoiceCount      = "gen_ai.request.choice.count"
	AttrGenAIOutputType              = "gen_ai.output.type"
)

// Reused stable OTel conventions from namespaces other than gen_ai.*.
const (
	// AttrErrorType is the OTel general error-type attribute, set on the
	// failure path. error.type is an open enum, so the HTTP status string
	// is a spec-legal value.
	AttrErrorType = "error.type"

	// AttrHTTPResponseStatusCode is the stable HTTP-semconv status code,
	// retained alongside error.type so dashboards keep per-status
	// granularity (429 vs 500 vs 503) that the coarse error.type drops.
	AttrHTTPResponseStatusCode = "http.response.status_code"

	// AttrServerAddress is the stable server.address attribute naming the
	// upstream host the request was forwarded to.
	AttrServerAddress = "server.address"

	// AttrServerPort is the stable server.port attribute. The GenAI spec
	// makes it conditionally required whenever server.address is set.
	AttrServerPort = "server.port"
)

// OpenAI-specific attributes (openai.* namespace), emitted only on OpenAI
// operations. service_tier is Conditionally Required (request: present and
// not "auto"; response: present); system_fingerprint and api.type are
// Recommended. Span-only — system_fingerprint changes with the serving
// provider and is too high-cardinality for a metric label, so the openai.*
// set rides the span, not the meters.
const (
	AttrOpenAIRequestServiceTier        = "openai.request.service_tier"
	AttrOpenAIResponseServiceTier       = "openai.response.service_tier"
	AttrOpenAIResponseSystemFingerprint = "openai.response.system_fingerprint"
	AttrOpenAIAPIType                   = "openai.api.type"
)

// GenAI event (log record) names and the exception attributes they carry.
// Events ride the OTel logs signal; the operation-details event is the
// structured carrier for bounded prompt/response content, the exception
// event is the spec's error-recording mechanism.
const (
	EventInferenceDetails   = "gen_ai.client.inference.operation.details"
	EventOperationException = "gen_ai.client.operation.exception"

	AttrExceptionType    = "exception.type"
	AttrExceptionMessage = "exception.message"
)

// Opt-In content attributes carried on the operation-details event. The
// gateway emits a bounded subset (latest user turn, model response, system
// instructions, tool definitions); the full content lives in the connector
// spool. Gated by SLUICE_OTEL_CAPTURE_CONTENT.
const (
	AttrGenAIInputMessages      = "gen_ai.input.messages"
	AttrGenAIOutputMessages     = "gen_ai.output.messages"
	AttrGenAISystemInstructions = "gen_ai.system_instructions"
	AttrGenAIToolDefinitions    = "gen_ai.tool.definitions"
)

// Well-known message-part type discriminators from the GenAI message JSON
// schema. Text and pass-through media parts carry "content"; tool parts carry
// the call/response fields instead.
const (
	PartTypeText             = "text"
	PartTypeToolCall         = "tool_call"
	PartTypeToolCallResponse = "tool_call_response"

	// PartTypeReasoning is the semconv message part type for a model's
	// reasoning/thinking trace (Anthropic thinking + redacted_thinking
	// blocks). Redacted thinking carries no recoverable text, so its part is
	// emitted with empty content — the type alone signals its presence.
	PartTypeReasoning = "reasoning"
)

// Sluice-namespaced extras — dimensions the GenAI spec has no concept for.
const (
	// AttrSluiceProtocol is the precise resolved protocol (chat, messages,
	// generate_content) or passthrough family, retained beside the coarse
	// gen_ai.operation.name so the console keeps its per-protocol breakdown.
	AttrSluiceProtocol = "sluice.protocol"

	// AttrSluiceConfiguration is the resolved Sluice configuration name.
	AttrSluiceConfiguration = "sluice.configuration"

	// AttrSluiceCorrelationID carries the request correlation id on the
	// span so a trace can be cross-referenced to logs and captured
	// records (which key on the same id).
	AttrSluiceCorrelationID = "sluice.correlation_id"

	// AttrSluiceResilienceTarget and AttrSluiceResilienceOutcome label a
	// per-attempt child span with the resilience target name and the
	// attempt outcome (success, failure_status, transport_error,
	// cb_blocked).
	AttrSluiceResilienceTarget  = "sluice.resilience.target"
	AttrSluiceResilienceOutcome = "sluice.resilience.outcome"

	// AttrSluiceUnmappedField is the dotted JSON path of a provider field
	// this build does not model — the per-field dimension on
	// gateway.unmapped_fields.total. Cardinality is bounded by the provider
	// API surface (tens of field names), not by request volume.
	AttrSluiceUnmappedField = "sluice.unmapped_field"

	// AttrSluiceUnmappedDirection is "request" or "response", marking which
	// side of the exchange carried the unmapped field.
	AttrSluiceUnmappedDirection = "sluice.unmapped_direction"
)

// gen_ai.operation.name values (spec-defined).
const (
	OperationChat            = "chat"
	OperationGenerateContent = "generate_content"
	OperationEmbeddings      = "embeddings"
)

// gen_ai.token.type values (spec-defined).
const (
	TokenTypeInput  = "input"
	TokenTypeOutput = "output"
)

// OperationNameForProtocol maps a Sluice protocol key to the
// gen_ai.operation.name spec value: OpenAI/Anthropic chat surfaces and the
// OpenAI responses API to "chat", Gemini's generate_content to the dedicated
// "generate_content" value, embeddings to "embeddings". Protocols the spec
// has no operation for (e.g. models listing) fall through to their own key —
// the precise route is always also emitted as sluice.protocol, so nothing is
// lost.
func OperationNameForProtocol(protocol string) string {
	switch protocol {
	case "chat_completions", "chat", "messages", "responses":
		return OperationChat
	case "generate_content":
		return OperationGenerateContent
	case "embeddings":
		return OperationEmbeddings
	default:
		return protocol
	}
}

// GenAIProviderName maps a Sluice internal provider key to the OTel
// gen_ai.provider.name enum value. "openai" and "anthropic" already match the
// enum; "gemini" maps to the spec value "gcp.gemini". Unknown providers pass
// through verbatim so a newly added provider surfaces rather than being
// silently rewritten.
func GenAIProviderName(provider string) string {
	switch provider {
	case "gemini":
		return "gcp.gemini"
	default:
		return provider
	}
}

// TokenUsageBuckets are the advisory explicit bucket boundaries the spec
// recommends for gen_ai.client.token.usage. Token counts span a wide
// range (a few tokens to multi-million-token contexts), so the buckets
// escalate by powers of four. Defined as a package var because Go has no
// composite-literal constants; read-only after init.
var TokenUsageBuckets = []float64{
	1,
	4,
	16,
	64,
	256,
	1024,
	4096,
	16384,
	65536,
	262144,
	1048576,
	4194304,
	16777216,
	67108864,
}
