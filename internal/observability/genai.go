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

	// AttrGenAITokenType keys the gen_ai.client.token.usage histogram by
	// direction. The spec enum is input|output only — cache tokens have
	// no value here and ride sluice.* counters instead.
	AttrGenAITokenType = "gen_ai.token.type" //nolint:gosec // G101 false positive: OTel attribute key, not a credential

	// AttrGenAIConversationID is the spec home for a session/conversation
	// identifier. Populated by the session-bundling work; declared here so
	// the span builder has a single key reference once that lands.
	AttrGenAIConversationID = "gen_ai.conversation.id"
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
)

// Sluice-namespaced extras — dimensions the GenAI spec has no concept for.
const (
	// AttrSluiceEndpoint is the precise provider route (chat_completions,
	// messages, generate_content) retained beside the coarse
	// gen_ai.operation.name so the console keeps its per-route breakdown.
	AttrSluiceEndpoint = "sluice.endpoint"

	// AttrSluiceConfiguration is the resolved Sluice configuration name.
	AttrSluiceConfiguration = "sluice.configuration"
)

// gen_ai.operation.name values (spec-defined).
const (
	OperationChat       = "chat"
	OperationEmbeddings = "embeddings"
)

// gen_ai.token.type values (spec-defined).
const (
	TokenTypeInput  = "input"
	TokenTypeOutput = "output"
)

// OperationNameForEndpoint maps a Sluice endpoint key to the coarse
// gen_ai.operation.name. The chat-shaped inference surfaces of all three
// providers collapse to "chat"; embeddings to "embeddings". Endpoints the
// spec has no operation for (e.g. models listing) fall through to their
// own key — the precise route is always also emitted as sluice.endpoint,
// so no information is lost.
func OperationNameForEndpoint(endpoint string) string {
	switch endpoint {
	case "chat_completions", "messages", "generate_content", "responses":
		return OperationChat
	case "embeddings":
		return OperationEmbeddings
	default:
		return endpoint
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
