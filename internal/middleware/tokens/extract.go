package tokens

// extractorFn signs every per-endpoint implementation. Implementations
// own the wire-shape sniffing (JSON body vs SSE) and the Strategy
// choice; the dispatcher only routes.
type extractorFn func(raw []byte) Snapshot

// registry maps endpoint names to extractor functions. Endpoint names
// match the strings the config loader accepts under
// providers.<name>.endpoints.<name>; identical keys to the
// livefeed/accumulator registry so adding a new provider extends both
// in tandem.
//
// Provider isn't part of the key — endpoint name alone is enough
// because Anthropic and Gemini's OpenAI-compat surfaces emit the
// OpenAI chunk shape under the same `chat_completions` name. If a
// future provider reuses a name with a different shape, dispatch
// would need to also switch on provider; today, names are stable per
// protocol.
var registry = map[string]extractorFn{
	"chat_completions": extractOpenAIChat,
	"responses":        extractOpenAIResponses,
	"messages":         extractAnthropicMessages,
	"generate_content": extractGeminiContent,
}

// Extract dispatches (provider, endpoint) onto the matching extractor
// and returns the aggregated Snapshot from walking raw. raw may be
// non-streaming JSON or SSE-framed chunks; the extractor sniffs the
// first non-whitespace byte and picks the right path.
//
// An unrecognised endpoint or a recognised endpoint that found no
// usage data returns Snapshot{Recognised: false}. Callers use that
// signal to leave the per-request token fields zero and skip bumping
// the gateway.tokens.* counters — partial accounting is worse than
// no accounting because dashboards will treat a missing total as a
// real low number.
//
// provider is currently unused; stays in the signature so a future
// provider with a name collision can disambiguate without breaking
// the call site.
func Extract(provider, endpoint string, raw []byte) Snapshot {
	_ = provider
	if len(raw) == 0 {
		return Snapshot{}
	}
	fn, ok := registry[endpoint]
	if !ok {
		return Snapshot{}
	}
	return fn(raw)
}
