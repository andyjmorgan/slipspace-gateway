package accumulator

// Result is the output of accumulating a streamed provider response.
// Empty Text and ToolCalls with Recognised=false signals the dispatcher
// has no accumulator for this (provider, endpoint) and the caller
// should fall back to surfacing the raw bytes.
type Result struct {
	// Text is the assembled human-readable response text, concatenated
	// across stream chunks in arrival order.
	Text string

	// ToolCalls is the list of tool/function-call invocations extracted
	// from the stream. Arguments are the concatenated delta JSON the
	// model emitted.
	ToolCalls []ToolCall

	// Recognised is true when an accumulator was selected for this
	// (provider, endpoint) and ran. False means the caller should
	// render the raw bytes instead.
	Recognised bool

	// Partial is true when the accumulator hit a malformed chunk or
	// an unknown delta type and stopped before EOF. Text and ToolCalls
	// still carry whatever the accumulator parsed up to that point.
	Partial bool
}

// ToolCall is one tool/function-call invocation reassembled from a
// streaming response.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
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
}

// Accumulate dispatches raw onto the per-endpoint accumulator and
// returns its Result. Recognised=false means the dispatcher had no
// accumulator for this endpoint; the caller should surface the raw
// bytes unchanged.
func Accumulate(_ /* provider */, endpoint string, raw []byte) Result {
	fn, ok := registry[endpoint]
	if !ok || len(raw) == 0 {
		return Result{}
	}
	res := fn(raw)
	res.Recognised = true
	return res
}
