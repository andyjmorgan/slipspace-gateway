// Package responses models the OpenAI /v1/responses wire surface — request,
// non-streaming response, and streaming event types. Every exported struct
// embeds models.DynamicProperties so provider-added fields round-trip intact.
package responses

import (
	"encoding/json"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// ResponsesRequest is the JSON body posted to /v1/responses.
type ResponsesRequest struct {
	Model string `json:"model"`

	Input json.RawMessage `json:"input"`

	Instructions *string `json:"instructions,omitempty"`

	Tools []Tool `json:"tools,omitempty"`

	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`

	MaxOutputTokens *int `json:"max_output_tokens,omitempty"`

	Reasoning *ReasoningOptions `json:"reasoning,omitempty"`

	Temperature *float64 `json:"temperature,omitempty"`

	TopP *float64 `json:"top_p,omitempty"`

	Modalities []string `json:"modalities,omitempty"`

	Store *bool `json:"store,omitempty"`

	Stream *bool `json:"stream,omitempty"`

	Metadata map[string]string `json:"metadata,omitempty"`

	PreviousResponseID *string `json:"previous_response_id,omitempty"`

	Truncation string `json:"truncation,omitempty"`

	Background bool `json:"background,omitempty"`

	User string `json:"user,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (r *ResponsesRequest) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, r)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (r ResponsesRequest) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// ResponsesResponse is the non-streaming response from /v1/responses.
type ResponsesResponse struct {
	ID string `json:"id"`

	Object string `json:"object"`

	CreatedAt int64 `json:"created_at"`

	Status string `json:"status"`

	Model string `json:"model"`

	Output []OutputItem `json:"output,omitempty"`

	OutputText string `json:"output_text,omitempty"`

	Usage *Usage `json:"usage,omitempty"`

	IncompleteDetails *IncompleteDetails `json:"incomplete_details,omitempty"`

	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`

	PreviousResponseID *string `json:"previous_response_id,omitempty"`

	Reasoning *ReasoningOutput `json:"reasoning,omitempty"`

	Metadata map[string]string `json:"metadata,omitempty"`

	Error json.RawMessage `json:"error,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (r *ResponsesResponse) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, r)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (r ResponsesResponse) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// Tool is a function or built-in tool the model may invoke.
type Tool struct {
	Type string `json:"type"`

	Name string `json:"name,omitempty"`

	Description string `json:"description,omitempty"`

	Parameters json.RawMessage `json:"parameters,omitempty"`

	Strict *bool `json:"strict,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (t *Tool) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (t Tool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// ReasoningOptions configures the model's reasoning behaviour.
type ReasoningOptions struct {
	Effort string `json:"effort,omitempty"`

	Summary string `json:"summary,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (o *ReasoningOptions) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, o)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (o ReasoningOptions) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(o) }

// ReasoningOutput surfaces reasoning telemetry on the response.
type ReasoningOutput struct {
	Effort string `json:"effort,omitempty"`

	Summary json.RawMessage `json:"summary,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (o *ReasoningOutput) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, o)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (o ReasoningOutput) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(o) }

// IncompleteDetails describes why a response was cut short.
type IncompleteDetails struct {
	Reason string `json:"reason,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (d *IncompleteDetails) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, d)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (d IncompleteDetails) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// Usage describes token accounting for a /v1/responses call. Defined locally
// rather than imported from the chat package — provider response shapes are
// independent and OpenAI uses slightly different field names here
// (input_tokens vs prompt_tokens, output_tokens vs completion_tokens).
type Usage struct {
	InputTokens int `json:"input_tokens"`

	OutputTokens int `json:"output_tokens"`

	TotalTokens int `json:"total_tokens"`

	InputTokensDetails *InputTokensDetails `json:"input_tokens_details,omitempty"`

	OutputTokensDetails *OutputTokensDetails `json:"output_tokens_details,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (u *Usage) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, u) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (u Usage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(u) }

// InputTokensDetails breaks the input-token total into sub-counters.
type InputTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (d *InputTokensDetails) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, d)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (d InputTokensDetails) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// OutputTokensDetails breaks the output-token total into sub-counters.
type OutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (d *OutputTokensDetails) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, d)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (d OutputTokensDetails) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// OutputItem is one item in the response's output array. Output items can be
// messages, tool calls, reasoning summaries, etc.; the shape is provider-defined
// and evolves frequently, so we model it as a single struct with a Type
// discriminator and Content carried as raw JSON. DynamicProperties preserves
// every other field.
type OutputItem struct {
	Type string `json:"type"`

	ID string `json:"id,omitempty"`

	Status string `json:"status,omitempty"`

	Role string `json:"role,omitempty"`

	Content json.RawMessage `json:"content,omitempty"`

	Name string `json:"name,omitempty"`

	Arguments string `json:"arguments,omitempty"`

	CallID string `json:"call_id,omitempty"`

	Output json.RawMessage `json:"output,omitempty"`

	Summary json.RawMessage `json:"summary,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (o *OutputItem) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, o) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (o OutputItem) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(o) }
