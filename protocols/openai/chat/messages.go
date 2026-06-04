package chat

import (
	"github.com/andyjmorgan/sluice-gateway/models"
)

// RequestMessage is the polymorphic interface implemented by every concrete
// request-message role on ChatCompletionRequest.Messages — SystemMessage,
// DeveloperMessage, UserMessage, AssistantMessage, ToolMessage,
// FunctionMessage, and the UnknownRequestMessage fallback.
//
// The discriminator is the "role" field rather than "type" — chat-completions
// is the only place in the OpenAI surface where role is the polymorphic key.
// Unknown role values are dispatched to UnknownRequestMessage, which preserves
// both the discriminator value AND any sibling JSON fields via
// DynamicProperties so the payload round-trips intact.
//
// Decode a JSON message array via UnmarshalRequestMessages (or let the typed
// RequestMessages slice do it for you). Calling json.Unmarshal directly into
// a []RequestMessage drops type information because Go cannot pick the
// concrete type from a bare interface — the registry dispatch is what makes
// the round-trip work.
type RequestMessage interface {
	// Role returns the wire discriminator value of the concrete message
	// type ("system", "user", ...).
	Role() string

	isRequestMessage()
}

// SystemMessage is the "system" role request message — instructions or
// persona priming sent ahead of the conversation. Unknown fields round-trip
// via the embedded DynamicProperties.
type SystemMessage struct {
	// RoleField is the wire "role" discriminator, always "system" on this
	// concrete type. Stored explicitly so RoleField round-trips with the
	// rest of the struct rather than being injected by MarshalJSON.
	RoleField string `json:"role"`

	// Content is the system prompt text.
	Content string `json:"content"`

	// Name is an optional caller-supplied identifier for the system message.
	Name string `json:"name,omitempty"`

	models.DynamicProperties
}

// Role returns the "system" discriminator.
func (SystemMessage) Role() string { return "system" }

func (SystemMessage) isRequestMessage() {}

// UnmarshalJSON decodes data into m, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (m *SystemMessage) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, m) }

// MarshalJSON encodes m and merges DynamicProperties.Extra back into the
// resulting object.
func (m SystemMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// DeveloperMessage is the "developer" role request message — the OpenAI
// replacement for SystemMessage on newer models. Unknown fields round-trip
// via the embedded DynamicProperties.
type DeveloperMessage struct {
	// RoleField is the wire "role" discriminator, always "developer".
	RoleField string `json:"role"`

	// Content is the developer prompt text.
	Content string `json:"content"`

	// Name is an optional caller-supplied identifier for the message.
	Name string `json:"name,omitempty"`

	models.DynamicProperties
}

// Role returns the "developer" discriminator.
func (DeveloperMessage) Role() string { return "developer" }

func (DeveloperMessage) isRequestMessage() {}

// UnmarshalJSON decodes data into m, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (m *DeveloperMessage) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, m)
}

// MarshalJSON encodes m and merges DynamicProperties.Extra back into the
// resulting object.
func (m DeveloperMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// UserMessage is the "user" role request message — a turn from the end user.
// Content may be a plain string or an array of polymorphic content parts;
// both shapes are preserved verbatim by MessageContent so the caller can pick
// the projection they want (see MessageContent.AsString, AsParts). Unknown
// fields round-trip via the embedded DynamicProperties.
type UserMessage struct {
	// RoleField is the wire "role" discriminator, always "user".
	RoleField string `json:"role"`

	// Content carries the user's message — either a plain string or an
	// array of ContentPart values, retained verbatim by MessageContent.
	Content MessageContent `json:"content"`

	// Name is an optional caller-supplied identifier for the end user.
	Name string `json:"name,omitempty"`

	models.DynamicProperties
}

// Role returns the "user" discriminator.
func (UserMessage) Role() string { return "user" }

func (UserMessage) isRequestMessage() {}

// UnmarshalJSON decodes data into m, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (m *UserMessage) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, m) }

// MarshalJSON encodes m and merges DynamicProperties.Extra back into the
// resulting object.
func (m UserMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// AssistantMessage is the "assistant" role request message — a prior
// assistant turn replayed back to the model in conversation history. Unknown
// fields round-trip via the embedded DynamicProperties.
type AssistantMessage struct {
	// RoleField is the wire "role" discriminator, always "assistant".
	RoleField string `json:"role"`

	// Content carries the prior reply text. Same string-or-array
	// polymorphism as UserMessage.Content.
	Content MessageContent `json:"content,omitempty"`

	// Name is an optional caller-supplied identifier.
	Name string `json:"name,omitempty"`

	// ToolCalls re-asserts the tool invocations the assistant chose in this
	// prior turn so the model can correlate them with the subsequent
	// ToolMessage results.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// Refusal carries the assistant's refusal message from the prior turn.
	Refusal *string `json:"refusal,omitempty"`

	// Audio carries the assistant's audio reply from the prior turn.
	Audio *AudioMessage `json:"audio,omitempty"`

	models.DynamicProperties
}

// Role returns the "assistant" discriminator.
func (AssistantMessage) Role() string { return "assistant" }

func (AssistantMessage) isRequestMessage() {}

// UnmarshalJSON decodes data into m, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (m *AssistantMessage) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, m)
}

// MarshalJSON encodes m and merges DynamicProperties.Extra back into the
// resulting object.
func (m AssistantMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// ToolMessage is the "tool" role request message — the result of a tool call
// the assistant requested in a prior turn. Unknown fields round-trip via the
// embedded DynamicProperties.
type ToolMessage struct {
	// RoleField is the wire "role" discriminator, always "tool".
	RoleField string `json:"role"`

	// Content carries the tool's output text. Same string-or-array
	// polymorphism as UserMessage.Content.
	Content MessageContent `json:"content"`

	// ToolCallID echoes the ID of the ToolCall this message answers so the
	// model can correlate the result with the request.
	ToolCallID string `json:"tool_call_id"`

	models.DynamicProperties
}

// Role returns the "tool" discriminator.
func (ToolMessage) Role() string { return "tool" }

func (ToolMessage) isRequestMessage() {}

// UnmarshalJSON decodes data into m, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (m *ToolMessage) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, m) }

// MarshalJSON encodes m and merges DynamicProperties.Extra back into the
// resulting object.
func (m ToolMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// FunctionMessage is the legacy "function" role request message, retained for
// backwards compatibility with clients that have not migrated to ToolMessage.
// Unknown fields round-trip via the embedded DynamicProperties.
type FunctionMessage struct {
	// RoleField is the wire "role" discriminator, always "function".
	RoleField string `json:"role"`

	// Content carries the legacy function's output text.
	Content MessageContent `json:"content"`

	// Name is the function identifier this message corresponds to.
	Name string `json:"name"`

	models.DynamicProperties
}

// Role returns the "function" discriminator.
func (FunctionMessage) Role() string { return "function" }

func (FunctionMessage) isRequestMessage() {}

// UnmarshalJSON decodes data into m, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (m *FunctionMessage) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, m)
}

// MarshalJSON encodes m and merges DynamicProperties.Extra back into the
// resulting object.
func (m FunctionMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// UnknownRequestMessage preserves any request message whose "role" value this
// package has not modelled. RoleField carries the unknown discriminator
// verbatim and every other JSON field lands in DynamicProperties.Extra so the
// message round-trips intact.
type UnknownRequestMessage struct {
	// RoleField is the unmodelled wire "role" discriminator, preserved
	// verbatim.
	RoleField string `json:"role"`

	models.DynamicProperties
}

// Role returns the unknown discriminator value verbatim.
func (m UnknownRequestMessage) Role() string { return m.RoleField }

func (UnknownRequestMessage) isRequestMessage() {}

// UnmarshalJSON decodes data into m, routing every non-role field into
// DynamicProperties.Extra so the unknown message round-trips byte-equivalent.
func (m *UnknownRequestMessage) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, m)
}

// MarshalJSON encodes m and merges DynamicProperties.Extra back into the
// resulting object.
func (m UnknownRequestMessage) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// RequestMessages is a JSON-array-shaped slice of polymorphic RequestMessage
// values. The slice type carries its own UnmarshalJSON so the registry
// dispatch happens automatically wherever RequestMessages appears as a field
// type — callers do not need to invoke UnmarshalRequestMessages manually.
type RequestMessages []RequestMessage

// UnmarshalJSON decodes a JSON array of request messages by delegating to
// UnmarshalRequestMessages so each element is dispatched on its "role"
// discriminator (falling back to UnknownRequestMessage).
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

// UnmarshalRequestMessage decodes a single chat-completions request message
// JSON object, dispatching on the "role" discriminator. Any role this package
// has not modelled is returned as an UnknownRequestMessage so the payload
// still round-trips.
func UnmarshalRequestMessage(data []byte) (RequestMessage, error) {
	return requestMessageRegistry.UnmarshalOne(data)
}

// UnmarshalRequestMessages decodes a JSON array of chat-completions request
// messages, dispatching each element on its "role" discriminator. Unmodelled
// roles return as UnknownRequestMessage.
func UnmarshalRequestMessages(data []byte) ([]RequestMessage, error) {
	return requestMessageRegistry.UnmarshalSlice(data)
}
