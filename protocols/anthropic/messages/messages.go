package messages

import (
	"encoding/json"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// MessagesRequest is the request body for Anthropic's POST /v1/messages
// endpoint. Unknown fields round-trip via the embedded DynamicProperties.
//
// System is left as json.RawMessage because Anthropic accepts both a bare
// string and an array of SystemBlock objects in the same field; callers
// should use SystemAsString or SystemAsBlocks to read the typed form, and
// SetSystemString or SetSystemBlocks to write it.
type MessagesRequest struct {
	// Model is the Anthropic model identifier (e.g.,
	// "claude-sonnet-4-7-20250105").
	Model string `json:"model"`

	// MaxTokens caps the model's generated tokens. Required by Anthropic
	// on every request.
	MaxTokens int `json:"max_tokens"`

	// Messages is the conversation history sent to the model.
	Messages []Message `json:"messages"`

	// System is the optional system prompt: either a JSON string or an
	// array of SystemBlock objects. Kept raw so both shapes round-trip;
	// see SystemAsString/SystemAsBlocks for typed projections.
	System json.RawMessage `json:"system,omitempty"`

	// Temperature is the sampling temperature; nil means "use the server
	// default".
	Temperature *float64 `json:"temperature,omitempty"`

	// TopP is the nucleus-sampling cutoff.
	TopP *float64 `json:"top_p,omitempty"`

	// TopK caps the candidate token set considered at each sampling step.
	TopK *int `json:"top_k,omitempty"`

	// StopSequences lists substrings the model must stop at.
	StopSequences []string `json:"stop_sequences,omitempty"`

	// Stream requests SSE streaming when true.
	Stream bool `json:"stream,omitempty"`

	// Tools advertises the callable tool catalogue.
	Tools []Tool `json:"tools,omitempty"`

	// ToolChoice pins the request's tool-selection policy.
	ToolChoice *ToolChoice `json:"tool_choice,omitempty"`

	// Metadata is the optional caller-supplied metadata block.
	Metadata *Metadata `json:"metadata,omitempty"`

	// Thinking configures extended thinking on supported models.
	Thinking *ThinkingConfig `json:"thinking,omitempty"`

	// ServiceTier selects a service tier (e.g., "auto", "standard_only").
	ServiceTier string `json:"service_tier,omitempty"`

	// OutputConfig configures output controls such as reasoning effort.
	OutputConfig *OutputConfig `json:"output_config,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into r, routing any top-level field not declared
// on the struct into DynamicProperties.Extra.
func (r *MessagesRequest) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, r) }

// MarshalJSON encodes r and merges DynamicProperties.Extra back into the
// resulting object.
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

// SetSystemBlocks writes the system prompt as a JSON array of SystemBlock
// objects.
func (r *MessagesRequest) SetSystemBlocks(blocks []SystemBlock) error {
	raw, err := json.Marshal(blocks)
	if err != nil {
		return err
	}
	r.System = raw
	return nil
}

// Message is one entry in MessagesRequest.Messages. Content is left as
// json.RawMessage because Anthropic accepts both a bare string and an array
// of ContentBlock objects in the same field; use ContentAsString or
// ContentAsBlocks to read the typed form. Unknown fields round-trip via the
// embedded DynamicProperties.
type Message struct {
	// Role is the message role ("user" or "assistant").
	Role string `json:"role"`

	// Content is either a JSON string or a JSON array of ContentBlock
	// values. Kept raw so the caller picks the projection; see
	// ContentAsString/ContentAsBlocks.
	Content json.RawMessage `json:"content"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into m, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (m *Message) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, m) }

// MarshalJSON encodes m and merges DynamicProperties.Extra back into the
// resulting object.
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

// ContentAsBlocks returns the message content when it was sent as a JSON
// array of ContentBlock values, dispatching unknown block discriminators to
// UnknownBlock. ok is false when the field is empty or carries a bare
// string.
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

// SetContentBlocks writes the message content as a JSON array of
// ContentBlock values.
func (m *Message) SetContentBlocks(blocks []ContentBlock) error {
	raw, err := json.Marshal(blocks)
	if err != nil {
		return err
	}
	m.Content = raw
	return nil
}

// Tool describes a callable tool exposed to the model on a MessagesRequest.
// Unknown fields round-trip via the embedded DynamicProperties.
type Tool struct {
	// Type identifies an Anthropic-defined (pre-built) tool by its
	// versioned discriminator (e.g. "web_search_20250305",
	// "computer_20241022", "tool_search_tool_regex"). Omitted for custom
	// tools defined purely by Name + InputSchema.
	// Ref: https://platform.claude.com/docs/en/agents-and-tools/tool-use/implement-tool-use
	Type string `json:"type,omitempty"`

	// Name is the tool identifier the model invokes when calling this
	// tool.
	Name string `json:"name"`

	// Description is the natural-language description the model uses to
	// decide when to call the tool.
	Description string `json:"description,omitempty"`

	// InputSchema is the JSON-Schema document describing the tool's
	// arguments. Kept raw so callers build schemas without going through
	// a typed schema model.
	InputSchema json.RawMessage `json:"input_schema,omitempty"`

	// MaxUses caps how many times the model may invoke this tool within a
	// single request. Primarily used with Anthropic server tools
	// (web_search, code_execution, web_fetch, tool_search) to bound cost;
	// nil leaves the limit unset.
	// Ref: https://platform.claude.com/docs/en/agents-and-tools/tool-use/overview
	MaxUses *int `json:"max_uses,omitempty"`

	// CacheControl marks the tool definition as eligible for prompt
	// caching at the configured tier.
	CacheControl *CacheControl `json:"cache_control,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into t, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (t *Tool) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON encodes t and merges DynamicProperties.Extra back into the
// resulting object.
func (t Tool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// ToolChoice is the request's tool-selection policy. Type is one of "auto",
// "any", "tool", or "none"; Name is set only when Type == "tool". Unknown
// fields round-trip via the embedded DynamicProperties.
type ToolChoice struct {
	// Type selects the policy ("auto", "any", "tool", "none").
	Type string `json:"type"`

	// Name pins a specific tool when Type == "tool".
	Name string `json:"name,omitempty"`

	// DisableParallelToolUse forces sequential tool calls when true.
	DisableParallelToolUse *bool `json:"disable_parallel_tool_use,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into t, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (t *ToolChoice) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON encodes t and merges DynamicProperties.Extra back into the
// resulting object.
func (t ToolChoice) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// Metadata is the optional metadata block on a MessagesRequest. Unknown
// fields round-trip via the embedded DynamicProperties.
type Metadata struct {
	// UserID is the caller-supplied end-user identifier Anthropic uses
	// for abuse detection.
	UserID string `json:"user_id,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into m, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (m *Metadata) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, m) }

// MarshalJSON encodes m and merges DynamicProperties.Extra back into the
// resulting object.
func (m Metadata) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// ThinkingConfig configures extended thinking on supported Anthropic models.
// Unknown fields round-trip via the embedded DynamicProperties.
type ThinkingConfig struct {
	// Type is the thinking mode ("enabled", "disabled").
	Type string `json:"type"`

	// BudgetTokens caps tokens spent on thinking; only meaningful when
	// Type == "enabled".
	BudgetTokens *int `json:"budget_tokens,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into t, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (t *ThinkingConfig) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON encodes t and merges DynamicProperties.Extra back into the
// resulting object.
func (t ThinkingConfig) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// SystemBlock is one element of an array-form system prompt on a
// MessagesRequest. Unknown fields round-trip via the embedded
// DynamicProperties.
type SystemBlock struct {
	// Type is the block kind (currently always "text").
	Type string `json:"type"`

	// Text is the system block's literal text content.
	Text string `json:"text"`

	// CacheControl marks the block as eligible for prompt caching at the
	// configured tier.
	CacheControl *CacheControl `json:"cache_control,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into b, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (b *SystemBlock) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, b) }

// MarshalJSON encodes b and merges DynamicProperties.Extra back into the
// resulting object.
func (b SystemBlock) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(b) }

// OutputConfig carries Anthropic output controls on a MessagesRequest:
// the reasoning-effort hint and the structured-output Format block. Any
// further controls Anthropic ships round-trip via the embedded
// DynamicProperties until a captured sample justifies typing them.
type OutputConfig struct {
	// Effort is the reasoning-effort hint ("low", "medium", "high",
	// "xhigh", "max").
	Effort string `json:"effort,omitempty"`

	// Format constrains the model's output to a structured shape — the GA
	// successor to the deprecated top-level output_format. Currently
	// carries {"type":"json_schema","schema":{...}} for JSON-schema-
	// constrained output. Nil leaves output unconstrained.
	// Ref: https://platform.claude.com/docs/en/build-with-claude/structured-outputs
	Format *OutputFormat `json:"format,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into o, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (o *OutputConfig) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, o) }

// MarshalJSON encodes o and merges DynamicProperties.Extra back into the
// resulting object.
func (o OutputConfig) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(o) }

// OutputFormat is the structured-output constraint carried by
// OutputConfig.Format. Type selects the constraint kind (currently
// "json_schema") and Schema carries the JSON-Schema document the output must
// satisfy; Schema is kept raw so an arbitrary schema round-trips without a
// typed schema model. Unknown fields round-trip via the embedded
// DynamicProperties.
// Ref: https://platform.claude.com/docs/en/build-with-claude/structured-outputs
type OutputFormat struct {
	// Type is the constraint kind, currently always "json_schema".
	Type string `json:"type"`

	// Schema is the JSON-Schema document the output must conform to.
	Schema json.RawMessage `json:"schema,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into f, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (f *OutputFormat) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, f) }

// MarshalJSON encodes f and merges DynamicProperties.Extra back into the
// resulting object.
func (f OutputFormat) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(f) }

// CacheControl is the prompt-caching hint attached to system blocks, tools,
// and content blocks. Unknown fields round-trip via the embedded
// DynamicProperties.
type CacheControl struct {
	// Type is the cache tier (currently always "ephemeral").
	Type string `json:"type"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (c *CacheControl) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c CacheControl) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }
