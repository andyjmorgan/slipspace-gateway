// Package chat models OpenAI's POST /v1/chat/completions wire surface — the
// request body, the non-streaming response, and the streaming SSE chunk shape.
//
// Every exported struct embeds models.DynamicProperties so fields OpenAI ships
// after this package was built still round-trip when a request or response
// flows through the gateway. Polymorphic JSON (the messages array, content
// parts, tool_choice) is dispatched through hand-rolled UnmarshalX helpers
// rather than json.Unmarshal so unknown discriminator values land in the
// matching UnknownX fallback type.
package chat

import (
	"encoding/json"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// ChatCompletionRequest is the request body for OpenAI's POST
// /v1/chat/completions endpoint. Unknown fields round-trip via the embedded
// DynamicProperties.
type ChatCompletionRequest struct {
	// Model is the OpenAI model identifier to invoke (e.g., "gpt-4o").
	Model string `json:"model"`

	// Messages is the conversation history sent to the model. Each element is
	// a polymorphic RequestMessage keyed by its "role" discriminator.
	Messages RequestMessages `json:"messages"`

	// MaxTokens caps generated tokens. Deprecated by OpenAI in favour of
	// MaxCompletionTokens; both are accepted on the wire.
	MaxTokens *int `json:"max_tokens,omitempty"`

	// MaxCompletionTokens caps generated tokens on models that distinguish
	// reasoning tokens from completion tokens.
	MaxCompletionTokens *int `json:"max_completion_tokens,omitempty"`

	// Temperature is the sampling temperature; nil means "use the server default".
	Temperature *float64 `json:"temperature,omitempty"`

	// TopP is the nucleus-sampling cutoff; nil means "use the server default".
	TopP *float64 `json:"top_p,omitempty"`

	// N is the number of completions to return.
	N *int `json:"n,omitempty"`

	// Stream requests SSE streaming when true.
	Stream bool `json:"stream,omitempty"`

	// StreamOptions tunes streaming-specific behaviour and is only meaningful
	// when Stream is true.
	StreamOptions *StreamOptions `json:"stream_options,omitempty"`

	// Stop is OpenAI's polymorphic stop-sequence field: either a single string
	// or an array of strings. Kept as json.RawMessage so callers can pick the
	// representation they want.
	Stop json.RawMessage `json:"stop,omitempty"`

	// PresencePenalty biases the model against repeating content.
	PresencePenalty *float64 `json:"presence_penalty,omitempty"`

	// FrequencyPenalty biases the model against frequent tokens.
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`

	// LogitBias adjusts the sampling probability of specific token IDs.
	LogitBias map[string]int `json:"logit_bias,omitempty"`

	// LogProbs enables log-probability output on the response.
	LogProbs *bool `json:"logprobs,omitempty"`

	// TopLogProbs caps how many alternative tokens log-probabilities are
	// returned for per position.
	TopLogProbs *int `json:"top_logprobs,omitempty"`

	// User is the caller-supplied end-user identifier used by OpenAI for abuse
	// detection.
	User string `json:"user,omitempty"`

	// Tools advertises the function/tool catalogue the model may call.
	Tools []Tool `json:"tools,omitempty"`

	// ToolChoice is OpenAI's polymorphic tool-selection field: either the
	// string "auto"/"none"/"required" or an object pinning a specific tool.
	// Kept as json.RawMessage so callers can pick the representation.
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`

	// ResponseFormat constrains the output shape (e.g., JSON schema).
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`

	// Seed pins sampling for best-effort determinism.
	Seed *int `json:"seed,omitempty"`

	// Modalities lists the output modalities to enable (e.g., "text", "audio").
	Modalities []string `json:"modalities,omitempty"`

	// ReasoningEffort hints how much reasoning the model should spend on the
	// request when running on a reasoning-capable model.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`

	// ServiceTier selects a service tier (e.g., "default", "flex").
	ServiceTier string `json:"service_tier,omitempty"`

	// Audio configures audio output when "audio" appears in Modalities.
	Audio *AudioOptions `json:"audio,omitempty"`

	// Metadata is a free-form key/value bag stored alongside the request when
	// Store is true.
	Metadata map[string]string `json:"metadata,omitempty"`

	// Store opts the request into OpenAI's stored-completions feature.
	Store *bool `json:"store,omitempty"`

	// ParallelToolCalls allows the model to emit multiple tool calls per turn.
	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into r, routing any field not declared on the
// struct into DynamicProperties.Extra so it survives a subsequent MarshalJSON.
func (r *ChatCompletionRequest) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, r)
}

// MarshalJSON encodes r and merges DynamicProperties.Extra back into the
// resulting object so previously preserved unknown fields appear at the top
// level.
func (r ChatCompletionRequest) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// ChatCompletionResponse is the non-streaming response body returned by POST
// /v1/chat/completions. Unknown fields round-trip via the embedded
// DynamicProperties.
type ChatCompletionResponse struct {
	// ID is the OpenAI-assigned completion identifier.
	ID string `json:"id"`

	// Object is the OpenAI object discriminator (typically
	// "chat.completion").
	Object string `json:"object"`

	// Created is the response creation time as a Unix epoch in seconds.
	Created int64 `json:"created"`

	// Model is the resolved model name OpenAI billed the request against.
	Model string `json:"model"`

	// SystemFingerprint identifies the provider configuration that served the
	// request; useful for reproducibility when paired with Seed.
	SystemFingerprint string `json:"system_fingerprint,omitempty"`

	// ServiceTier echoes the service tier that actually served the request.
	ServiceTier string `json:"service_tier,omitempty"`

	// Choices carries one Choice per completion (length equals the request's
	// N).
	Choices []Choice `json:"choices"`

	// Usage reports token accounting. Nil when not returned (e.g., on some
	// streaming variants without StreamOptions.IncludeUsage).
	Usage *Usage `json:"usage,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into r, routing any field not declared on the
// struct into DynamicProperties.Extra so it survives a subsequent MarshalJSON.
func (r *ChatCompletionResponse) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, r)
}

// MarshalJSON encodes r and merges DynamicProperties.Extra back into the
// resulting object.
func (r ChatCompletionResponse) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// ChatCompletionChunk is one SSE event payload emitted by a streaming POST
// /v1/chat/completions response. A typical stream emits many chunks; the final
// chunk carries finish_reason on each Choice and (when requested) a Usage
// summary. Unknown fields round-trip via the embedded DynamicProperties.
type ChatCompletionChunk struct {
	// ID is the OpenAI-assigned completion identifier; identical across all
	// chunks of a single stream.
	ID string `json:"id"`

	// Object is the OpenAI object discriminator (typically
	// "chat.completion.chunk").
	Object string `json:"object"`

	// Created is the stream start time as a Unix epoch in seconds.
	Created int64 `json:"created"`

	// Model is the resolved model name.
	Model string `json:"model"`

	// SystemFingerprint identifies the provider configuration that served the
	// stream.
	SystemFingerprint string `json:"system_fingerprint,omitempty"`

	// ServiceTier echoes the service tier that actually served the stream.
	ServiceTier string `json:"service_tier,omitempty"`

	// Choices carries one ChunkChoice per concurrent completion in the
	// stream.
	Choices []ChunkChoice `json:"choices"`

	// Usage is only present on the terminal chunk when the caller set
	// StreamOptions.IncludeUsage on the request.
	Usage *Usage `json:"usage,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (c *ChatCompletionChunk) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, c)
}

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c ChatCompletionChunk) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// Choice is one of N completion candidates in a non-streaming
// ChatCompletionResponse. Unknown fields round-trip via the embedded
// DynamicProperties.
type Choice struct {
	// Index is the choice's zero-based position within Choices.
	Index int `json:"index"`

	// Message is the assistant's reply for this candidate.
	Message ResponseMessage `json:"message"`

	// FinishReason is "stop", "length", "tool_calls", "content_filter", etc.
	// Nil while the choice is still in progress (only seen on background
	// requests).
	FinishReason *string `json:"finish_reason"`

	// LogProbs carries per-token log-probability detail when
	// ChatCompletionRequest.LogProbs is true. Shape is provider-defined; kept
	// raw so future changes round-trip.
	LogProbs json.RawMessage `json:"logprobs,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (c *Choice) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c Choice) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// ChunkChoice is one candidate's incremental update inside a
// ChatCompletionChunk. Delta carries only the text/tool-call fragment added by
// this chunk; the receiver concatenates the deltas across all chunks for the
// same Index to reconstruct the final ResponseMessage. Unknown fields
// round-trip via the embedded DynamicProperties.
type ChunkChoice struct {
	// Index is the choice's zero-based position within Choices.
	Index int `json:"index"`

	// Delta is the incremental message body for this chunk; populated only on
	// streaming responses.
	Delta DeltaMessage `json:"delta"`

	// FinishReason is non-nil only on the terminal chunk for this choice.
	FinishReason *string `json:"finish_reason"`

	// LogProbs carries per-token log-probability detail when
	// ChatCompletionRequest.LogProbs is true.
	LogProbs json.RawMessage `json:"logprobs,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (c *ChunkChoice) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c ChunkChoice) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// Usage describes token accounting for one chat completion request/response
// pair. Unknown fields round-trip via the embedded DynamicProperties.
type Usage struct {
	// PromptTokens counts tokens billed for the request prompt.
	PromptTokens int `json:"prompt_tokens"`

	// CompletionTokens counts tokens billed for the model's generated reply.
	CompletionTokens int `json:"completion_tokens"`

	// TotalTokens is PromptTokens + CompletionTokens (mirrored by the
	// provider).
	TotalTokens int `json:"total_tokens"`

	// PromptTokensDetails breaks the prompt total into sub-counters (cached,
	// audio, etc.) when the model reports them.
	PromptTokensDetails *PromptTokensDetails `json:"prompt_tokens_details,omitempty"`

	// CompletionTokensDetails breaks the completion total into sub-counters
	// (reasoning, audio, accepted/rejected predictions).
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into u, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (u *Usage) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, u) }

// MarshalJSON encodes u and merges DynamicProperties.Extra back into the
// resulting object.
func (u Usage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(u) }

// PromptTokensDetails breaks the Usage.PromptTokens total into sub-counters.
// Unknown fields round-trip via the embedded DynamicProperties.
type PromptTokensDetails struct {
	// CachedTokens is the share of PromptTokens served from the prompt cache.
	CachedTokens int `json:"cached_tokens,omitempty"`

	// AudioTokens is the share of PromptTokens that were audio input.
	AudioTokens int `json:"audio_tokens,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into d, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (d *PromptTokensDetails) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, d)
}

// MarshalJSON encodes d and merges DynamicProperties.Extra back into the
// resulting object.
func (d PromptTokensDetails) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// CompletionTokensDetails breaks the Usage.CompletionTokens total into
// sub-counters. Unknown fields round-trip via the embedded DynamicProperties.
type CompletionTokensDetails struct {
	// ReasoningTokens is the share of CompletionTokens consumed by the
	// model's hidden reasoning trace (reasoning models only).
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`

	// AudioTokens is the share of CompletionTokens emitted as audio.
	AudioTokens int `json:"audio_tokens,omitempty"`

	// AcceptedPredictionTokens counts predicted-output tokens the model
	// accepted (predicted-output feature).
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens,omitempty"`

	// RejectedPredictionTokens counts predicted-output tokens the model
	// rejected and regenerated.
	RejectedPredictionTokens int `json:"rejected_prediction_tokens,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into d, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (d *CompletionTokensDetails) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, d)
}

// MarshalJSON encodes d and merges DynamicProperties.Extra back into the
// resulting object.
func (d CompletionTokensDetails) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// StreamOptions controls streaming-specific behaviour on a
// ChatCompletionRequest. Only meaningful when ChatCompletionRequest.Stream is
// true. Unknown fields round-trip via the embedded DynamicProperties.
type StreamOptions struct {
	// IncludeUsage asks OpenAI to emit a final chunk carrying Usage once the
	// stream completes.
	IncludeUsage bool `json:"include_usage,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into s, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (s *StreamOptions) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, s) }

// MarshalJSON encodes s and merges DynamicProperties.Extra back into the
// resulting object.
func (s StreamOptions) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(s) }

// Tool describes one tool exposed to the model on a ChatCompletionRequest.
// Today OpenAI only ships Type == "function"; Tool is left open-shaped so a
// new tool kind round-trips even before this package learns about it. Unknown
// fields round-trip via the embedded DynamicProperties.
type Tool struct {
	// Type is the tool discriminator (currently always "function").
	Type string `json:"type"`

	// Function declares the callable when Type == "function".
	Function *ToolFunction `json:"function,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into t, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (t *Tool) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON encodes t and merges DynamicProperties.Extra back into the
// resulting object.
func (t Tool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// ToolFunction is the nested function declaration inside a Tool with
// Type == "function". Unknown fields round-trip via the embedded
// DynamicProperties.
type ToolFunction struct {
	// Name is the function identifier the model invokes when calling this
	// tool.
	Name string `json:"name"`

	// Description is the natural-language description the model uses to
	// decide when to call the function.
	Description string `json:"description,omitempty"`

	// Parameters is the JSON-Schema document describing the function's
	// arguments. Kept raw so callers can build/inspect schemas without going
	// through a typed schema model.
	Parameters json.RawMessage `json:"parameters,omitempty"`

	// Strict requests strict JSON-Schema enforcement on the model's
	// generated arguments.
	Strict *bool `json:"strict,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into f, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (f *ToolFunction) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, f) }

// MarshalJSON encodes f and merges DynamicProperties.Extra back into the
// resulting object.
func (f ToolFunction) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(f) }

// ResponseFormat constrains the model's output shape — plain text, a JSON
// object, or a strict JSON-Schema-validated payload. Unknown fields round-trip
// via the embedded DynamicProperties.
type ResponseFormat struct {
	// Type is "text", "json_object", or "json_schema".
	Type string `json:"type"`

	// JSONSchema declares the schema the model must match when Type ==
	// "json_schema".
	JSONSchema *ResponseFormatJSONSchema `json:"json_schema,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into f, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (f *ResponseFormat) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, f) }

// MarshalJSON encodes f and merges DynamicProperties.Extra back into the
// resulting object.
func (f ResponseFormat) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(f) }

// ResponseFormatJSONSchema describes the schema the model must conform to when
// ResponseFormat.Type is "json_schema". Unknown fields round-trip via the
// embedded DynamicProperties.
type ResponseFormatJSONSchema struct {
	// Name is the schema identifier surfaced in errors and logs.
	Name string `json:"name"`

	// Description is the natural-language description the model uses to
	// understand the schema's intent.
	Description string `json:"description,omitempty"`

	// Schema is the JSON-Schema document the response must validate against.
	// Kept raw so callers can build schemas without a typed schema model.
	Schema json.RawMessage `json:"schema,omitempty"`

	// Strict requests strict validation; when true, the model is constrained
	// to emit a payload that validates against Schema.
	Strict *bool `json:"strict,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into s, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (s *ResponseFormatJSONSchema) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, s)
}

// MarshalJSON encodes s and merges DynamicProperties.Extra back into the
// resulting object.
func (s ResponseFormatJSONSchema) MarshalJSON() ([]byte, error) {
	return models.MarshalDynamic(s)
}

// AudioOptions configures audio output on a ChatCompletionRequest when "audio"
// appears in Modalities. Unknown fields round-trip via the embedded
// DynamicProperties.
type AudioOptions struct {
	// Voice selects the synthesised voice (e.g., "alloy").
	Voice string `json:"voice,omitempty"`

	// Format selects the audio container/codec (e.g., "wav", "mp3", "pcm16").
	Format string `json:"format,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into a, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (a *AudioOptions) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, a) }

// MarshalJSON encodes a and merges DynamicProperties.Extra back into the
// resulting object.
func (a AudioOptions) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// ResponseMessage is the assistant's reply for a single Choice in a
// non-streaming ChatCompletionResponse. Content may be a JSON string or an
// array of polymorphic content parts; both shapes round-trip via the raw
// json.RawMessage. Unknown fields round-trip via the embedded
// DynamicProperties.
type ResponseMessage struct {
	// Role is the reply role (typically "assistant").
	Role string `json:"role"`

	// Content carries the assistant's textual reply. It may be a JSON string
	// or a JSON array of polymorphic ContentPart values; kept raw so the
	// caller picks the projection they want.
	Content json.RawMessage `json:"content,omitempty"`

	// ToolCalls lists tool invocations the assistant chose to make this turn.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// Refusal carries the assistant's refusal message when the model declined
	// to answer. Nil when not refused.
	Refusal *string `json:"refusal,omitempty"`

	// Reasoning carries the model's surfaced chain-of-thought / reasoning text
	// on OpenAI-compatible providers (gpt-oss, vLLM, DeepSeek, Ollama qwen3).
	// This is an OpenAI-compat convention, NOT native OpenAI Chat Completions —
	// native reasoning models surface summaries via the Responses API's
	// reasoning.summary instead. The wire field migrated from
	// "reasoning_content" to "reasoning" in vLLM; we model the current
	// "reasoning" spelling and let any older "reasoning_content" sibling
	// round-trip via DynamicProperties. Pointer so an explicit empty string is
	// preserved distinctly from an absent field.
	// Sources: vLLM reasoning-outputs docs
	// (https://docs.vllm.ai/en/latest/features/reasoning_outputs.html);
	// OpenAI reasoning-models guide
	// (https://platform.openai.com/docs/guides/reasoning); LangChain issue #35059.
	Reasoning *string `json:"reasoning,omitempty"`

	// Audio carries the assistant's audio reply when audio modality is
	// enabled.
	Audio *AudioMessage `json:"audio,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into m, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (m *ResponseMessage) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, m) }

// MarshalJSON encodes m and merges DynamicProperties.Extra back into the
// resulting object.
func (m ResponseMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// AudioMessage carries the audio component of an assistant reply. Unknown
// fields round-trip via the embedded DynamicProperties.
type AudioMessage struct {
	// ID is the OpenAI-assigned audio identifier; can be passed back in a
	// subsequent turn instead of re-sending Data.
	ID string `json:"id,omitempty"`

	// Data is the base64-encoded audio payload.
	Data string `json:"data,omitempty"`

	// Transcript is the textual transcript of the audio reply.
	Transcript string `json:"transcript,omitempty"`

	// ExpiresAt is the Unix epoch in seconds at which ID stops being valid
	// for replay.
	ExpiresAt *int64 `json:"expires_at,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into a, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (a *AudioMessage) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, a) }

// MarshalJSON encodes a and merges DynamicProperties.Extra back into the
// resulting object.
func (a AudioMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// DeltaMessage is the incremental message body delivered in a streaming
// ChunkChoice. Every field is optional because different chunks carry
// different pieces — Role appears in the first chunk, Content streams across
// many, ToolCalls arrive piecewise via ToolCallDelta. Unknown fields
// round-trip via the embedded DynamicProperties.
type DeltaMessage struct {
	// Role is set on the first chunk of the choice and absent thereafter.
	Role string `json:"role,omitempty"`

	// Content carries the text fragment for this chunk. Same string-or-array
	// polymorphism as ResponseMessage.Content.
	Content json.RawMessage `json:"content,omitempty"`

	// ToolCalls carries the incremental tool-call updates for this chunk;
	// each entry's Index keys into the eventual assembled tool call list.
	ToolCalls []ToolCallDelta `json:"tool_calls,omitempty"`

	// Refusal carries the assistant's refusal message when the model declined
	// mid-stream.
	Refusal *string `json:"refusal,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into m, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (m *DeltaMessage) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, m) }

// MarshalJSON encodes m and merges DynamicProperties.Extra back into the
// resulting object.
func (m DeltaMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// ToolCall is a single function invocation requested by the assistant in a
// non-streaming ResponseMessage. Unknown fields round-trip via the embedded
// DynamicProperties.
type ToolCall struct {
	// ID is the OpenAI-assigned call identifier; echoed back on the
	// corresponding ToolMessage to correlate the result.
	ID string `json:"id,omitempty"`

	// Type is the tool kind (currently always "function").
	Type string `json:"type"`

	// Function carries the function name + serialized argument payload.
	Function ToolCallFunction `json:"function"`

	// Index is the call's position when ParallelToolCalls is on. Only
	// populated on a small number of OpenAI response variants; preserved so
	// the field round-trips.
	Index *int `json:"index,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (c *ToolCall) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c ToolCall) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// ToolCallDelta is the streaming counterpart to ToolCall — fields may arrive
// piecewise across chunks, so every field is optional and Index keys
// successive deltas to the same call. Unknown fields round-trip via the
// embedded DynamicProperties.
type ToolCallDelta struct {
	// Index keys successive deltas of the same tool call across chunks.
	Index int `json:"index"`

	// ID is set on the first chunk for the call and absent on subsequent
	// chunks.
	ID string `json:"id,omitempty"`

	// Type is set on the first chunk for the call and absent on subsequent
	// chunks.
	Type string `json:"type,omitempty"`

	// Function carries the name + argument-string fragment for this chunk.
	Function *ToolCallFunctionDelta `json:"function,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (c *ToolCallDelta) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c ToolCallDelta) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// ToolCallFunction is the function name + serialized argument payload on a
// ToolCall. Unknown fields round-trip via the embedded DynamicProperties.
//
// OpenAI ships function Arguments as a JSON string (not a JSON object), so
// Arguments is typed as a Go string here and the caller is expected to
// json.Unmarshal it to recover the structured payload.
type ToolCallFunction struct {
	// Name is the function identifier the assistant chose to invoke.
	Name string `json:"name"`

	// Arguments is the function arguments encoded as a JSON string (not a
	// JSON object). Callers must json.Unmarshal Arguments to recover the
	// structured payload.
	Arguments string `json:"arguments"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into f, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (f *ToolCallFunction) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, f)
}

// MarshalJSON encodes f and merges DynamicProperties.Extra back into the
// resulting object.
func (f ToolCallFunction) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(f) }

// ToolCallFunctionDelta is the streaming-chunk counterpart to
// ToolCallFunction. Name appears in the first chunk only; Arguments streams
// as a sequence of partial strings the receiver concatenates. Unknown fields
// round-trip via the embedded DynamicProperties.
//
// Arguments is emitted without omitempty because OpenAI emits an
// empty-string Arguments delta on the first chunk of a tool call to signal
// "tool call begins" — dropping that empty string would lose the start
// marker.
type ToolCallFunctionDelta struct {
	// Name is set on the first chunk of the tool call and absent thereafter.
	Name string `json:"name,omitempty"`

	// Arguments is the next argument-string fragment. Always present (even
	// when empty) so the start-of-call marker round-trips.
	Arguments string `json:"arguments"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into f, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (f *ToolCallFunctionDelta) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, f)
}

// MarshalJSON encodes f and merges DynamicProperties.Extra back into the
// resulting object.
func (f ToolCallFunctionDelta) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(f) }
