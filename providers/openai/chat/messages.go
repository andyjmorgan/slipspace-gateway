package chat

import (
	"github.com/andyjmorgan/sluice-gateway/models"
)

// RequestMessage is implemented by every concrete request-message role and by
// UnknownRequestMessage. The discriminator is the "role" field rather than
// "type" — this is the only place in the OpenAI surface where role is the
// polymorphic key.
type RequestMessage interface {
	Role() string

	isRequestMessage()
}

// SystemMessage is the "system" role request message.
type SystemMessage struct {
	RoleField string `json:"role"`

	Content string `json:"content"`

	Name string `json:"name,omitempty"`

	models.DynamicProperties
}

// Role returns the "system" discriminator.
func (SystemMessage) Role() string { return "system" }

func (SystemMessage) isRequestMessage() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (m *SystemMessage) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, m) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (m SystemMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// DeveloperMessage is the "developer" role request message (preferred over
// "system" on newer OpenAI models).
type DeveloperMessage struct {
	RoleField string `json:"role"`

	Content string `json:"content"`

	Name string `json:"name,omitempty"`

	models.DynamicProperties
}

// Role returns the "developer" discriminator.
func (DeveloperMessage) Role() string { return "developer" }

func (DeveloperMessage) isRequestMessage() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (m *DeveloperMessage) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, m)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (m DeveloperMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// UserMessage is the "user" role request message. Content may be a plain
// string or an array of polymorphic content parts; both are preserved verbatim
// as a json.RawMessage so the caller can pick the representation they want.
type UserMessage struct {
	RoleField string `json:"role"`

	Content MessageContent `json:"content"`

	Name string `json:"name,omitempty"`

	models.DynamicProperties
}

// Role returns the "user" discriminator.
func (UserMessage) Role() string { return "user" }

func (UserMessage) isRequestMessage() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (m *UserMessage) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, m) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (m UserMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// AssistantMessage is the "assistant" role request message — a prior
// assistant turn replayed back to the model.
type AssistantMessage struct {
	RoleField string `json:"role"`

	Content MessageContent `json:"content,omitempty"`

	Name string `json:"name,omitempty"`

	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	Refusal *string `json:"refusal,omitempty"`

	Audio *AudioMessage `json:"audio,omitempty"`

	models.DynamicProperties
}

// Role returns the "assistant" discriminator.
func (AssistantMessage) Role() string { return "assistant" }

func (AssistantMessage) isRequestMessage() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (m *AssistantMessage) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, m)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (m AssistantMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// ToolMessage is the "tool" role request message — the result of a tool call
// the assistant requested in a prior turn.
type ToolMessage struct {
	RoleField string `json:"role"`

	Content MessageContent `json:"content"`

	ToolCallID string `json:"tool_call_id"`

	models.DynamicProperties
}

// Role returns the "tool" discriminator.
func (ToolMessage) Role() string { return "tool" }

func (ToolMessage) isRequestMessage() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (m *ToolMessage) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, m) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (m ToolMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// FunctionMessage is the legacy "function" role request message, retained for
// backwards compatibility with clients that have not migrated to tool_calls.
type FunctionMessage struct {
	RoleField string `json:"role"`

	Content MessageContent `json:"content"`

	Name string `json:"name"`

	models.DynamicProperties
}

// Role returns the "function" discriminator.
func (FunctionMessage) Role() string { return "function" }

func (FunctionMessage) isRequestMessage() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (m *FunctionMessage) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, m)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (m FunctionMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// UnknownRequestMessage preserves any request message whose role we have not
// modelled. RoleField carries the unknown discriminator and every other JSON
// field lands in DynamicProperties.Extra so the message round-trips intact.
type UnknownRequestMessage struct {
	RoleField string `json:"role"`

	models.DynamicProperties
}

// Role returns the unknown discriminator value verbatim.
func (m UnknownRequestMessage) Role() string { return m.RoleField }

func (UnknownRequestMessage) isRequestMessage() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (m *UnknownRequestMessage) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, m)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (m UnknownRequestMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// RequestMessages is a JSON-array-shaped slice of polymorphic RequestMessage
// values. The slice type carries its own UnmarshalJSON so the registry
// dispatch happens automatically wherever RequestMessages appears as a
// field type.
type RequestMessages []RequestMessage

// UnmarshalJSON dispatches each element through the request-message registry.
func (m *RequestMessages) UnmarshalJSON(data []byte) error {
	msgs, err := UnmarshalRequestMessages(data)
	if err != nil {
		return err
	}
	*m = msgs
	return nil
}

var requestMessageRegistry = models.PolymorphicRegistry[RequestMessage]{
	DiscriminatorField: "role",
	Factories: map[string]func() RequestMessage{
		"system":    func() RequestMessage { return &SystemMessage{} },
		"developer": func() RequestMessage { return &DeveloperMessage{} },
		"user":      func() RequestMessage { return &UserMessage{} },
		"assistant": func() RequestMessage { return &AssistantMessage{} },
		"tool":      func() RequestMessage { return &ToolMessage{} },
		"function":  func() RequestMessage { return &FunctionMessage{} },
	},
	Fallback: func(disc string) RequestMessage { return &UnknownRequestMessage{RoleField: disc} },
}

// UnmarshalRequestMessage decodes a single chat-completions request message,
// dispatching on the "role" discriminator and falling back to
// UnknownRequestMessage for any unrecognised value.
func UnmarshalRequestMessage(data []byte) (RequestMessage, error) {
	return requestMessageRegistry.UnmarshalOne(data)
}

// UnmarshalRequestMessages decodes a JSON array of chat-completions request
// messages.
func UnmarshalRequestMessages(data []byte) ([]RequestMessage, error) {
	return requestMessageRegistry.UnmarshalSlice(data)
}
