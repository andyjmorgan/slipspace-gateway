package responses

import (
	"encoding/json"

	"github.com/andyjmorgan/slipspace-gateway/models"
)

// ResponsesStreamEvent is the polymorphic interface implemented by every
// event type emitted by the streaming /v1/responses endpoint — the
// ResponseCreated, ResponseInProgress, OutputItemAdded/Done,
// ContentPartAdded/Done, OutputTextDelta/Done, ResponseCompleted,
// ResponseFailed concrete types, and the UnknownEvent fallback.
//
// The discriminator is the "type" field. Unknown discriminator values are
// dispatched to UnknownEvent, which preserves both the type value AND any
// sibling JSON fields via DynamicProperties so the event round-trips intact —
// this matters because OpenAI ships new event types frequently and the
// gateway must not drop them.
//
// Decode a single event JSON object via UnmarshalStreamEvent (or a whole
// array via UnmarshalStreamEvents) rather than calling json.Unmarshal into a
// ResponsesStreamEvent directly — Go cannot pick the concrete type from a
// bare interface, so registry dispatch is what makes the round-trip work.
type ResponsesStreamEvent interface {
	// EventType returns the wire discriminator value of the concrete
	// event type (e.g., "response.created").
	EventType() string

	isResponsesStreamEvent()
}

// ResponseCreatedEvent fires once when the response is initially created at
// the start of a stream. Unknown fields round-trip via the embedded
// DynamicProperties.
type ResponseCreatedEvent struct {
	// Type is the wire "type" discriminator, always "response.created".
	Type string `json:"type"`

	// SequenceNumber is the event's monotonic position in the stream
	// (when OpenAI sends it).
	SequenceNumber *int `json:"sequence_number,omitempty"`

	// Response is the initial ResponsesResponse shape (mostly empty at
	// creation). Kept raw because the type otherwise self-references.
	Response json.RawMessage `json:"response,omitempty"`

	models.DynamicProperties
}

// EventType returns the "response.created" discriminator.
func (ResponseCreatedEvent) EventType() string { return "response.created" }

func (ResponseCreatedEvent) isResponsesStreamEvent() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *ResponseCreatedEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e ResponseCreatedEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// ResponseInProgressEvent fires periodically while the response is being
// generated and carries the latest partial ResponsesResponse snapshot.
// Unknown fields round-trip via the embedded DynamicProperties.
type ResponseInProgressEvent struct {
	// Type is the wire "type" discriminator, always "response.in_progress".
	Type string `json:"type"`

	// SequenceNumber is the event's monotonic position in the stream.
	SequenceNumber *int `json:"sequence_number,omitempty"`

	// Response is the partial ResponsesResponse snapshot. Kept raw because
	// the type otherwise self-references.
	Response json.RawMessage `json:"response,omitempty"`

	models.DynamicProperties
}

// EventType returns the "response.in_progress" discriminator.
func (ResponseInProgressEvent) EventType() string { return "response.in_progress" }

func (ResponseInProgressEvent) isResponsesStreamEvent() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *ResponseInProgressEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e ResponseInProgressEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// OutputItemAddedEvent fires when a new OutputItem is appended to
// ResponsesResponse.Output. Unknown fields round-trip via the embedded
// DynamicProperties.
type OutputItemAddedEvent struct {
	// Type is the wire "type" discriminator, always
	// "response.output_item.added".
	Type string `json:"type"`

	// SequenceNumber is the event's monotonic position in the stream.
	SequenceNumber *int `json:"sequence_number,omitempty"`

	// OutputIndex is the new item's zero-based position in Output.
	OutputIndex int `json:"output_index"`

	// Item is the OutputItem being appended. Kept raw so callers dispatch
	// on Type themselves.
	Item json.RawMessage `json:"item,omitempty"`

	models.DynamicProperties
}

// EventType returns the "response.output_item.added" discriminator.
func (OutputItemAddedEvent) EventType() string { return "response.output_item.added" }

func (OutputItemAddedEvent) isResponsesStreamEvent() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *OutputItemAddedEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e OutputItemAddedEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// OutputItemDoneEvent fires when the OutputItem at OutputIndex is finalised
// (no more incremental updates will arrive for it). Unknown fields
// round-trip via the embedded DynamicProperties.
type OutputItemDoneEvent struct {
	// Type is the wire "type" discriminator, always
	// "response.output_item.done".
	Type string `json:"type"`

	// SequenceNumber is the event's monotonic position in the stream.
	SequenceNumber *int `json:"sequence_number,omitempty"`

	// OutputIndex is the finalised item's position in Output.
	OutputIndex int `json:"output_index"`

	// Item is the final OutputItem shape after streaming has completed.
	Item json.RawMessage `json:"item,omitempty"`

	models.DynamicProperties
}

// EventType returns the "response.output_item.done" discriminator.
func (OutputItemDoneEvent) EventType() string { return "response.output_item.done" }

func (OutputItemDoneEvent) isResponsesStreamEvent() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *OutputItemDoneEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e OutputItemDoneEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// ContentPartAddedEvent fires when a new content part is added inside the
// OutputItem at OutputIndex. Unknown fields round-trip via the embedded
// DynamicProperties.
type ContentPartAddedEvent struct {
	// Type is the wire "type" discriminator, always
	// "response.content_part.added".
	Type string `json:"type"`

	// SequenceNumber is the event's monotonic position in the stream.
	SequenceNumber *int `json:"sequence_number,omitempty"`

	// ItemID identifies the OutputItem this part belongs to.
	ItemID string `json:"item_id,omitempty"`

	// OutputIndex is the parent item's position in Output.
	OutputIndex int `json:"output_index"`

	// ContentIndex is the new part's zero-based position within the
	// item's content array.
	ContentIndex int `json:"content_index"`

	// Part is the content part being appended. Kept raw because the
	// shape varies by part type.
	Part json.RawMessage `json:"part,omitempty"`

	models.DynamicProperties
}

// EventType returns the "response.content_part.added" discriminator.
func (ContentPartAddedEvent) EventType() string { return "response.content_part.added" }

func (ContentPartAddedEvent) isResponsesStreamEvent() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *ContentPartAddedEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e ContentPartAddedEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// ContentPartDoneEvent fires when the content part at ContentIndex inside
// the OutputItem at OutputIndex is finalised. Unknown fields round-trip via
// the embedded DynamicProperties.
type ContentPartDoneEvent struct {
	// Type is the wire "type" discriminator, always
	// "response.content_part.done".
	Type string `json:"type"`

	// SequenceNumber is the event's monotonic position in the stream.
	SequenceNumber *int `json:"sequence_number,omitempty"`

	// ItemID identifies the OutputItem this part belongs to.
	ItemID string `json:"item_id,omitempty"`

	// OutputIndex is the parent item's position in Output.
	OutputIndex int `json:"output_index"`

	// ContentIndex is the finalised part's position within the item's
	// content array.
	ContentIndex int `json:"content_index"`

	// Part is the final content part after streaming has completed.
	Part json.RawMessage `json:"part,omitempty"`

	models.DynamicProperties
}

// EventType returns the "response.content_part.done" discriminator.
func (ContentPartDoneEvent) EventType() string { return "response.content_part.done" }

func (ContentPartDoneEvent) isResponsesStreamEvent() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *ContentPartDoneEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e ContentPartDoneEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// OutputTextDeltaEvent fires for each incremental fragment of generated text
// inside an output_text content part. Receivers concatenate Delta across
// successive events keyed by (OutputIndex, ContentIndex) to reconstruct the
// final text. Unknown fields round-trip via the embedded DynamicProperties.
type OutputTextDeltaEvent struct {
	// Type is the wire "type" discriminator, always
	// "response.output_text.delta".
	Type string `json:"type"`

	// SequenceNumber is the event's monotonic position in the stream.
	SequenceNumber *int `json:"sequence_number,omitempty"`

	// ItemID identifies the OutputItem this text belongs to.
	ItemID string `json:"item_id,omitempty"`

	// OutputIndex is the parent item's position in Output.
	OutputIndex int `json:"output_index"`

	// ContentIndex is the parent content part's position.
	ContentIndex int `json:"content_index"`

	// Delta is the next text fragment to append to the content part.
	Delta string `json:"delta"`

	models.DynamicProperties
}

// EventType returns the "response.output_text.delta" discriminator.
func (OutputTextDeltaEvent) EventType() string { return "response.output_text.delta" }

func (OutputTextDeltaEvent) isResponsesStreamEvent() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *OutputTextDeltaEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e OutputTextDeltaEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// OutputTextDoneEvent fires when an output_text content part is finalised
// and carries the full assembled Text (allowing receivers to validate their
// own concatenation). Unknown fields round-trip via the embedded
// DynamicProperties.
type OutputTextDoneEvent struct {
	// Type is the wire "type" discriminator, always
	// "response.output_text.done".
	Type string `json:"type"`

	// SequenceNumber is the event's monotonic position in the stream.
	SequenceNumber *int `json:"sequence_number,omitempty"`

	// ItemID identifies the OutputItem this text belongs to.
	ItemID string `json:"item_id,omitempty"`

	// OutputIndex is the parent item's position in Output.
	OutputIndex int `json:"output_index"`

	// ContentIndex is the parent content part's position.
	ContentIndex int `json:"content_index"`

	// Text is the complete assembled text for this content part.
	Text string `json:"text"`

	models.DynamicProperties
}

// EventType returns the "response.output_text.done" discriminator.
func (OutputTextDoneEvent) EventType() string { return "response.output_text.done" }

func (OutputTextDoneEvent) isResponsesStreamEvent() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *OutputTextDoneEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e OutputTextDoneEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// ResponseCompletedEvent fires once when the response has been fully
// generated and carries the final ResponsesResponse shape. Unknown fields
// round-trip via the embedded DynamicProperties.
type ResponseCompletedEvent struct {
	// Type is the wire "type" discriminator, always "response.completed".
	Type string `json:"type"`

	// SequenceNumber is the event's monotonic position in the stream.
	SequenceNumber *int `json:"sequence_number,omitempty"`

	// Response is the final ResponsesResponse snapshot. Kept raw because
	// the type otherwise self-references.
	Response json.RawMessage `json:"response,omitempty"`

	models.DynamicProperties
}

// EventType returns the "response.completed" discriminator.
func (ResponseCompletedEvent) EventType() string { return "response.completed" }

func (ResponseCompletedEvent) isResponsesStreamEvent() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *ResponseCompletedEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e ResponseCompletedEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// ResponseFailedEvent fires once if generation fails and carries the upstream
// error payload. Unknown fields round-trip via the embedded
// DynamicProperties.
type ResponseFailedEvent struct {
	// Type is the wire "type" discriminator, always "response.failed".
	Type string `json:"type"`

	// SequenceNumber is the event's monotonic position in the stream.
	SequenceNumber *int `json:"sequence_number,omitempty"`

	// Response is the final ResponsesResponse snapshot with Status ==
	// "failed". Kept raw because the type otherwise self-references.
	Response json.RawMessage `json:"response,omitempty"`

	// Error is the upstream error payload. Kept raw because OpenAI's
	// error shape evolves frequently.
	Error json.RawMessage `json:"error,omitempty"`

	models.DynamicProperties
}

// EventType returns the "response.failed" discriminator.
func (ResponseFailedEvent) EventType() string { return "response.failed" }

func (ResponseFailedEvent) isResponsesStreamEvent() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *ResponseFailedEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e ResponseFailedEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// UnknownEvent preserves any streaming event whose "type" discriminator this
// package has not modelled. Type carries the unknown discriminator verbatim
// and every other JSON field lands in DynamicProperties.Extra so the event
// round-trips intact — critical for forward-compatibility with new event
// kinds OpenAI ships between releases.
type UnknownEvent struct {
	// Type is the unmodelled wire discriminator, preserved verbatim.
	Type string `json:"type"`

	// SequenceNumber is the event's monotonic position in the stream when
	// OpenAI sends it.
	SequenceNumber *int `json:"sequence_number,omitempty"`

	models.DynamicProperties
}

// EventType returns the unknown discriminator value verbatim.
func (e UnknownEvent) EventType() string { return e.Type }

func (UnknownEvent) isResponsesStreamEvent() {}

// UnmarshalJSON decodes data into e, routing every non-typed field into
// DynamicProperties.Extra so the unknown event round-trips byte-equivalent.
func (e *UnknownEvent) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, e) }

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
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

// UnmarshalStreamEvent decodes a single /v1/responses streaming event JSON
// object, dispatching on the "type" discriminator. Any event type this
// package has not modelled is returned as an UnknownEvent so the payload
// still round-trips.
func UnmarshalStreamEvent(data []byte) (ResponsesStreamEvent, error) {
	return streamEventRegistry.UnmarshalOne(data)
}

// UnmarshalStreamEvents decodes a JSON array of /v1/responses streaming
// events, dispatching each element on its "type" discriminator. Unmodelled
// types return as UnknownEvent.
func UnmarshalStreamEvents(data []byte) ([]ResponsesStreamEvent, error) {
	return streamEventRegistry.UnmarshalSlice(data)
}
