package observability

// OpenTelemetry GenAI semantic-convention attribute keys, operation
// names, and token types, plus the SlipSpace-namespaced extras that ride
// alongside them.
//
// Naming discipline (load-bearing — see the design note "OTel GenAI
// Conformance"): the gen_ai.* namespace is owned by the OTel spec. We
// only ever emit keys and enum values the spec defines; we never mint a
// new key or value inside gen_ai.*. Everything SlipSpace-specific lives
// under slipspace.*; where another stable OTel convention already models a
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
	// gcp.gemini). Required; replaces SlipSpace's former "provider" label.
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
	// no value here and ride slipspace.* counters instead.
	AttrGenAITokenType = "gen_ai.token.type" //nolint:gosec // G101 false positive: OTel attribute key, not a credential

	// AttrGenAIConversationID is the spec home for a session/conversation
	// identifier. The semconv defines it as "the unique identifier for a
	// conversation (session, thread)", so it carries the most-specific thread a
	// request belongs to: the session for a main agent, or a distinct subagent
	// thread when one is active.
	AttrGenAIConversationID = "gen_ai.conversation.id"

	// AttrSlipSpaceSessionID carries the session bundle root — the stable id that
	// groups every request of a conversation, including all of its subagent
	// threads. The semconv has no session-vs-thread distinction (conversation.id
	// covers both) and no parent/child concept, so the root rides this slipspace.*
	// attribute; the Arbiter projects it to request_events.session_id
	// for top-down bundling. For a main agent it equals gen_ai.conversation.id.
	AttrSlipSpaceSessionID = "slipspace.session_id"

	// AttrSlipSpaceParentConversationID carries the parent of a subagent thread —
	// the hierarchy edge the semconv has no home for. Set only when the resolved
	// conversation is a subagent thread (distinct from the session); empty for a
	// main agent. Codex supplies it explicitly via X-Codex-Parent-Thread-Id.
	AttrSlipSpaceParentConversationID = "slipspace.parent_conversation_id"

	// AttrGenAIAgentID is the spec home for the GenAI agent identifier (see
	// the GenAI agent-spans convention). SlipSpace resolves it from
	// X-Slipspace-Agent-Id (or a configured fallback such as
	// X-Claude-Code-Agent-Id) and stamps it on the request span and the
	// operation-details event — never on a metric label (unbounded
	// cardinality), mirroring gen_ai.conversation.id.
	AttrGenAIAgentID = "gen_ai.agent.id"

	// AttrEnduserID is the end-user identifier. The GenAI semconv defines no
	// user attribute (only gen_ai.conversation.id and gen_ai.agent.id), so the
	// end user rides the general-purpose `enduser` namespace (enduser.id,
	// recently un-deprecated). SlipSpace resolves it from X-Slipspace-User-Id (or a
	// configured fallback) and stamps it on the request span and the
	// operation-details event — never on a metric label (unbounded
	// cardinality), mirroring gen_ai.conversation.id / gen_ai.agent.id.
	AttrEnduserID = "enduser.id"

	// AttrGenAIUsageInputTokens and AttrGenAIUsageOutputTokens are the
	// spec gen_ai.usage.* span attributes carrying per-request token
	// counts. Span-only — the metric form is the token-usage histogram.
	AttrGenAIUsageInputTokens  = "gen_ai.usage.input_tokens"  //nolint:gosec // G101 false positive: attribute key, not a credential
	AttrGenAIUsageOutputTokens = "gen_ai.usage.output_tokens" //nolint:gosec // G101 false positive: attribute key, not a credential

	// AttrGenAIUsageCacheCreationInputTokens and
	// AttrGenAIUsageCacheReadInputTokens are the spec span/event attributes
	// for provider-managed prompt-cache tokens (defined on the GenAI
	// Anthropic page). gen_ai.usage.input_tokens is the cache-inclusive
	// total. The spec has no cache metric, so on the span these counts
	// travel as attributes rather than as a gen_ai meter. That is a
	// separate channel from the slipspace.tokens.cached.total /
	// slipspace.tokens.cache_creation.total counters, which are still
	// declared, registered, and emitted (see meters.go) and which the
	// admin dashboard's cache panel reads — do not remove them. Billing
	// aggregation rides the connector Record, not a meter (invariant #4).
	AttrGenAIUsageCacheCreationInputTokens = "gen_ai.usage.cache_creation.input_tokens" //nolint:gosec // G101 false positive: attribute key, not a credential
	AttrGenAIUsageCacheReadInputTokens     = "gen_ai.usage.cache_read.input_tokens"     //nolint:gosec // G101 false positive: attribute key, not a credential

	// AttrGenAIUsageReasoningOutputTokens is the Recommended count of
	// reasoning/thinking tokens, emitted when the provider reports it
	// (OpenAI reasoning_tokens, Gemini thoughtsTokenCount).
	AttrGenAIUsageReasoningOutputTokens = "gen_ai.usage.reasoning.output_tokens" //nolint:gosec // G101 false positive: attribute key, not a credential

	// AttrGenAIUsageServerToolUsePrefix prefixes the per-tool server-side
	// usage counters Anthropic reports under usage.server_tool_use
	// (web_search_requests, web_fetch_requests, ...), emitted as
	// gen_ai.usage.server_tool_use.<counter> with the provider's own counter
	// name as the suffix. Conscious exception to the "never mint inside
	// gen_ai.*" rule above: the spec has no home for server-tool counters
	// yet, the key family mirrors the provider-page usage.* pattern the
	// cache attributes follow, and it renames in lockstep if/when the spec
	// lands one. Emitted only for counters the provider actually reported
	// (non-zero), like every other usage attribute.
	AttrGenAIUsageServerToolUsePrefix = "gen_ai.usage.server_tool_use." //nolint:gosec // G101 false positive: attribute key, not a credential

	// AttrSlipSpaceUsageCacheCreation5m / 1h split
	// gen_ai.usage.cache_creation.input_tokens by cache TTL (Anthropic
	// usage.cache_creation.ephemeral_{5m,1h}_input_tokens). The tiers
	// bill at different write premiums (1.25× vs 2× input), so costing
	// needs the split. slipspace.* mints — the GenAI semconv has no TTL
	// vocabulary (checked 2026-07-03); they rename in lockstep if one
	// lands. When present they sum to the cache_creation total.
	AttrSlipSpaceUsageCacheCreation5m = "slipspace.usage.cache_creation.ephemeral_5m_input_tokens" //nolint:gosec // G101 false positive: attribute key, not a credential
	AttrSlipSpaceUsageCacheCreation1h = "slipspace.usage.cache_creation.ephemeral_1h_input_tokens" //nolint:gosec // G101 false positive: attribute key, not a credential

	// AttrSlipSpaceUsageInputAudioTokens / OutputAudioTokens are the
	// audio-modality shares of input/output tokens (OpenAI
	// *_tokens_details.audio_tokens, Gemini per-modality
	// *TokensDetails). Audio bills at its own (much higher) rate.
	// slipspace.* mints shaped per-modality after the direction of
	// semconv-genai issue #23 (gen_ai.token.modality), so a rename to
	// spec keys stays mechanical.
	AttrSlipSpaceUsageInputAudioTokens  = "slipspace.usage.audio.input_tokens"  //nolint:gosec // G101 false positive: attribute key, not a credential
	AttrSlipSpaceUsageOutputAudioTokens = "slipspace.usage.audio.output_tokens" //nolint:gosec // G101 false positive: attribute key, not a credential

	// AttrSlipSpaceServiceTier is the provider-reported processing tier
	// the request was billed under (OpenAI/Anthropic service_tier) — a
	// whole-request pricing multiplier. Generic slipspace.* key because
	// the semconv only defines the provider-scoped
	// openai.response.service_tier (still emitted for OpenAI) and
	// nothing for other providers; one key beats a per-provider fan-out
	// for the pricing layer that consumes it.
	AttrSlipSpaceServiceTier = "slipspace.service_tier"

	// AttrSlipSpaceInferenceGeo is Anthropic's usage.inference_geo —
	// the inference region, which carries its own pricing multiplier
	// (e.g. "us" bills 1.1×).
	AttrSlipSpaceInferenceGeo = "slipspace.inference_geo"

	// AttrSlipSpaceCostUSD is the pricing engine's total USD estimate
	// for the request, on the span/event only. Per-category companions
	// are minted as AttrSlipSpaceCostPrefix + <category> + ".usd". The
	// semconv defines no cost vocabulary (checked 2026-07-03).
	AttrSlipSpaceCostUSD = "slipspace.cost.usd"

	// AttrSlipSpaceCostPrefix prefixes the per-category cost attributes
	// (slipspace.cost.input.usd, slipspace.cost.cache_write.usd, ...).
	AttrSlipSpaceCostPrefix = "slipspace.cost."

	// AttrSlipSpaceCostUnpriced marks a usage-bearing request no
	// rate-card entry matched — it has token counts and no cost, as
	// opposed to a request costing $0.
	AttrSlipSpaceCostUnpriced = "slipspace.cost.unpriced"

	// AttrSlipSpaceCostCategory is the charge-category label on the
	// slipspace.cost.usd.total meter (input|output|cache_read|
	// cache_write|tool_calls).
	AttrSlipSpaceCostCategory = "slipspace.cost.category"

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
// spool. Gated by SLIPSPACE_OTEL_CAPTURE_CONTENT.
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

// Executor values for a tool_call / tool_call_response part — who runs the
// tool. "server" is a provider-hosted tool the upstream executes inline (its
// result rides the same span); "client" is an ordinary function call the
// caller runs and returns on a later turn. Empty marks a non-tool part or a
// legacy span predating the distinction.
const (
	ExecutorServer = "server"
	ExecutorClient = "client"
)

// SlipSpace-namespaced extras — dimensions the GenAI spec has no concept for.
const (
	// AttrSlipSpaceProtocol is the precise resolved protocol (chat, messages,
	// generate_content) or passthrough family, retained beside the coarse
	// gen_ai.operation.name so the console keeps its per-protocol breakdown.
	AttrSlipSpaceProtocol = "slipspace.protocol"

	// AttrSlipSpaceConfiguration is the resolved SlipSpace configuration name.
	AttrSlipSpaceConfiguration = "slipspace.configuration"

	// AttrSlipSpaceMethod, AttrSlipSpaceAPIKeyName, AttrSlipSpaceUpstreamStatus,
	// AttrSlipSpaceTags, and AttrSlipSpaceRulesFired are the gateway facts the
	// Arbiter ingest reads off the gen_ai span to populate the
	// request_events gateway-owned columns. The span is the SINGLE writer of
	// that entity — the Record feed lands only a lazy verbatim blob, joined by
	// correlation_id when an operator opens the inspector (invariant #4) — so
	// carrying the already-computed gateway scalars on the span is what lets the
	// console render a row from the span alone. They stay off every meter label
	// — method/api-key/rule names are unbounded cardinality. The ingest reads
	// these keys as string literals; do not rename without rolling the telemetry
	// service in lockstep.
	AttrSlipSpaceMethod         = "slipspace.method"
	AttrSlipSpaceAPIKeyName     = "slipspace.api_key_name" //nolint:gosec // G101 false positive: attribute key naming the API key, not its secret value
	AttrSlipSpaceUpstreamStatus = "slipspace.upstream_status"

	// AttrSlipSpaceTags is the post-rule tag set attached to the request, as a
	// string slice. AttrSlipSpaceRulesFired is the set of fired rule names (names
	// only — actions/termination ride the Record's rule chain, not the span).
	AttrSlipSpaceTags       = "slipspace.tags"
	AttrSlipSpaceRulesFired = "slipspace.rules_fired"

	// AttrSlipSpaceCorrelationID carries the request correlation id on the
	// span so a trace can be cross-referenced to logs and captured
	// records (which key on the same id).
	AttrSlipSpaceCorrelationID = "slipspace.correlation_id"

	// AttrSlipSpaceResilienceTarget and AttrSlipSpaceResilienceOutcome label a
	// per-attempt child span with the resilience target name and the
	// attempt outcome (success, failure_status, transport_error,
	// cb_blocked).
	AttrSlipSpaceResilienceTarget  = "slipspace.resilience.target"
	AttrSlipSpaceResilienceOutcome = "slipspace.resilience.outcome"

	// AttrSlipSpaceUnmappedField is the dotted JSON path of a provider field
	// this build does not model — the per-field dimension on
	// gateway.unmapped_fields.total. Cardinality is bounded by the provider
	// API surface (tens of field names), not by request volume.
	AttrSlipSpaceUnmappedField = "slipspace.unmapped_field"

	// AttrSlipSpaceUnmappedDirection is "request" or "response", marking which
	// side of the exchange carried the unmapped field.
	AttrSlipSpaceUnmappedDirection = "slipspace.unmapped_direction"

	// AttrSlipSpaceTranslateSource and AttrSlipSpaceTranslateTarget are the source
	// and target wire protocols of a cross-provider translation — the
	// pair dimensions on gateway.translation.field_drops.total.
	AttrSlipSpaceTranslateSource = "slipspace.translate_source"
	AttrSlipSpaceTranslateTarget = "slipspace.translate_target"

	// AttrSlipSpaceTranslateField is the dotted source-side path of a feature
	// dropped in translation — the per-field dimension on
	// gateway.translation.field_drops.total. Bounded by the modelled field set.
	AttrSlipSpaceTranslateField = "slipspace.translate_field"
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

// OperationNameForProtocol maps a SlipSpace protocol key to the
// gen_ai.operation.name spec value: OpenAI/Anthropic chat surfaces and the
// OpenAI responses API to "chat", Gemini's generate_content to the dedicated
// "generate_content" value, embeddings to "embeddings". Protocols the spec
// has no operation for (e.g. models listing) fall through to their own key —
// the precise route is always also emitted as slipspace.protocol, so nothing is
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

// GenAIProviderName maps a SlipSpace internal provider key to the OTel
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
