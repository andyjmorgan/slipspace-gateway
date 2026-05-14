package messages

import (
	"encoding/json"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// MessagesRequest is the request shape for Anthropic's POST /v1/messages.
//
// System is left as json.RawMessage because Anthropic accepts both a bare
// string and an array of SystemBlock objects in the same field; callers should
// use SystemAsString or SystemAsBlocks to read the typed form, and
// SetSystemString or SetSystemBlocks to write it.
type MessagesRequest struct {
	Model string `json:"model"`

	MaxTokens int `json:"max_tokens"`

	Messages []Message `json:"messages"`

	System json.RawMessage `json:"system,omitempty"`

	Temperature *float64 `json:"temperature,omitempty"`

	TopP *float64 `json:"top_p,omitempty"`

	TopK *int `json:"top_k,omitempty"`

	StopSequences []string `json:"stop_sequences,omitempty"`

	Stream bool `json:"stream,omitempty"`

	Tools []Tool `json:"tools,omitempty"`

	ToolChoice *ToolChoice `json:"tool_choice,omitempty"`

	Metadata *Metadata `json:"metadata,omitempty"`

	Thinking *ThinkingConfig `json:"thinking,omitempty"`

	ServiceTier string `json:"service_tier,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown top-level fields through DynamicProperties.
func (r *MessagesRequest) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, r) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (r MessagesRequest) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// SystemAsString returns the system prompt when it was sent as a bare JSON
// string. ok is false when the field is empty or carries an array instead.
func (r MessagesRequest) SystemAsString() (s string, ok bool) {
	if len(r.System) == 0 {
		return "", false
	}
	if err := json.Unmarshal(r.System, &s); err != nil {
		return "", false
	}
	return s, true
}

// SystemAsBlocks returns the system prompt when it was sent as an array of
// SystemBlock objects. ok is false when the field is empty or carries a bare
// string.
func (r MessagesRequest) SystemAsBlocks() (blocks []SystemBlock, ok bool) {
	if len(r.System) == 0 {
		return nil, false
	}
	if err := json.Unmarshal(r.System, &blocks); err != nil {
		return nil, false
	}
	return blocks, true
}

// SetSystemString writes the system prompt as a bare JSON string.
func (r *MessagesRequest) SetSystemString(s string) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	r.System = raw
	return nil
}

// SetSystemBlocks writes the system prompt as an array of SystemBlock objects.
func (r *MessagesRequest) SetSystemBlocks(blocks []SystemBlock) error {
	raw, err := json.Marshal(blocks)
	if err != nil {
		return err
	}
	r.System = raw
	return nil
}

// Message is one entry in MessagesRequest.Messages. Content is left as
// json.RawMessage because Anthropic accepts both a bare string and an array of
// ContentBlock objects in the same field; use ContentAsString or
// ContentAsBlocks to read the typed form.
type Message struct {
	Role string `json:"role"`

	Content json.RawMessage `json:"content"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (m *Message) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, m) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (m Message) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// ContentAsString returns the message content when it was sent as a bare JSON
// string. ok is false when the field is empty or carries an array instead.
func (m Message) ContentAsString() (s string, ok bool) {
	if len(m.Content) == 0 {
		return "", false
	}
	if err := json.Unmarshal(m.Content, &s); err != nil {
		return "", false
	}
	return s, true
}

// ContentAsBlocks returns the message content when it was sent as an array of
// content blocks. ok is false when the field is empty or carries a bare
// string. Unknown block discriminators round-trip via UnknownBlock.
func (m Message) ContentAsBlocks() (blocks []ContentBlock, ok bool) {
	if len(m.Content) == 0 || m.Content[0] != '[' {
		return nil, false
	}
	parsed, err := UnmarshalContentBlocks(m.Content)
	if err != nil {
		return nil, false
	}
	return parsed, true
}

// SetContentString writes the message content as a bare JSON string.
func (m *Message) SetContentString(s string) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	m.Content = raw
	return nil
}

// SetContentBlocks writes the message content as an array of content blocks.
func (m *Message) SetContentBlocks(blocks []ContentBlock) error {
	raw, err := json.Marshal(blocks)
	if err != nil {
		return err
	}
	m.Content = raw
	return nil
}

// Tool describes a callable tool exposed to the model.
type Tool struct {
	Name string `json:"name"`

	Description string `json:"description,omitempty"`

	InputSchema json.RawMessage `json:"input_schema,omitempty"`

	CacheControl *CacheControl `json:"cache_control,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (t *Tool) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (t Tool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// ToolChoice is the request's tool selection policy. Type is one of "auto",
// "any", "tool", or "none"; Name is set only when Type == "tool".
type ToolChoice struct {
	Type string `json:"type"`

	Name string `json:"name,omitempty"`

	DisableParallelToolUse *bool `json:"disable_parallel_tool_use,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (t *ToolChoice) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (t ToolChoice) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// Metadata is the optional metadata block on a request.
type Metadata struct {
	UserID string `json:"user_id,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (m *Metadata) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, m) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (m Metadata) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// ThinkingConfig configures extended thinking on supported models.
type ThinkingConfig struct {
	Type string `json:"type"`

	BudgetTokens *int `json:"budget_tokens,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (t *ThinkingConfig) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (t ThinkingConfig) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// SystemBlock is one element of an array-form system prompt.
type SystemBlock struct {
	Type string `json:"type"`

	Text string `json:"text"`

	CacheControl *CacheControl `json:"cache_control,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (b *SystemBlock) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, b) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (b SystemBlock) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(b) }

// CacheControl is the prompt-caching hint attached to system blocks, tools,
// and content blocks.
type CacheControl struct {
	Type string `json:"type"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *CacheControl) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c CacheControl) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }
