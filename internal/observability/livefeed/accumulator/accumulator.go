package accumulator

// Result is the output of accumulating a streamed provider response.
// Recognised=false with an empty Assembled signals the dispatcher had
// no accumulator for this (provider, endpoint) and the caller should
// fall back to surfacing the raw bytes unchanged.
type Result struct {
	// Assembled is the JSON-encoded reconstruction of the response the
	// provider would have returned non-streaming. Shape matches the
	// provider's non-streaming response type exactly — ChatCompletion
	// for OpenAI, MessagesResponse for Anthropic, GenerateContent for
	// Gemini — so operators can eyeball streaming and non-streaming
	// traffic against the same mental model.
	Assembled []byte

	// Recognised is true when an accumulator was selected for this
	// (provider, endpoint) and ran. False means the caller should
	// render the raw bytes instead.
	Recognised bool

	// Partial is true when the accumulator hit a malformed chunk or
	// an unknown delta type and stopped before EOF. Assembled still
	// carries whatever the accumulator was able to reconstruct up to
	// that point.
	Partial bool
}

// accumulatorFn signs every per-protocol implementation.
type accumulatorFn func(raw []byte) Result

// registry maps endpoint names to accumulator functions. Endpoint names
// are operator-authored map keys per provider in providers.yaml; the
// conventional names (`chat_completions`, `messages`, `generate_content`,
// `responses`) cover the v1.0 surface and the OpenAI-compat endpoints
// on Anthropic / Gemini that share the OpenAI chunk shape.
//
// Provider isn't part of the key — endpoint name alone is enough
// because Anthropic / Gemini's OpenAI-compat surfaces emit OpenAI
// chunks under the same `chat_completions` name. If a future provider
// reuses a name with a different shape, dispatch would need to also
// switch on provider; today, names are stable per protocol.
var registry = map[string]accumulatorFn{
	"chat_completions": accumulateOpenAIChat,
	"messages":         accumulateAnthropicMessages,
	"generate_content": accumulateGeminiContent,
	"responses":        accumulateOpenAIResponses,
}

// Accumulate dispatches raw onto the per-endpoint accumulator and
// returns its Result. Recognised=false means the dispatcher had no
// accumulator for this endpoint; the caller should surface the raw
// bytes unchanged.
//
// provider is currently unused — dispatch keys on endpoint alone
// because the OpenAI-compat surfaces on Anthropic/Gemini emit the
// OpenAI chunk shape under the same `chat_completions` name. It
// stays in the signature so a future provider with a name collision
// can disambiguate without breaking callers.
func Accumulate(provider, endpoint string, raw []byte) Result {
	_ = provider
	fn, ok := registry[endpoint]
	if !ok || len(raw) == 0 {
		return Result{}
	}
	res := fn(raw)
	res.Recognised = true
	return res
}
