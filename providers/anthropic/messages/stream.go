package messages

import (
	"encoding/json"
	"fmt"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// StreamEvent is the polymorphic interface implemented by every event type
// emitted by Anthropic's streaming POST /v1/messages endpoint —
// MessageStartEvent, ContentBlockStartEvent, ContentBlockDeltaEvent,
// ContentBlockStopEvent, MessageDeltaEvent, MessageStopEvent, PingEvent,
// ErrorEvent, and the UnknownStreamEvent fallback.
//
// The discriminator is the "type" field. Unknown discriminator values are
// dispatched to UnknownStreamEvent, which preserves both the type value AND
// any sibling JSON fields via DynamicProperties so the event round-trips
// intact.
//
// Decode a single event JSON object via UnmarshalStreamEvent rather than
// calling json.Unmarshal into a StreamEvent directly — Go cannot pick the
// concrete type from a bare interface, so registry dispatch is what makes
// the round-trip work.
type StreamEvent interface {
	// EventType returns the wire discriminator value of the concrete
	// event type (e.g., "message_start").
	EventType() string

	isStreamEvent()
}

// MessageStartEvent is the first event in a stream and carries the partial
// MessagesResponse that the rest of the stream incrementally delta into.
// Unknown fields round-trip via the embedded DynamicProperties.
type MessageStartEvent struct {
	// Type is the wire "type" discriminator, always "message_start".
	Type string `json:"type"`

	// Message is the partial MessagesResponse — Content is initially
	// empty and is populated by subsequent ContentBlockStart/Delta/Stop
	// events.
	Message MessagesResponse `json:"message"`

	models.DynamicProperties
}

// EventType returns the "message_start" discriminator.
func (MessageStartEvent) EventType() string { return "message_start" }

func (MessageStartEvent) isStreamEvent() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *MessageStartEvent) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, e) }

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e MessageStartEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// ContentBlockStartEvent announces the start of a new content block at
// Index. ContentBlock is dispatched through the polymorphic registry so
// unknown block discriminators round-trip via UnknownBlock. Unknown fields
// round-trip via the embedded DynamicProperties.
type ContentBlockStartEvent struct {
	// Type is the wire "type" discriminator, always
	// "content_block_start".
	Type string `json:"type"`

	// Index is the new block's zero-based position in the assembled
	// message's content array.
	Index int `json:"index"`

	// ContentBlock is the initial block shape (may be empty placeholder
	// content that Delta events populate).
	ContentBlock ContentBlock `json:"content_block"`

	models.DynamicProperties
}

// EventType returns the "content_block_start" discriminator.
func (ContentBlockStartEvent) EventType() string { return "content_block_start" }

func (ContentBlockStartEvent) isStreamEvent() {}

// UnmarshalJSON decodes data into e. The ContentBlock field is dispatched
// through the polymorphic block registry so unknown block discriminators
// round-trip via UnknownBlock; any other unknown top-level field lands in
// DynamicProperties.Extra.
func (e *ContentBlockStartEvent) UnmarshalJSON(data []byte) error {
	var shadow contentBlockStartRaw
	if err := models.UnmarshalDynamic(data, &shadow); err != nil {
		return err
	}
	*e = ContentBlockStartEvent{
		Type:              shadow.Type,
		Index:             shadow.Index,
		DynamicProperties: shadow.DynamicProperties,
	}
	if len(shadow.ContentBlock) == 0 || string(shadow.ContentBlock) == "null" {
		return nil
	}
	block, err := UnmarshalContentBlock(shadow.ContentBlock)
	if err != nil {
		return fmt.Errorf("content_block_start.content_block: %w", err)
	}
	e.ContentBlock = block
	return nil
}

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e ContentBlockStartEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

type contentBlockStartRaw struct {
	Type string `json:"type"`

	Index int `json:"index"`

	ContentBlock json.RawMessage `json:"content_block,omitempty"`

	models.DynamicProperties
}

// ContentBlockDeltaEvent carries an incremental update to the content block
// at Index. The Delta itself is polymorphic — TextDelta, InputJSONDelta,
// ThinkingDelta, SignatureDelta, or UnknownContentBlockDelta. Unknown fields
// round-trip via the embedded DynamicProperties.
type ContentBlockDeltaEvent struct {
	// Type is the wire "type" discriminator, always
	// "content_block_delta".
	Type string `json:"type"`

	// Index is the position of the block this delta updates.
	Index int `json:"index"`

	// Delta is the incremental update payload, dispatched via
	// UnmarshalContentBlockDelta.
	Delta ContentBlockDelta `json:"delta"`

	models.DynamicProperties
}

// EventType returns the "content_block_delta" discriminator.
func (ContentBlockDeltaEvent) EventType() string { return "content_block_delta" }

func (ContentBlockDeltaEvent) isStreamEvent() {}

// UnmarshalJSON decodes data into e. The Delta field is dispatched through
// the polymorphic delta registry so unknown delta discriminators round-trip
// via UnknownContentBlockDelta; any other unknown top-level field lands in
// DynamicProperties.Extra.
func (e *ContentBlockDeltaEvent) UnmarshalJSON(data []byte) error {
	var shadow contentBlockDeltaRaw
	if err := models.UnmarshalDynamic(data, &shadow); err != nil {
		return err
	}
	*e = ContentBlockDeltaEvent{
		Type:              shadow.Type,
		Index:             shadow.Index,
		DynamicProperties: shadow.DynamicProperties,
	}
	if len(shadow.Delta) == 0 || string(shadow.Delta) == "null" {
		return nil
	}
	delta, err := UnmarshalContentBlockDelta(shadow.Delta)
	if err != nil {
		return fmt.Errorf("content_block_delta.delta: %w", err)
	}
	e.Delta = delta
	return nil
}

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e ContentBlockDeltaEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

type contentBlockDeltaRaw struct {
	Type string `json:"type"`

	Index int `json:"index"`

	Delta json.RawMessage `json:"delta,omitempty"`

	models.DynamicProperties
}

// ContentBlockStopEvent terminates the content block at Index (no more
// deltas will arrive for it). Unknown fields round-trip via the embedded
// DynamicProperties.
type ContentBlockStopEvent struct {
	// Type is the wire "type" discriminator, always
	// "content_block_stop".
	Type string `json:"type"`

	// Index is the position of the terminated block.
	Index int `json:"index"`

	models.DynamicProperties
}

// EventType returns the "content_block_stop" discriminator.
func (ContentBlockStopEvent) EventType() string { return "content_block_stop" }

func (ContentBlockStopEvent) isStreamEvent() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *ContentBlockStopEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e ContentBlockStopEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// MessageDeltaEvent is emitted near the end of a stream with the terminal
// message state (stop_reason, stop_sequence) and the final usage accounting.
// Unknown fields round-trip via the embedded DynamicProperties.
type MessageDeltaEvent struct {
	// Type is the wire "type" discriminator, always "message_delta".
	Type string `json:"type"`

	// Delta carries terminal stop_reason/stop_sequence state.
	Delta MessageDelta `json:"delta"`

	// Usage is the final token accounting for the request.
	Usage Usage `json:"usage"`

	models.DynamicProperties
}

// EventType returns the "message_delta" discriminator.
func (MessageDeltaEvent) EventType() string { return "message_delta" }

func (MessageDeltaEvent) isStreamEvent() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *MessageDeltaEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e MessageDeltaEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// MessageDelta is the payload of MessageDeltaEvent.Delta — terminal message
// state delivered alongside the final usage accounting. Unknown fields
// round-trip via the embedded DynamicProperties.
type MessageDelta struct {
	// StopReason is the terminal stop cause ("end_turn", "max_tokens",
	// ...).
	StopReason *string `json:"stop_reason"`

	// StopSequence is the matching stop string when StopReason ==
	// "stop_sequence".
	StopSequence *string `json:"stop_sequence"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into d, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (d *MessageDelta) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, d) }

// MarshalJSON encodes d and merges DynamicProperties.Extra back into the
// resulting object.
func (d MessageDelta) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// MessageStopEvent terminates the stream — no further events will arrive
// after this. Unknown fields round-trip via the embedded DynamicProperties.
type MessageStopEvent struct {
	// Type is the wire "type" discriminator, always "message_stop".
	Type string `json:"type"`

	models.DynamicProperties
}

// EventType returns the "message_stop" discriminator.
func (MessageStopEvent) EventType() string { return "message_stop" }

func (MessageStopEvent) isStreamEvent() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *MessageStopEvent) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, e) }

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e MessageStopEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// PingEvent is a periodic keep-alive emitted on long-running streams.
// Unknown fields round-trip via the embedded DynamicProperties.
type PingEvent struct {
	// Type is the wire "type" discriminator, always "ping".
	Type string `json:"type"`

	models.DynamicProperties
}

// EventType returns the "ping" discriminator.
func (PingEvent) EventType() string { return "ping" }

func (PingEvent) isStreamEvent() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *PingEvent) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, e) }

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e PingEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// ErrorEvent is emitted when the upstream terminates the stream with an
// error. Unknown fields round-trip via the embedded DynamicProperties.
type ErrorEvent struct {
	// Type is the wire "type" discriminator, always "error".
	Type string `json:"type"`

	// Error describes the upstream error.
	Error StreamError `json:"error"`

	models.DynamicProperties
}

// EventType returns the "error" discriminator.
func (ErrorEvent) EventType() string { return "error" }

func (ErrorEvent) isStreamEvent() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *ErrorEvent) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, e) }

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e ErrorEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// StreamError describes the upstream error carried by an ErrorEvent.
// Unknown fields round-trip via the embedded DynamicProperties.
type StreamError struct {
	// Type is Anthropic's machine-readable error code (e.g.,
	// "overloaded_error", "rate_limit_error").
	Type string `json:"type"`

	// Message is the human-readable error message.
	Message string `json:"message"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into s, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (s *StreamError) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, s) }

// MarshalJSON encodes s and merges DynamicProperties.Extra back into the
// resulting object.
func (s StreamError) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(s) }

// UnknownStreamEvent preserves any stream event whose "type" discriminator
// this package has not modelled. Type carries the unknown discriminator
// verbatim and every other JSON field lands in DynamicProperties.Extra so
// the event round-trips intact.
type UnknownStreamEvent struct {
	// Type is the unmodelled wire discriminator, preserved verbatim.
	Type string `json:"type"`

	models.DynamicProperties
}

// EventType returns the unknown discriminator value verbatim.
func (e UnknownStreamEvent) EventType() string { return e.Type }

func (UnknownStreamEvent) isStreamEvent() {}

// UnmarshalJSON decodes data into e, routing every non-type field into
// DynamicProperties.Extra so the unknown event round-trips byte-equivalent.
func (e *UnknownStreamEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e UnknownStreamEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

var streamEventRegistry = models.PolymorphicRegistry[StreamEvent]{
	DiscriminatorField: "type",
	Factories: map[string]func() StreamEvent{
		"message_start":       func() StreamEvent { return &MessageStartEvent{} },
		"content_block_start": func() StreamEvent { return &ContentBlockStartEvent{} },
		"content_block_delta": func() StreamEvent { return &ContentBlockDeltaEvent{} },
		"content_block_stop":  func() StreamEvent { return &ContentBlockStopEvent{} },
		"message_delta":       func() StreamEvent { return &MessageDeltaEvent{} },
		"message_stop":        func() StreamEvent { return &MessageStopEvent{} },
		"ping":                func() StreamEvent { return &PingEvent{} },
		"error":               func() StreamEvent { return &ErrorEvent{} },
	},
	Fallback: func(disc string) StreamEvent { return &UnknownStreamEvent{Type: disc} },
}

// UnmarshalStreamEvent decodes a single Anthropic streaming event JSON
// object, dispatching on the "type" discriminator. Any event type this
// package has not modelled is returned as an UnknownStreamEvent so the
// payload still round-trips.
func UnmarshalStreamEvent(data []byte) (StreamEvent, error) {
	return streamEventRegistry.UnmarshalOne(data)
}

// ContentBlockDelta is the polymorphic interface implemented by every delta
// variant carried inside a ContentBlockDeltaEvent — TextDelta,
// InputJSONDelta, ThinkingDelta, SignatureDelta, and the
// UnknownContentBlockDelta fallback.
//
// The discriminator is the "type" field. Unknown discriminator values are
// dispatched to UnknownContentBlockDelta, which preserves both the type
// value AND any sibling JSON fields via DynamicProperties so the delta
// round-trips intact.
//
// Decode a single delta JSON object via UnmarshalContentBlockDelta rather
// than calling json.Unmarshal into a ContentBlockDelta directly — Go cannot
// pick the concrete type from a bare interface, so registry dispatch is
// what makes the round-trip work.
type ContentBlockDelta interface {
	// DeltaType returns the wire discriminator value of the concrete
	// delta type (e.g., "text_delta").
	DeltaType() string

	isContentBlockDelta()
}

// TextDelta appends Text to the text block at the ContentBlockDeltaEvent's
// Index. Unknown fields round-trip via the embedded DynamicProperties.
type TextDelta struct {
	// Type is the wire "type" discriminator, always "text_delta".
	Type string `json:"type"`

	// Text is the text fragment to append.
	Text string `json:"text"`

	models.DynamicProperties
}

// DeltaType returns the "text_delta" discriminator.
func (TextDelta) DeltaType() string { return "text_delta" }

func (TextDelta) isContentBlockDelta() {}

// UnmarshalJSON decodes data into d, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (d *TextDelta) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, d) }

// MarshalJSON encodes d and merges DynamicProperties.Extra back into the
// resulting object.
func (d TextDelta) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// InputJSONDelta appends PartialJSON to the tool_use block at the
// ContentBlockDeltaEvent's Index. The JSON fragment is unparseable in
// isolation; clients concatenate successive PartialJSON values and parse the
// result once the block stops. Unknown fields round-trip via the embedded
// DynamicProperties.
type InputJSONDelta struct {
	// Type is the wire "type" discriminator, always "input_json_delta".
	Type string `json:"type"`

	// PartialJSON is the next JSON-string fragment to append; do NOT
	// json.Unmarshal this in isolation — concatenate all PartialJSON
	// values for the same block first.
	PartialJSON string `json:"partial_json"`

	models.DynamicProperties
}

// DeltaType returns the "input_json_delta" discriminator.
func (InputJSONDelta) DeltaType() string { return "input_json_delta" }

func (InputJSONDelta) isContentBlockDelta() {}

// UnmarshalJSON decodes data into d, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (d *InputJSONDelta) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, d) }

// MarshalJSON encodes d and merges DynamicProperties.Extra back into the
// resulting object.
func (d InputJSONDelta) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// ThinkingDelta appends Thinking to the thinking block at the
// ContentBlockDeltaEvent's Index. Unknown fields round-trip via the
// embedded DynamicProperties.
type ThinkingDelta struct {
	// Type is the wire "type" discriminator, always "thinking_delta".
	Type string `json:"type"`

	// Thinking is the next thinking-trace fragment to append.
	Thinking string `json:"thinking"`

	models.DynamicProperties
}

// DeltaType returns the "thinking_delta" discriminator.
func (ThinkingDelta) DeltaType() string { return "thinking_delta" }

func (ThinkingDelta) isContentBlockDelta() {}

// UnmarshalJSON decodes data into d, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (d *ThinkingDelta) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, d) }

// MarshalJSON encodes d and merges DynamicProperties.Extra back into the
// resulting object.
func (d ThinkingDelta) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// SignatureDelta carries the cryptographic signature attached to a thinking
// block once it stops; emitted as a single delta rather than a stream.
// Unknown fields round-trip via the embedded DynamicProperties.
type SignatureDelta struct {
	// Type is the wire "type" discriminator, always "signature_delta".
	Type string `json:"type"`

	// Signature is the cryptographic signature over the thinking block.
	Signature string `json:"signature"`

	models.DynamicProperties
}

// DeltaType returns the "signature_delta" discriminator.
func (SignatureDelta) DeltaType() string { return "signature_delta" }

func (SignatureDelta) isContentBlockDelta() {}

// UnmarshalJSON decodes data into d, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (d *SignatureDelta) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, d) }

// MarshalJSON encodes d and merges DynamicProperties.Extra back into the
// resulting object.
func (d SignatureDelta) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// UnknownContentBlockDelta preserves any delta whose "type" discriminator
// this package has not modelled. Type carries the unknown discriminator
// verbatim and every other JSON field lands in DynamicProperties.Extra so
// the delta round-trips intact.
type UnknownContentBlockDelta struct {
	// Type is the unmodelled wire discriminator, preserved verbatim.
	Type string `json:"type"`

	models.DynamicProperties
}

// DeltaType returns the unknown discriminator value verbatim.
func (d UnknownContentBlockDelta) DeltaType() string { return d.Type }

func (UnknownContentBlockDelta) isContentBlockDelta() {}

// UnmarshalJSON decodes data into d, routing every non-type field into
// DynamicProperties.Extra so the unknown delta round-trips byte-equivalent.
func (d *UnknownContentBlockDelta) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, d)
}

// MarshalJSON encodes d and merges DynamicProperties.Extra back into the
// resulting object.
func (d UnknownContentBlockDelta) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

var contentBlockDeltaRegistry = models.PolymorphicRegistry[ContentBlockDelta]{
	DiscriminatorField: "type",
	Factories: map[string]func() ContentBlockDelta{
		"text_delta":       func() ContentBlockDelta { return &TextDelta{} },
		"input_json_delta": func() ContentBlockDelta { return &InputJSONDelta{} },
		"thinking_delta":   func() ContentBlockDelta { return &ThinkingDelta{} },
		"signature_delta":  func() ContentBlockDelta { return &SignatureDelta{} },
	},
	Fallback: func(disc string) ContentBlockDelta { return &UnknownContentBlockDelta{Type: disc} },
}

// UnmarshalContentBlockDelta decodes a single Anthropic content-block delta
// JSON object, dispatching on the "type" discriminator. Any delta type this
// package has not modelled is returned as an UnknownContentBlockDelta so
// the payload still round-trips.
func UnmarshalContentBlockDelta(data []byte) (ContentBlockDelta, error) {
	return contentBlockDeltaRegistry.UnmarshalOne(data)
}
