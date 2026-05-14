package messages

import (
	"encoding/json"
	"fmt"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// MessagesResponse is the response shape for Anthropic's POST /v1/messages
// when the request did not opt into streaming.
type MessagesResponse struct {
	ID string `json:"id"`

	Type string `json:"type"`

	Role string `json:"role"`

	Content []ContentBlock `json:"content"`

	Model string `json:"model"`

	StopReason *string `json:"stop_reason"`

	StopSequence *string `json:"stop_sequence"`

	Usage Usage `json:"usage"`

	Container *Container `json:"container,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON dispatches Content through the polymorphic ContentBlock
// registry so unknown block discriminators round-trip via UnknownBlock, then
// routes unknown top-level fields through DynamicProperties.
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

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (r MessagesResponse) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// Usage describes token accounting for one request.
type Usage struct {
	InputTokens int `json:"input_tokens"`

	OutputTokens int `json:"output_tokens"`

	CacheCreationInputTokens *int `json:"cache_creation_input_tokens,omitempty"`

	CacheReadInputTokens *int `json:"cache_read_input_tokens,omitempty"`

	ServerToolUse *ServerToolUseUsage `json:"server_tool_use,omitempty"`

	ServiceTier string `json:"service_tier,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (u *Usage) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, u) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (u Usage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(u) }

// ServerToolUseUsage describes server-side tool calls counted against the
// request (e.g., web search).
type ServerToolUseUsage struct {
	WebSearchRequests *int `json:"web_search_requests,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (s *ServerToolUseUsage) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, s)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (s ServerToolUseUsage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(s) }

// Container describes the code-execution container (when used).
type Container struct {
	ID string `json:"id"`

	ExpiresAt string `json:"expires_at,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *Container) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c Container) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }
