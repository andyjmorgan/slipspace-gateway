package messages

import (
	"encoding/json"
	"fmt"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// MessagesResponse is the non-streaming response body returned by Anthropic's
// POST /v1/messages endpoint. Unknown fields round-trip via the embedded
// DynamicProperties, and the polymorphic Content array preserves unknown
// block types via UnknownBlock.
type MessagesResponse struct {
	// ID is the Anthropic-assigned message identifier.
	ID string `json:"id"`

	// Type is the Anthropic object discriminator (typically "message").
	Type string `json:"type"`

	// Role is the response role (typically "assistant").
	Role string `json:"role"`

	// Content is the assistant's content blocks; unknown block kinds
	// land in UnknownBlock so the response round-trips intact.
	Content []ContentBlock `json:"content"`

	// Model is the resolved model name Anthropic billed against.
	Model string `json:"model"`

	// StopReason is "end_turn", "max_tokens", "stop_sequence",
	// "tool_use", etc. Nil while the response is still in progress.
	StopReason *string `json:"stop_reason"`

	// StopSequence carries the stop string that triggered termination
	// when StopReason == "stop_sequence". Nil otherwise.
	StopSequence *string `json:"stop_sequence"`

	// Usage reports token accounting.
	Usage Usage `json:"usage"`

	// Container describes the code-execution sandbox when the request
	// used the code-execution tool.
	Container *Container `json:"container,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into r. The Content array is dispatched through
// the polymorphic ContentBlock registry so unknown block discriminators
// round-trip via UnknownBlock; any other unknown top-level field lands in
// DynamicProperties.Extra.
func (r *MessagesResponse) UnmarshalJSON(data []byte) error {
	var shadow messagesResponseRaw
	if err := models.UnmarshalDynamic(data, &shadow); err != nil {
		return err
	}

	*r = MessagesResponse{
		ID:                shadow.ID,
		Type:              shadow.Type,
		Role:              shadow.Role,
		Model:             shadow.Model,
		StopReason:        shadow.StopReason,
		StopSequence:      shadow.StopSequence,
		Usage:             shadow.Usage,
		Container:         shadow.Container,
		DynamicProperties: shadow.DynamicProperties,
	}

	if len(shadow.Content) == 0 || string(shadow.Content) == "null" {
		return nil
	}
	blocks, err := UnmarshalContentBlocks(shadow.Content)
	if err != nil {
		return fmt.Errorf("messages response content: %w", err)
	}
	r.Content = blocks
	return nil
}

// messagesResponseRaw mirrors MessagesResponse but keeps Content as a
// RawMessage so UnmarshalDynamic ignores its polymorphic shape; the parent
// UnmarshalJSON dispatches the content blocks afterwards.
type messagesResponseRaw struct {
	ID string `json:"id"`

	Type string `json:"type"`

	Role string `json:"role"`

	Content json.RawMessage `json:"content,omitempty"`

	Model string `json:"model"`

	StopReason *string `json:"stop_reason"`

	StopSequence *string `json:"stop_sequence"`

	Usage Usage `json:"usage"`

	Container *Container `json:"container,omitempty"`

	models.DynamicProperties
}

// MarshalJSON encodes r and merges DynamicProperties.Extra back into the
// resulting object.
func (r MessagesResponse) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// Usage describes token accounting for one Anthropic /v1/messages request.
// Unknown fields round-trip via the embedded DynamicProperties.
type Usage struct {
	// InputTokens counts tokens billed for the request input.
	InputTokens int `json:"input_tokens"`

	// OutputTokens counts tokens billed for the model's generated reply.
	OutputTokens int `json:"output_tokens"`

	// CacheCreationInputTokens counts tokens that were written into the
	// prompt cache on this request. Nil when the model did not report
	// cache activity.
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens,omitempty"`

	// CacheReadInputTokens counts tokens served from the prompt cache.
	// Nil when the model did not report cache activity.
	CacheReadInputTokens *int `json:"cache_read_input_tokens,omitempty"`

	// ServerToolUse counts server-side tool calls (e.g., web search).
	ServerToolUse *ServerToolUseUsage `json:"server_tool_use,omitempty"`

	// CacheCreation breaks prompt-cache writes down by cache TTL tier. Nil
	// when the model did not report tiered cache activity.
	CacheCreation *CacheCreation `json:"cache_creation,omitempty"`

	// OutputTokensDetails breaks the generated output tokens down (e.g. how
	// many were spent inside thinking blocks). Nil when not reported.
	OutputTokensDetails *OutputTokensDetails `json:"output_tokens_details,omitempty"`

	// InferenceGeo is the geographic region inference ran in (e.g.
	// "not_available"). Empty when the model did not report it.
	InferenceGeo string `json:"inference_geo,omitempty"`

	// ServiceTier echoes the service tier that actually served the
	// request.
	ServiceTier string `json:"service_tier,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into u, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (u *Usage) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, u) }

// MarshalJSON encodes u and merges DynamicProperties.Extra back into the
// resulting object.
func (u Usage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(u) }

// ServerToolUseUsage counts server-side tool invocations billed against the
// request (e.g., Anthropic's hosted web search). Unknown fields round-trip
// via the embedded DynamicProperties.
type ServerToolUseUsage struct {
	// WebSearchRequests counts web-search calls the model made on the
	// server.
	WebSearchRequests *int `json:"web_search_requests,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into s, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (s *ServerToolUseUsage) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, s)
}

// MarshalJSON encodes s and merges DynamicProperties.Extra back into the
// resulting object.
func (s ServerToolUseUsage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(s) }

// CacheCreation breaks prompt-cache write tokens down by cache TTL tier.
// Unknown fields round-trip via the embedded DynamicProperties.
type CacheCreation struct {
	// Ephemeral5mInputTokens counts tokens written to the 5-minute
	// ephemeral cache tier.
	Ephemeral5mInputTokens *int `json:"ephemeral_5m_input_tokens,omitempty"`

	// Ephemeral1hInputTokens counts tokens written to the 1-hour ephemeral
	// cache tier.
	Ephemeral1hInputTokens *int `json:"ephemeral_1h_input_tokens,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (c *CacheCreation) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c CacheCreation) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// OutputTokensDetails breaks the generated output token count down by category.
// Unknown fields round-trip via the embedded DynamicProperties.
type OutputTokensDetails struct {
	// ThinkingTokens counts output tokens generated inside thinking blocks,
	// including the thinking-block delimiter tokens.
	ThinkingTokens *int `json:"thinking_tokens,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into o, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (o *OutputTokensDetails) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, o)
}

// MarshalJSON encodes o and merges DynamicProperties.Extra back into the
// resulting object.
func (o OutputTokensDetails) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(o) }

// Container describes the code-execution container the request ran tools
// against (when the request used the code-execution tool). Unknown fields
// round-trip via the embedded DynamicProperties.
type Container struct {
	// ID is the Anthropic-assigned container identifier; can be re-used
	// across requests to preserve workspace state.
	ID string `json:"id"`

	// ExpiresAt is the container's expiry timestamp (RFC 3339 string).
	ExpiresAt string `json:"expires_at,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (c *Container) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c Container) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }
