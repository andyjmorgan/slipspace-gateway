package tokens

import "github.com/andyjmorgan/slipspace-gateway/internal/middleware/sseframe"

// extractorFn signs every per-endpoint implementation. The dispatcher
// collates the response once (via sseframe) and hands each implementation
// the resulting JSON frames; implementations only walk them and pick the
// Strategy.
type extractorFn func(frames [][]byte) Snapshot

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
	"chat":             extractOpenAIChat,
	"responses":        extractOpenAIResponses,
	"messages":         extractAnthropicMessages,
	"generate_content": extractGeminiContent,
}

// Extract collates raw — a non-streaming JSON body or SSE-framed chunks —
// and dispatches it onto the matching extractor. Prefer ExtractFrames when
// the caller already holds sseframe-collated frames, so the response body
// is split only once per request.
//
// An unrecognised endpoint or a recognised endpoint that found no usage
// data returns Snapshot{Recognised: false}. Callers use that signal to
// leave the per-request token fields zero and skip recording — partial
// accounting is worse than none, because dashboards treat a missing total
// as a real low number.
func Extract(provider, endpoint string, raw []byte) Snapshot {
	return ExtractFrames(provider, endpoint, sseframe.Collate(raw))
}

// ExtractFrames dispatches frames already collated by sseframe.Collate onto
// the matching extractor. provider is currently unused; it stays in the
// signature so a future provider with a name collision can disambiguate
// without breaking the call site.
func ExtractFrames(provider, endpoint string, frames [][]byte) Snapshot {
	_ = provider
	if len(frames) == 0 {
		return Snapshot{}
	}
	fn, ok := registry[endpoint]
	if !ok {
		return Snapshot{}
	}
	return fn(frames)
}

// ServerToolUseFrames returns the provider's server-side tool usage counters
// for frames already collated by sseframe.Collate, keyed by the wire counter
// name (web_search_requests, web_fetch_requests, ...). It is a sibling to
// ExtractFrames rather than a Snapshot field so Snapshot stays a comparable
// value type. Anthropic Messages is the only protocol that reports per-tool
// counters today (usage.server_tool_use); the OpenAI Responses / Chat usage
// objects carry no analogue (confirmed against the SDK ResponseUsage model —
// built-in tool invocations are billed per *_call output item, outside
// usage), and Gemini's usageMetadata has none. Every other endpoint returns
// nil, as does an Anthropic response that invoked no server tools.
func ServerToolUseFrames(provider, endpoint string, frames [][]byte) map[string]int {
	_ = provider
	if endpoint != "messages" || len(frames) == 0 {
		return nil
	}
	return extractAnthropicServerToolUse(frames)
}
