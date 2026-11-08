package messages

import (
	"encoding/json"

	"github.com/andyjmorgan/slipspace-gateway/models"
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

	// ContextManagement is the request-side context-management config (BETA;
	// requires the "anthropic-beta: context-management-2025-06-27" header).
	// It carries an array of context-edit strategies (clear_tool_uses,
	// clear_thinking, compact). Distinct from the response-side
	// MessagesResponse.ContextManagement, which reports the edits the server
	// actually applied. Nil leaves context management unconfigured.
	// Ref: anthropic-sdk-python message_create_params.py — context_management:
	// Optional[BetaContextManagementConfigParam]:
	// https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/beta/message_create_params.py
	ContextManagement *ContextManagementConfig `json:"context_management,omitempty"`

	// McpServers lists the remote MCP servers the API connects to on the
	// caller's behalf (BETA; requires the "anthropic-beta:
	// mcp-client-2025-11-20" header, or the superset
	// "mcp-client-2026-09-15"). Each server must be referenced by exactly
	// one {"type":"mcp_toolset"} entry in Tools. Nil leaves the connector
	// unused.
	// Ref: https://platform.claude.com/docs/en/agents-and-tools/mcp-connector
	// Ref: anthropic-sdk-python beta_request_mcp_server_url_definition_param.py:
	// https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/beta/beta_request_mcp_server_url_definition_param.py
	McpServers []McpServerDefinition `json:"mcp_servers,omitempty"`

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
	// "computer_20241022", "tool_search_tool_regex_20251119",
	// "tool_search_tool_bm25_20251119"). Omitted for custom tools defined
	// purely by Name + InputSchema.
	// Ref: https://platform.claude.com/docs/en/agents-and-tools/tool-use/implement-tool-use
	Type string `json:"type,omitempty"`

	// Name is the tool identifier the model invokes when calling this
	// tool. Absent on a {"type":"mcp_toolset"} entry, which is addressed
	// by MCPServerName instead, so it must be omitted when empty for that
	// shape to round-trip.
	Name string `json:"name,omitempty"`

	// Description is the natural-language description the model uses to
	// decide when to call the tool.
	Description string `json:"description,omitempty"`

	// InputSchema is the JSON-Schema document describing the tool's
	// arguments. Kept raw so callers build schemas without going through
	// a typed schema model.
	InputSchema json.RawMessage `json:"input_schema,omitempty"`

	// DeferLoading marks the tool for on-demand loading through the tool
	// search tool: the definition is still sent on every request, but it
	// only enters the model's context when discovered via a tool search
	// (returned as a tool_reference block the API expands server-side).
	// Anthropic rejects requests where every tool is deferred, and a
	// deferred tool may not also carry CacheControl. Nil/false loads the
	// definition up front.
	// Ref: https://platform.claude.com/docs/en/agents-and-tools/tool-use/tool-search-tool
	DeferLoading *bool `json:"defer_loading,omitempty"`

	// MaxUses caps how many times the model may invoke this tool within a
	// single request. Primarily used with Anthropic server tools
	// (web_search, code_execution, web_fetch, tool_search) to bound cost;
	// nil leaves the limit unset.
	// Ref: https://platform.claude.com/docs/en/agents-and-tools/tool-use/overview
	MaxUses *int `json:"max_uses,omitempty"`

	// AllowedDomains restricts an Anthropic server tool (web_search /
	// web_fetch, versions 20250305+) to results from these domains only.
	// Entries are bare domains with an optional path (no scheme). Mutually
	// exclusive with BlockedDomains — the API 400s when both are set. Nil
	// leaves results unrestricted.
	// Ref: https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-search-tool
	AllowedDomains []string `json:"allowed_domains,omitempty"`

	// BlockedDomains excludes results from these domains on an Anthropic
	// server tool (web_search / web_fetch, versions 20250305+). Mutually
	// exclusive with AllowedDomains — the API 400s when both are set.
	// Ref: https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-search-tool
	BlockedDomains []string `json:"blocked_domains,omitempty"`

	// CacheControl marks the tool definition as eligible for prompt
	// caching at the configured tier.
	CacheControl *CacheControl `json:"cache_control,omitempty"`

	// MCPServerName binds a {"type":"mcp_toolset"} entry to the
	// MessagesRequest.McpServers element whose Name matches (BETA;
	// mcp-client-2025-11-20). Required on that shape — the API 400s when
	// it names no declared server, and every declared server must be bound
	// by exactly one toolset. Empty on every other tool kind.
	// Ref: https://platform.claude.com/docs/en/agents-and-tools/mcp-connector
	// Ref: anthropic-sdk-python beta_mcp_toolset_param.py:
	// https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/beta/beta_mcp_toolset_param.py
	MCPServerName string `json:"mcp_server_name,omitempty"`

	// DefaultConfig is the mcp_toolset-level configuration applied to every
	// tool the bound MCP server exposes; per-tool entries in Configs
	// override it. Nil means the server defaults (enabled, not deferred).
	// Ref: https://platform.claude.com/docs/en/agents-and-tools/mcp-connector
	DefaultConfig *McpToolConfig `json:"default_config,omitempty"`

	// Configs holds per-tool overrides on an mcp_toolset, keyed by the
	// tool name as the MCP server lists it. A name the server does not
	// expose is logged server-side but not rejected. Nil applies
	// DefaultConfig uniformly.
	// Ref: https://platform.claude.com/docs/en/agents-and-tools/mcp-connector
	Configs map[string]McpToolConfig `json:"configs,omitempty"`

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

// McpToolConfig is the per-tool configuration object used both as an
// mcp_toolset's DefaultConfig and as each value in its Configs map (BETA;
// mcp-client-2025-11-20). Both fields are pointers so an explicit false
// survives omitempty — "enabled": false is the denylist primitive, and
// collapsing it to absent would silently re-enable the tool. Unknown fields
// round-trip via the embedded DynamicProperties.
// Ref: https://platform.claude.com/docs/en/agents-and-tools/mcp-connector
// Ref: anthropic-sdk-python beta_mcp_tool_config_param.py /
// beta_mcp_tool_default_config_param.py (identical shape):
// https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/beta/beta_mcp_tool_config_param.py
type McpToolConfig struct {
	// Enabled exposes the tool to the model; the server default is true.
	Enabled *bool `json:"enabled,omitempty"`

	// DeferLoading withholds the tool's description until a tool search
	// discovers it (see Tool.DeferLoading); the server default is false.
	DeferLoading *bool `json:"defer_loading,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (c *McpToolConfig) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c McpToolConfig) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// McpServerDefinition is one element of MessagesRequest.McpServers — a remote
// MCP server the API connects to server-side (BETA; mcp-client-2025-11-20).
// Type is modelled as a plain discriminator rather than a polymorphic
// registry because "url" is the only variant Anthropic documents. Unknown
// fields round-trip via the embedded DynamicProperties.
// Ref: https://platform.claude.com/docs/en/agents-and-tools/mcp-connector
// Ref: anthropic-sdk-python beta_request_mcp_server_url_definition_param.py:
// https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/beta/beta_request_mcp_server_url_definition_param.py
type McpServerDefinition struct {
	// Type is the server definition kind; "url" is the only documented
	// value.
	Type string `json:"type"`

	// URL is the MCP server endpoint. Must be https:// and publicly
	// reachable (Streamable HTTP or SSE transport).
	URL string `json:"url"`

	// Name is the caller-chosen identifier an mcp_toolset binds to via
	// Tool.MCPServerName; unique within the request.
	Name string `json:"name"`

	// AuthorizationToken is the OAuth bearer token the API presents to the
	// MCP server. It is a credential: the gateway masks it wherever the
	// request body is projected for operators (connector Record, admin
	// live feed) via internal/contentredact.RedactBody, while the
	// verbatim bytes still reach the upstream provider.
	AuthorizationToken string `json:"authorization_token,omitempty"`

	// ToolConfiguration is the DEPRECATED per-server tool filter from the
	// superseded mcp-client-2025-04-04 beta. Accepted so callers still on
	// the old header round-trip intact; new callers express the same
	// policy through an mcp_toolset's DefaultConfig / Configs.
	// Ref: https://platform.claude.com/docs/en/agents-and-tools/mcp-connector
	// (section "Deprecated version: mcp-client-2025-04-04")
	ToolConfiguration *McpServerToolConfiguration `json:"tool_configuration,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into d, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (d *McpServerDefinition) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, d)
}

// MarshalJSON encodes d and merges DynamicProperties.Extra back into the
// resulting object.
func (d McpServerDefinition) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// McpServerToolConfiguration is the DEPRECATED mcp-client-2025-04-04 shape
// of McpServerDefinition.ToolConfiguration. Enabled is a pointer so an
// explicit false (disable every tool on the server) survives omitempty.
// Unknown fields round-trip via the embedded DynamicProperties.
// Ref: anthropic-sdk-python beta_request_mcp_server_tool_configuration_param.py:
// https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/beta/beta_request_mcp_server_tool_configuration_param.py
type McpServerToolConfiguration struct {
	// Enabled toggles every tool on the server; superseded by
	// McpToolConfig.Enabled on the toolset's DefaultConfig.
	Enabled *bool `json:"enabled,omitempty"`

	// AllowedTools is the allowlist of tool names; superseded by per-tool
	// entries in Tool.Configs.
	AllowedTools []string `json:"allowed_tools,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (c *McpServerToolConfiguration) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, c)
}

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c McpServerToolConfiguration) MarshalJSON() ([]byte, error) {
	return models.MarshalDynamic(c)
}

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
	// Type is the thinking mode ("adaptive", "enabled", "disabled").
	// "adaptive" is the primary mode on current models; "enabled" +
	// budget_tokens is deprecated on Claude 4.6 and rejected (400) on 4.7+.
	// Ref: https://platform.claude.com/docs/en/build-with-claude/thinking#configuring-thinking
	Type string `json:"type"`

	// BudgetTokens caps tokens spent on thinking; only meaningful when
	// Type == "enabled".
	BudgetTokens *int `json:"budget_tokens,omitempty"`

	// Display controls whether summarized thinking text is returned
	// ("summarized") or thinking blocks arrive with an empty thinking field
	// ("omitted"). Invalid when Type == "disabled".
	// Ref: https://platform.claude.com/docs/en/build-with-claude/thinking#controlling-thinking-display
	Display string `json:"display,omitempty"`

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
