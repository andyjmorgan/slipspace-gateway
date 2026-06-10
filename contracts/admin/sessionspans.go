package admin

import "time"

// SessionSpan is one element of the SessionSpansDTO v1 array returned by
// GET /api/v1/sessions/{id}/spans — one gen_ai span (one upstream request) of
// a session, projected from the request_events span_event blob alone (never
// from Records; invariant #4). The shape is frozen by the span-dto JSON schema
// (sluice:session-spans-dto/v1) the session lifecycle page renders from.
//
// Truncation policy: this server serves FULL text and tool arguments — no
// cap is applied — and every *Chars field carries the same value as the
// served text's length, so the renderer's "showing first N of M" guard never
// fires. The *Chars fields remain in the contract because the schema makes
// truncation a server prerogative; a future cap only changes the server, not
// the renderer.
type SessionSpan struct {
	// CID is the correlation id (= span id in the console).
	CID string `json:"cid"`

	// At is the request receipt time (the span start), RFC3339.
	At time.Time `json:"at"`

	// LatencyMs is the request wall time; null when the span blob is absent.
	LatencyMs *int64 `json:"latency_ms"`

	// TTFCMs is gen_ai.response.time_to_first_chunk converted to ms; null for
	// non-streaming requests (the attribute is emitted on streams only).
	TTFCMs *int64 `json:"ttfc_ms"`

	// Status is the upstream HTTP status (sluice.upstream_status, falling back
	// to the client-facing status column); null when neither is known.
	Status *int `json:"status"`

	// Model is the requested model; null when unknown.
	Model *string `json:"model"`

	// FinishReason is the first gen_ai.response.finish_reasons entry; null
	// when the response carried none.
	FinishReason *string `json:"finish_reason"`

	// SessionID is the session bundle root every span of the session shares.
	SessionID string `json:"session_id"`

	// ConversationID is the thread the request belongs to: the session for the
	// main loop, the subagent thread id for sub-agents.
	ConversationID string `json:"conversation_id"`

	// ParentConversationID links a subagent thread toward its parent; null for
	// the main loop.
	ParentConversationID *string `json:"parent_conversation_id"`

	// Usage is the per-span token rollup incl. the server-tool counter map.
	Usage SessionSpanUsage `json:"usage"`

	// OutputParts is the normalized assistant output envelope, in emission
	// order. Always an array (empty when content capture was off).
	OutputParts []SessionSpanOutputPart `json:"output_parts"`

	// InputParts is the trailing input delta — the NEW parts of this request
	// only. Always an array (empty when content capture was off).
	InputParts []SessionSpanInputPart `json:"input_parts"`

	// InputText is the human text of the trailing message when InputParts has
	// text parts (concatenated in order); null otherwise.
	InputText *string `json:"input_text"`

	// InputTextChars is the uncapped total size of InputText.
	InputTextChars *int `json:"input_text_chars"`

	// OutputText is the concatenated output text parts; null when the
	// response carried no text part.
	OutputText *string `json:"output_text"`

	// OutputTextChars is the uncapped total size of OutputText.
	OutputTextChars *int `json:"output_text_chars"`
}

// SessionSpanUsage is the per-span token rollup. Each count is null when the
// span carried no such usage attribute (e.g. an errored request with no usage
// block), distinguishing "not reported" from a genuine zero.
type SessionSpanUsage struct {
	// Input / Output are gen_ai.usage.input_tokens / .output_tokens.
	Input  *int64 `json:"input"`
	Output *int64 `json:"output"`

	// CacheRead / CacheCreation are the provider prompt-cache counts
	// (gen_ai.usage.cache_read.input_tokens / .cache_creation.input_tokens).
	CacheRead     *int64 `json:"cache_read"`
	CacheCreation *int64 `json:"cache_creation"`

	// ServerToolUse is the generic provider-executed tool counter map
	// (gen_ai.usage.server_tool_use.*), keyed by the provider's own counter
	// name; null when the span carried none.
	ServerToolUse map[string]int64 `json:"server_tool_use"`
}

// SessionSpanOutputPart is one normalized assistant output part. Type is one
// of text | reasoning | tool_call | tool_call_response | unknown; only the
// fields relevant to the type are set.
type SessionSpanOutputPart struct {
	// Type is the part discriminator.
	Type string `json:"type"`

	// Chars is the true size of the part's text (text / reasoning content, or
	// a tool_call_response's result). Absent for tool_call parts.
	Chars *int `json:"chars,omitempty"`

	// ID is the tool_call / tool_call_response join key.
	ID string `json:"id,omitempty"`

	// Name is the tool name (tool_call only).
	Name string `json:"name,omitempty"`

	// Args is the raw tool-call arguments JSON — an opaque vendor payload the
	// renderer never field-picks. Served whole (no cap).
	Args string `json:"args,omitempty"`

	// ArgsChars is the uncapped arguments size; equal to len(Args) since this
	// server applies no cap.
	ArgsChars *int `json:"args_chars,omitempty"`

	// Text is the result text of a tool_call_response output part (a
	// server-executed tool's result, carried on the same span as its call).
	// Served whole (no cap); Chars carries its size.
	Text string `json:"text,omitempty"`
}

// SessionSpanInputPart is one part of the trailing input delta. Type is
// text | tool_call_response.
type SessionSpanInputPart struct {
	// Type is the part discriminator.
	Type string `json:"type"`

	// ID joins a tool_call_response back to a prior span's tool_call.
	ID string `json:"id,omitempty"`

	// Chars is the true total size of Text before any cap (equal to
	// len(Text) since this server applies no cap).
	Chars *int `json:"chars,omitempty"`

	// Text is the part's own text — the user message text or the tool result
	// body. Served whole (no cap).
	Text string `json:"text,omitempty"`

	// IsError marks an errored tool result. Omitted today: the gateway's
	// content normalizer does not yet capture the provider is_error flag.
	IsError bool `json:"is_error,omitempty"`
}
