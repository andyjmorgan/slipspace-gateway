// Package chat models the OpenAI /v1/chat/completions wire surface — request,
// non-streaming response, and streaming chunk types. Every exported struct
// embeds models.DynamicProperties so provider-added fields round-trip intact.
package chat

import (
	"encoding/json"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// ChatCompletionRequest is the JSON body posted to /v1/chat/completions.
type ChatCompletionRequest struct {
	Model string `json:"model"`

	Messages RequestMessages `json:"messages"`

	MaxTokens *int `json:"max_tokens,omitempty"`

	MaxCompletionTokens *int `json:"max_completion_tokens,omitempty"`

	Temperature *float64 `json:"temperature,omitempty"`

	TopP *float64 `json:"top_p,omitempty"`

	N *int `json:"n,omitempty"`

	Stream bool `json:"stream,omitempty"`

	StreamOptions *StreamOptions `json:"stream_options,omitempty"`

	Stop json.RawMessage `json:"stop,omitempty"`

	PresencePenalty *float64 `json:"presence_penalty,omitempty"`

	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`

	LogitBias map[string]int `json:"logit_bias,omitempty"`

	LogProbs *bool `json:"logprobs,omitempty"`

	TopLogProbs *int `json:"top_logprobs,omitempty"`

	User string `json:"user,omitempty"`

	Tools []Tool `json:"tools,omitempty"`

	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`

	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`

	Seed *int `json:"seed,omitempty"`

	Modalities []string `json:"modalities,omitempty"`

	ReasoningEffort string `json:"reasoning_effort,omitempty"`

	ServiceTier string `json:"service_tier,omitempty"`

	Audio *AudioOptions `json:"audio,omitempty"`

	Metadata map[string]string `json:"metadata,omitempty"`

	Store *bool `json:"store,omitempty"`

	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (r *ChatCompletionRequest) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, r)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (r ChatCompletionRequest) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// ChatCompletionResponse is the non-streaming response from /v1/chat/completions.
type ChatCompletionResponse struct {
	ID string `json:"id"`

	Object string `json:"object"`

	Created int64 `json:"created"`

	Model string `json:"model"`

	SystemFingerprint string `json:"system_fingerprint,omitempty"`

	ServiceTier string `json:"service_tier,omitempty"`

	Choices []Choice `json:"choices"`

	Usage *Usage `json:"usage,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (r *ChatCompletionResponse) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, r)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (r ChatCompletionResponse) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// ChatCompletionChunk is a single SSE event payload from a streaming
// /v1/chat/completions response.
type ChatCompletionChunk struct {
	ID string `json:"id"`

	Object string `json:"object"`

	Created int64 `json:"created"`

	Model string `json:"model"`

	SystemFingerprint string `json:"system_fingerprint,omitempty"`

	ServiceTier string `json:"service_tier,omitempty"`

	Choices []ChunkChoice `json:"choices"`

	Usage *Usage `json:"usage,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *ChatCompletionChunk) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, c)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c ChatCompletionChunk) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// Choice is one of N completion candidates in a non-streaming response.
type Choice struct {
	Index int `json:"index"`

	Message ResponseMessage `json:"message"`

	FinishReason *string `json:"finish_reason"`

	LogProbs json.RawMessage `json:"logprobs,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *Choice) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c Choice) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// ChunkChoice is one of N candidate deltas in a streaming chunk.
type ChunkChoice struct {
	Index int `json:"index"`

	Delta DeltaMessage `json:"delta"`

	FinishReason *string `json:"finish_reason"`

	LogProbs json.RawMessage `json:"logprobs,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *ChunkChoice) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c ChunkChoice) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// Usage describes token accounting for a chat completion.
type Usage struct {
	PromptTokens int `json:"prompt_tokens"`

	CompletionTokens int `json:"completion_tokens"`

	TotalTokens int `json:"total_tokens"`

	PromptTokensDetails *PromptTokensDetails `json:"prompt_tokens_details,omitempty"`

	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (u *Usage) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, u) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (u Usage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(u) }

// PromptTokensDetails breaks the prompt-token total into sub-counters.
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`

	AudioTokens int `json:"audio_tokens,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (d *PromptTokensDetails) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, d)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (d PromptTokensDetails) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// CompletionTokensDetails breaks the completion-token total into sub-counters.
type CompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`

	AudioTokens int `json:"audio_tokens,omitempty"`

	AcceptedPredictionTokens int `json:"accepted_prediction_tokens,omitempty"`

	RejectedPredictionTokens int `json:"rejected_prediction_tokens,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (d *CompletionTokensDetails) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, d)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (d CompletionTokensDetails) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// StreamOptions controls streaming-specific behaviour.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (s *StreamOptions) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, s) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (s StreamOptions) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(s) }

// Tool describes a function the model may call.
type Tool struct {
	Type string `json:"type"`

	Function *ToolFunction `json:"function,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (t *Tool) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (t Tool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// ToolFunction is the nested function declaration inside a Tool.
type ToolFunction struct {
	Name string `json:"name"`

	Description string `json:"description,omitempty"`

	Parameters json.RawMessage `json:"parameters,omitempty"`

	Strict *bool `json:"strict,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (f *ToolFunction) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, f) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (f ToolFunction) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(f) }

// ResponseFormat controls structured-output enforcement.
type ResponseFormat struct {
	Type string `json:"type"`

	JSONSchema *ResponseFormatJSONSchema `json:"json_schema,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (f *ResponseFormat) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, f) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (f ResponseFormat) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(f) }

// ResponseFormatJSONSchema describes the schema the model must conform to when
// ResponseFormat.Type is "json_schema".
type ResponseFormatJSONSchema struct {
	Name string `json:"name"`

	Description string `json:"description,omitempty"`

	Schema json.RawMessage `json:"schema,omitempty"`

	Strict *bool `json:"strict,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (s *ResponseFormatJSONSchema) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, s)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (s ResponseFormatJSONSchema) MarshalJSON() ([]byte, error) {
	return models.MarshalDynamic(s)
}

// AudioOptions configures audio output modality.
type AudioOptions struct {
	Voice string `json:"voice,omitempty"`

	Format string `json:"format,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (a *AudioOptions) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, a) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (a AudioOptions) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// ResponseMessage is the assistant's reply on a non-streaming completion.
type ResponseMessage struct {
	Role string `json:"role"`

	Content json.RawMessage `json:"content,omitempty"`

	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	Refusal *string `json:"refusal,omitempty"`

	Audio *AudioMessage `json:"audio,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (m *ResponseMessage) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, m) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (m ResponseMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// AudioMessage carries the audio component of an assistant reply.
type AudioMessage struct {
	ID string `json:"id,omitempty"`

	Data string `json:"data,omitempty"`

	Transcript string `json:"transcript,omitempty"`

	ExpiresAt *int64 `json:"expires_at,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (a *AudioMessage) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, a) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (a AudioMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// DeltaMessage is the incremental message body delivered in each streaming chunk.
type DeltaMessage struct {
	Role string `json:"role,omitempty"`

	Content json.RawMessage `json:"content,omitempty"`

	ToolCalls []ToolCallDelta `json:"tool_calls,omitempty"`

	Refusal *string `json:"refusal,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (m *DeltaMessage) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, m) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (m DeltaMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// ToolCall is a single function invocation requested by the assistant.
type ToolCall struct {
	ID string `json:"id,omitempty"`

	Type string `json:"type"`

	Function ToolCallFunction `json:"function"`

	Index *int `json:"index,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *ToolCall) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c ToolCall) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// ToolCallDelta is the streaming counterpart to ToolCall — fields may arrive
// piecewise across chunks, so every field is optional and Index keys them.
type ToolCallDelta struct {
	Index int `json:"index"`

	ID string `json:"id,omitempty"`

	Type string `json:"type,omitempty"`

	Function *ToolCallFunctionDelta `json:"function,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *ToolCallDelta) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c ToolCallDelta) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// ToolCallFunction is the function name + serialized argument payload.
//
// OpenAI ships function arguments as a JSON string (not a JSON object), so
// Arguments is typed as a Go string and re-parsed by the consumer.
type ToolCallFunction struct {
	Name string `json:"name"`

	Arguments string `json:"arguments"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (f *ToolCallFunction) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, f)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (f ToolCallFunction) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(f) }

// ToolCallFunctionDelta is the streaming-chunk counterpart: name appears once,
// arguments stream as a sequence of partial strings. Arguments is emitted
// without omitempty because OpenAI emits an empty-string arguments delta on
// the first chunk of a tool call to signal "tool call begins".
type ToolCallFunctionDelta struct {
	Name string `json:"name,omitempty"`

	Arguments string `json:"arguments"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (f *ToolCallFunctionDelta) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, f)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (f ToolCallFunctionDelta) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(f) }
