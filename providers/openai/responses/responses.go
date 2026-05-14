// Package responses models OpenAI's POST /v1/responses wire surface — the
// request body, the non-streaming response, and the streaming SSE event
// hierarchy (see events.go).
//
// Every exported struct embeds models.DynamicProperties so fields OpenAI ships
// after this package was built still round-trip when a request or response
// flows through the gateway. Output items inside ResponsesResponse.Output and
// streaming events carry polymorphic shapes; the dedicated UnmarshalX helpers
// dispatch unknown discriminator values to UnknownX fallbacks rather than
// dropping them.
package responses

import (
	"encoding/json"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// ResponsesRequest is the request body for OpenAI's POST /v1/responses
// endpoint. Unknown fields round-trip via the embedded DynamicProperties.
type ResponsesRequest struct {
	// Model is the OpenAI model identifier to invoke.
	Model string `json:"model"`

	// Input is the polymorphic request input: either a bare string or an
	// array of typed input items. Kept raw so both shapes round-trip
	// without forcing a particular projection.
	Input json.RawMessage `json:"input"`

	// Instructions is the system-style prompt prefixed to the conversation.
	Instructions *string `json:"instructions,omitempty"`

	// Tools advertises the function/built-in tool catalogue the model may
	// call.
	Tools []Tool `json:"tools,omitempty"`

	// ToolChoice is OpenAI's polymorphic tool-selection field: either a
	// string ("auto"/"none"/"required") or an object pinning a specific
	// tool. Kept raw so callers pick the representation.
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`

	// MaxOutputTokens caps generated tokens (including reasoning tokens on
	// reasoning models).
	MaxOutputTokens *int `json:"max_output_tokens,omitempty"`

	// Reasoning configures the model's reasoning behaviour.
	Reasoning *ReasoningOptions `json:"reasoning,omitempty"`

	// Temperature is the sampling temperature; nil means "use the server
	// default".
	Temperature *float64 `json:"temperature,omitempty"`

	// TopP is the nucleus-sampling cutoff; nil means "use the server
	// default".
	TopP *float64 `json:"top_p,omitempty"`

	// Modalities lists the output modalities to enable (e.g., "text").
	Modalities []string `json:"modalities,omitempty"`

	// Store opts the request into OpenAI's stored-responses feature.
	Store *bool `json:"store,omitempty"`

	// Stream requests SSE streaming when non-nil and true.
	Stream *bool `json:"stream,omitempty"`

	// Metadata is a free-form key/value bag stored alongside the response
	// when Store is true.
	Metadata map[string]string `json:"metadata,omitempty"`

	// PreviousResponseID chains this request to a prior stored response so
	// the model continues the same conversation server-side.
	PreviousResponseID *string `json:"previous_response_id,omitempty"`

	// Truncation selects how the server handles prompts that exceed the
	// model's context window (e.g., "auto", "disabled").
	Truncation string `json:"truncation,omitempty"`

	// Background runs the response in the background and returns
	// immediately with a status that the caller polls.
	Background bool `json:"background,omitempty"`

	// User is the caller-supplied end-user identifier used by OpenAI for
	// abuse detection.
	User string `json:"user,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into r, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (r *ResponsesRequest) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, r)
}

// MarshalJSON encodes r and merges DynamicProperties.Extra back into the
// resulting object.
func (r ResponsesRequest) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// ResponsesResponse is the non-streaming response body returned by POST
// /v1/responses. Unknown fields round-trip via the embedded
// DynamicProperties.
type ResponsesResponse struct {
	// ID is the OpenAI-assigned response identifier.
	ID string `json:"id"`

	// Object is the OpenAI object discriminator (typically "response").
	Object string `json:"object"`

	// CreatedAt is the response creation time as a Unix epoch in seconds.
	CreatedAt int64 `json:"created_at"`

	// Status is the response lifecycle state ("completed", "in_progress",
	// "incomplete", "failed", ...).
	Status string `json:"status"`

	// Model is the resolved model name OpenAI billed the request against.
	Model string `json:"model"`

	// Output is the ordered list of items the model emitted (messages,
	// tool calls, reasoning summaries, ...).
	Output []OutputItem `json:"output,omitempty"`

	// OutputText is the concatenated plain-text projection of Output, as a
	// convenience for callers that only want the final text.
	OutputText string `json:"output_text,omitempty"`

	// Usage reports token accounting.
	Usage *Usage `json:"usage,omitempty"`

	// IncompleteDetails explains why a response with Status ==
	// "incomplete" stopped early.
	IncompleteDetails *IncompleteDetails `json:"incomplete_details,omitempty"`

	// ParallelToolCalls echoes whether parallel tool calls were enabled.
	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`

	// PreviousResponseID is the prior-response identifier this response
	// chained from (when ResponsesRequest.PreviousResponseID was set).
	PreviousResponseID *string `json:"previous_response_id,omitempty"`

	// Reasoning surfaces reasoning-effort telemetry from the model.
	Reasoning *ReasoningOutput `json:"reasoning,omitempty"`

	// Metadata echoes the request metadata.
	Metadata map[string]string `json:"metadata,omitempty"`

	// Error carries the upstream error payload when Status == "failed".
	// Kept raw because OpenAI's error shape evolves frequently.
	Error json.RawMessage `json:"error,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into r, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (r *ResponsesResponse) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, r)
}

// MarshalJSON encodes r and merges DynamicProperties.Extra back into the
// resulting object.
func (r ResponsesResponse) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// Tool is one tool exposed to the model on a ResponsesRequest — a function
// declaration, a built-in tool, or a custom tool kind OpenAI adds later.
// Unknown fields round-trip via the embedded DynamicProperties.
type Tool struct {
	// Type is the tool discriminator (e.g., "function", "web_search",
	// "code_interpreter").
	Type string `json:"type"`

	// Name is the function identifier when Type == "function".
	Name string `json:"name,omitempty"`

	// Description is the natural-language description the model uses to
	// decide when to call the tool.
	Description string `json:"description,omitempty"`

	// Parameters is the JSON-Schema document describing the function's
	// arguments when Type == "function". Kept raw so callers can build or
	// inspect schemas without going through a typed schema model.
	Parameters json.RawMessage `json:"parameters,omitempty"`

	// Strict requests strict JSON-Schema enforcement on the model's
	// generated arguments.
	Strict *bool `json:"strict,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into t, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (t *Tool) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON encodes t and merges DynamicProperties.Extra back into the
// resulting object.
func (t Tool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// ReasoningOptions configures the model's reasoning behaviour on a
// ResponsesRequest. Unknown fields round-trip via the embedded
// DynamicProperties.
type ReasoningOptions struct {
	// Effort hints how much reasoning the model should spend ("low",
	// "medium", "high").
	Effort string `json:"effort,omitempty"`

	// Summary requests a textual summary of the reasoning trace
	// ("auto", "concise", "detailed").
	Summary string `json:"summary,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into o, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (o *ReasoningOptions) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, o)
}

// MarshalJSON encodes o and merges DynamicProperties.Extra back into the
// resulting object.
func (o ReasoningOptions) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(o) }

// ReasoningOutput surfaces reasoning telemetry from the model on a
// ResponsesResponse. Unknown fields round-trip via the embedded
// DynamicProperties.
type ReasoningOutput struct {
	// Effort echoes the resolved reasoning effort the model used.
	Effort string `json:"effort,omitempty"`

	// Summary is the textual reasoning summary. Kept raw because OpenAI
	// has shipped both string and array shapes for this field.
	Summary json.RawMessage `json:"summary,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into o, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (o *ReasoningOutput) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, o)
}

// MarshalJSON encodes o and merges DynamicProperties.Extra back into the
// resulting object.
func (o ReasoningOutput) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(o) }

// IncompleteDetails describes why a response with Status == "incomplete"
// stopped early. Unknown fields round-trip via the embedded
// DynamicProperties.
type IncompleteDetails struct {
	// Reason is OpenAI's machine-readable termination cause (e.g.,
	// "max_output_tokens", "content_filter").
	Reason string `json:"reason,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into d, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (d *IncompleteDetails) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, d)
}

// MarshalJSON encodes d and merges DynamicProperties.Extra back into the
// resulting object.
func (d IncompleteDetails) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// Usage describes token accounting for a /v1/responses call. Defined locally
// rather than imported from the chat package because the /v1/responses
// endpoint uses different field names (input_tokens/output_tokens vs
// prompt_tokens/completion_tokens). Unknown fields round-trip via the
// embedded DynamicProperties.
type Usage struct {
	// InputTokens counts tokens billed for the request input.
	InputTokens int `json:"input_tokens"`

	// OutputTokens counts tokens billed for the model's generated output.
	OutputTokens int `json:"output_tokens"`

	// TotalTokens is InputTokens + OutputTokens (mirrored by the provider).
	TotalTokens int `json:"total_tokens"`

	// InputTokensDetails breaks the input total into sub-counters when the
	// model reports them (e.g., cached tokens).
	InputTokensDetails *InputTokensDetails `json:"input_tokens_details,omitempty"`

	// OutputTokensDetails breaks the output total into sub-counters
	// (e.g., reasoning tokens).
	OutputTokensDetails *OutputTokensDetails `json:"output_tokens_details,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into u, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (u *Usage) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, u) }

// MarshalJSON encodes u and merges DynamicProperties.Extra back into the
// resulting object.
func (u Usage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(u) }

// InputTokensDetails breaks the Usage.InputTokens total into sub-counters.
// Unknown fields round-trip via the embedded DynamicProperties.
type InputTokensDetails struct {
	// CachedTokens is the share of InputTokens served from the prompt
	// cache.
	CachedTokens int `json:"cached_tokens,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into d, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (d *InputTokensDetails) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, d)
}

// MarshalJSON encodes d and merges DynamicProperties.Extra back into the
// resulting object.
func (d InputTokensDetails) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// OutputTokensDetails breaks the Usage.OutputTokens total into sub-counters.
// Unknown fields round-trip via the embedded DynamicProperties.
type OutputTokensDetails struct {
	// ReasoningTokens is the share of OutputTokens consumed by the model's
	// hidden reasoning trace.
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into d, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (d *OutputTokensDetails) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, d)
}

// MarshalJSON encodes d and merges DynamicProperties.Extra back into the
// resulting object.
func (d OutputTokensDetails) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// OutputItem is one item in ResponsesResponse.Output. Output items are
// polymorphic by Type — they can be assistant messages, tool calls,
// reasoning summaries, etc. — but the shape evolves frequently, so this
// package models them as a single struct that carries every common field as
// json.RawMessage. Type is the discriminator; the typed sub-payload (Content,
// Output, Summary) is kept raw so callers can dispatch on Type and decode
// the appropriate shape. Unknown fields round-trip via the embedded
// DynamicProperties.
type OutputItem struct {
	// Type is the item discriminator (e.g., "message", "function_call",
	// "tool_call", "reasoning").
	Type string `json:"type"`

	// ID is the OpenAI-assigned item identifier within the response.
	ID string `json:"id,omitempty"`

	// Status is the item's lifecycle state.
	Status string `json:"status,omitempty"`

	// Role is the message role on items where Type == "message"
	// (typically "assistant").
	Role string `json:"role,omitempty"`

	// Content is the typed content payload (e.g., array of content parts
	// on message items). Kept raw because the shape varies by Type.
	Content json.RawMessage `json:"content,omitempty"`

	// Name is the function name on function/tool-call items.
	Name string `json:"name,omitempty"`

	// Arguments is the function arguments encoded as a JSON string (not a
	// JSON object) on function/tool-call items, matching OpenAI's
	// convention.
	Arguments string `json:"arguments,omitempty"`

	// CallID correlates a tool-call item to its corresponding output item.
	CallID string `json:"call_id,omitempty"`

	// Output is the tool output payload on tool-output items. Kept raw
	// because the shape is tool-dependent.
	Output json.RawMessage `json:"output,omitempty"`

	// Summary is the reasoning summary payload on reasoning items. Kept
	// raw because OpenAI has shipped both string and array shapes.
	Summary json.RawMessage `json:"summary,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into o, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (o *OutputItem) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, o) }

// MarshalJSON encodes o and merges DynamicProperties.Extra back into the
// resulting object.
func (o OutputItem) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(o) }
