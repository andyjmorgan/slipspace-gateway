package responses

import (
	"encoding/json"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// ResponsesStreamEvent is implemented by every concrete streaming event type
// and by UnknownEvent. The discriminator is the "type" field.
type ResponsesStreamEvent interface {
	EventType() string

	isResponsesStreamEvent()
}

// ResponseCreatedEvent fires once when the response is initially created.
type ResponseCreatedEvent struct {
	Type string `json:"type"`

	SequenceNumber *int `json:"sequence_number,omitempty"`

	Response json.RawMessage `json:"response,omitempty"`

	models.DynamicProperties
}

// EventType returns the "response.created" discriminator.
func (ResponseCreatedEvent) EventType() string { return "response.created" }

func (ResponseCreatedEvent) isResponsesStreamEvent() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *ResponseCreatedEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e ResponseCreatedEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// ResponseInProgressEvent fires while the response is being generated.
type ResponseInProgressEvent struct {
	Type string `json:"type"`

	SequenceNumber *int `json:"sequence_number,omitempty"`

	Response json.RawMessage `json:"response,omitempty"`

	models.DynamicProperties
}

// EventType returns the "response.in_progress" discriminator.
func (ResponseInProgressEvent) EventType() string { return "response.in_progress" }

func (ResponseInProgressEvent) isResponsesStreamEvent() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *ResponseInProgressEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e ResponseInProgressEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// OutputItemAddedEvent fires when a new output item is appended.
type OutputItemAddedEvent struct {
	Type string `json:"type"`

	SequenceNumber *int `json:"sequence_number,omitempty"`

	OutputIndex int `json:"output_index"`

	Item json.RawMessage `json:"item,omitempty"`

	models.DynamicProperties
}

// EventType returns the "response.output_item.added" discriminator.
func (OutputItemAddedEvent) EventType() string { return "response.output_item.added" }

func (OutputItemAddedEvent) isResponsesStreamEvent() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *OutputItemAddedEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e OutputItemAddedEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// OutputItemDoneEvent fires when an output item is finished.
type OutputItemDoneEvent struct {
	Type string `json:"type"`

	SequenceNumber *int `json:"sequence_number,omitempty"`

	OutputIndex int `json:"output_index"`

	Item json.RawMessage `json:"item,omitempty"`

	models.DynamicProperties
}

// EventType returns the "response.output_item.done" discriminator.
func (OutputItemDoneEvent) EventType() string { return "response.output_item.done" }

func (OutputItemDoneEvent) isResponsesStreamEvent() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *OutputItemDoneEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e OutputItemDoneEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// ContentPartAddedEvent fires when a new content part is added inside an
// output item.
type ContentPartAddedEvent struct {
	Type string `json:"type"`

	SequenceNumber *int `json:"sequence_number,omitempty"`

	ItemID string `json:"item_id,omitempty"`

	OutputIndex int `json:"output_index"`

	ContentIndex int `json:"content_index"`

	Part json.RawMessage `json:"part,omitempty"`

	models.DynamicProperties
}

// EventType returns the "response.content_part.added" discriminator.
func (ContentPartAddedEvent) EventType() string { return "response.content_part.added" }

func (ContentPartAddedEvent) isResponsesStreamEvent() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *ContentPartAddedEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e ContentPartAddedEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// ContentPartDoneEvent fires when a content part inside an output item is
// finalised.
type ContentPartDoneEvent struct {
	Type string `json:"type"`

	SequenceNumber *int `json:"sequence_number,omitempty"`

	ItemID string `json:"item_id,omitempty"`

	OutputIndex int `json:"output_index"`

	ContentIndex int `json:"content_index"`

	Part json.RawMessage `json:"part,omitempty"`

	models.DynamicProperties
}

// EventType returns the "response.content_part.done" discriminator.
func (ContentPartDoneEvent) EventType() string { return "response.content_part.done" }

func (ContentPartDoneEvent) isResponsesStreamEvent() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *ContentPartDoneEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e ContentPartDoneEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// OutputTextDeltaEvent fires for each incremental token of generated text.
type OutputTextDeltaEvent struct {
	Type string `json:"type"`

	SequenceNumber *int `json:"sequence_number,omitempty"`

	ItemID string `json:"item_id,omitempty"`

	OutputIndex int `json:"output_index"`

	ContentIndex int `json:"content_index"`

	Delta string `json:"delta"`

	models.DynamicProperties
}

// EventType returns the "response.output_text.delta" discriminator.
func (OutputTextDeltaEvent) EventType() string { return "response.output_text.delta" }

func (OutputTextDeltaEvent) isResponsesStreamEvent() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *OutputTextDeltaEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e OutputTextDeltaEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// OutputTextDoneEvent fires when an output_text content part is finalised
// with its complete text.
type OutputTextDoneEvent struct {
	Type string `json:"type"`

	SequenceNumber *int `json:"sequence_number,omitempty"`

	ItemID string `json:"item_id,omitempty"`

	OutputIndex int `json:"output_index"`

	ContentIndex int `json:"content_index"`

	Text string `json:"text"`

	models.DynamicProperties
}

// EventType returns the "response.output_text.done" discriminator.
func (OutputTextDoneEvent) EventType() string { return "response.output_text.done" }

func (OutputTextDoneEvent) isResponsesStreamEvent() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *OutputTextDoneEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e OutputTextDoneEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// ResponseCompletedEvent fires once when the response is fully generated.
type ResponseCompletedEvent struct {
	Type string `json:"type"`

	SequenceNumber *int `json:"sequence_number,omitempty"`

	Response json.RawMessage `json:"response,omitempty"`

	models.DynamicProperties
}

// EventType returns the "response.completed" discriminator.
func (ResponseCompletedEvent) EventType() string { return "response.completed" }

func (ResponseCompletedEvent) isResponsesStreamEvent() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *ResponseCompletedEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e ResponseCompletedEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// ResponseFailedEvent fires once if generation fails.
type ResponseFailedEvent struct {
	Type string `json:"type"`

	SequenceNumber *int `json:"sequence_number,omitempty"`

	Response json.RawMessage `json:"response,omitempty"`

	Error json.RawMessage `json:"error,omitempty"`

	models.DynamicProperties
}

// EventType returns the "response.failed" discriminator.
func (ResponseFailedEvent) EventType() string { return "response.failed" }

func (ResponseFailedEvent) isResponsesStreamEvent() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *ResponseFailedEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e ResponseFailedEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// UnknownEvent preserves any streaming event whose discriminator we have not
// modelled. Type carries the unknown discriminator and every other JSON field
// lands in DynamicProperties.Extra so the event round-trips intact.
type UnknownEvent struct {
	Type string `json:"type"`

	SequenceNumber *int `json:"sequence_number,omitempty"`

	models.DynamicProperties
}

// EventType returns the unknown discriminator value verbatim.
func (e UnknownEvent) EventType() string { return e.Type }

func (UnknownEvent) isResponsesStreamEvent() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *UnknownEvent) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, e) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e UnknownEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

var streamEventRegistry = models.PolymorphicRegistry[ResponsesStreamEvent]{
	DiscriminatorField: "type",
	Factories: map[string]func() ResponsesStreamEvent{
		"response.created":            func() ResponsesStreamEvent { return &ResponseCreatedEvent{} },
		"response.in_progress":        func() ResponsesStreamEvent { return &ResponseInProgressEvent{} },
		"response.output_item.added":  func() ResponsesStreamEvent { return &OutputItemAddedEvent{} },
		"response.output_item.done":   func() ResponsesStreamEvent { return &OutputItemDoneEvent{} },
		"response.content_part.added": func() ResponsesStreamEvent { return &ContentPartAddedEvent{} },
		"response.content_part.done":  func() ResponsesStreamEvent { return &ContentPartDoneEvent{} },
		"response.output_text.delta":  func() ResponsesStreamEvent { return &OutputTextDeltaEvent{} },
		"response.output_text.done":   func() ResponsesStreamEvent { return &OutputTextDoneEvent{} },
		"response.completed":          func() ResponsesStreamEvent { return &ResponseCompletedEvent{} },
		"response.failed":             func() ResponsesStreamEvent { return &ResponseFailedEvent{} },
	},
	Fallback: func(disc string) ResponsesStreamEvent { return &UnknownEvent{Type: disc} },
}

// UnmarshalStreamEvent decodes a single /v1/responses streaming event,
// dispatching on the "type" discriminator and falling back to UnknownEvent
// for any unrecognised value.
func UnmarshalStreamEvent(data []byte) (ResponsesStreamEvent, error) {
	return streamEventRegistry.UnmarshalOne(data)
}

// UnmarshalStreamEvents decodes a JSON array of /v1/responses streaming events.
func UnmarshalStreamEvents(data []byte) ([]ResponsesStreamEvent, error) {
	return streamEventRegistry.UnmarshalSlice(data)
}
