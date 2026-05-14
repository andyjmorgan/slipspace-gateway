package messages

import (
	"encoding/json"
	"fmt"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// StreamEvent is implemented by every event type emitted by Anthropic's
// streaming /v1/messages endpoint, including the UnknownStreamEvent fallback
// used when Anthropic introduces a new event discriminator.
type StreamEvent interface {
	EventType() string

	isStreamEvent()
}

// MessageStartEvent is the first event in a stream and carries the partial
// MessagesResponse the rest of the events delta into.
type MessageStartEvent struct {
	Type string `json:"type"`

	Message MessagesResponse `json:"message"`

	models.DynamicProperties
}

// EventType returns the "message_start" discriminator.
func (MessageStartEvent) EventType() string { return "message_start" }

func (MessageStartEvent) isStreamEvent() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *MessageStartEvent) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, e) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e MessageStartEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// ContentBlockStartEvent announces the start of a new content block at Index.
type ContentBlockStartEvent struct {
	Type string `json:"type"`

	Index int `json:"index"`

	ContentBlock ContentBlock `json:"content_block"`

	models.DynamicProperties
}

// EventType returns the "content_block_start" discriminator.
func (ContentBlockStartEvent) EventType() string { return "content_block_start" }

func (ContentBlockStartEvent) isStreamEvent() {}

// UnmarshalJSON dispatches ContentBlock through the polymorphic block
// registry, then routes any unknown top-level fields through
// DynamicProperties.
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

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e ContentBlockStartEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

type contentBlockStartRaw struct {
	Type string `json:"type"`

	Index int `json:"index"`

	ContentBlock json.RawMessage `json:"content_block,omitempty"`

	models.DynamicProperties
}

// ContentBlockDeltaEvent carries an incremental update to the content block at
// Index. The Delta itself is polymorphic.
type ContentBlockDeltaEvent struct {
	Type string `json:"type"`

	Index int `json:"index"`

	Delta ContentBlockDelta `json:"delta"`

	models.DynamicProperties
}

// EventType returns the "content_block_delta" discriminator.
func (ContentBlockDeltaEvent) EventType() string { return "content_block_delta" }

func (ContentBlockDeltaEvent) isStreamEvent() {}

// UnmarshalJSON dispatches Delta through the polymorphic delta registry, then
// routes any unknown top-level fields through DynamicProperties.
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

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e ContentBlockDeltaEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

type contentBlockDeltaRaw struct {
	Type string `json:"type"`

	Index int `json:"index"`

	Delta json.RawMessage `json:"delta,omitempty"`

	models.DynamicProperties
}

// ContentBlockStopEvent terminates the content block at Index.
type ContentBlockStopEvent struct {
	Type string `json:"type"`

	Index int `json:"index"`

	models.DynamicProperties
}

// EventType returns the "content_block_stop" discriminator.
func (ContentBlockStopEvent) EventType() string { return "content_block_stop" }

func (ContentBlockStopEvent) isStreamEvent() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *ContentBlockStopEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e ContentBlockStopEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// MessageDeltaEvent is emitted near the end of a stream with terminal message
// state (stop_reason, stop_sequence) and the final usage accounting.
type MessageDeltaEvent struct {
	Type string `json:"type"`

	Delta MessageDelta `json:"delta"`

	Usage Usage `json:"usage"`

	models.DynamicProperties
}

// EventType returns the "message_delta" discriminator.
func (MessageDeltaEvent) EventType() string { return "message_delta" }

func (MessageDeltaEvent) isStreamEvent() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *MessageDeltaEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e MessageDeltaEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// MessageDelta is the payload of MessageDeltaEvent.Delta — terminal message
// state delivered alongside the final usage accounting.
type MessageDelta struct {
	StopReason *string `json:"stop_reason"`

	StopSequence *string `json:"stop_sequence"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (d *MessageDelta) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, d) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (d MessageDelta) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// MessageStopEvent terminates the stream.
type MessageStopEvent struct {
	Type string `json:"type"`

	models.DynamicProperties
}

// EventType returns the "message_stop" discriminator.
func (MessageStopEvent) EventType() string { return "message_stop" }

func (MessageStopEvent) isStreamEvent() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *MessageStopEvent) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, e) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e MessageStopEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// PingEvent is a periodic keep-alive emitted on long-running streams.
type PingEvent struct {
	Type string `json:"type"`

	models.DynamicProperties
}

// EventType returns the "ping" discriminator.
func (PingEvent) EventType() string { return "ping" }

func (PingEvent) isStreamEvent() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *PingEvent) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, e) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e PingEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// ErrorEvent is emitted when the upstream terminates the stream with an error.
type ErrorEvent struct {
	Type string `json:"type"`

	Error StreamError `json:"error"`

	models.DynamicProperties
}

// EventType returns the "error" discriminator.
func (ErrorEvent) EventType() string { return "error" }

func (ErrorEvent) isStreamEvent() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *ErrorEvent) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, e) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e ErrorEvent) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// StreamError describes the upstream error carried by an ErrorEvent.
type StreamError struct {
	Type string `json:"type"`

	Message string `json:"message"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (s *StreamError) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, s) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (s StreamError) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(s) }

// UnknownStreamEvent preserves any stream event whose discriminator we have
// not modelled. The Type field carries the unknown discriminator and every
// other JSON field lands in DynamicProperties.Extra so the event round-trips
// intact.
type UnknownStreamEvent struct {
	Type string `json:"type"`

	models.DynamicProperties
}

// EventType returns the unknown discriminator value verbatim.
func (e UnknownStreamEvent) EventType() string { return e.Type }

func (UnknownStreamEvent) isStreamEvent() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *UnknownStreamEvent) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
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

// UnmarshalStreamEvent decodes a single Anthropic streaming event, dispatching
// on the "type" discriminator and falling back to UnknownStreamEvent for any
// unrecognised value.
func UnmarshalStreamEvent(data []byte) (StreamEvent, error) {
	return streamEventRegistry.UnmarshalOne(data)
}

// ContentBlockDelta is implemented by every delta variant carried inside a
// ContentBlockDeltaEvent, including the UnknownContentBlockDelta fallback used
// when Anthropic introduces a new delta discriminator.
type ContentBlockDelta interface {
	DeltaType() string

	isContentBlockDelta()
}

// TextDelta appends Text to the text block at the event's Index.
type TextDelta struct {
	Type string `json:"type"`

	Text string `json:"text"`

	models.DynamicProperties
}

// DeltaType returns the "text_delta" discriminator.
func (TextDelta) DeltaType() string { return "text_delta" }

func (TextDelta) isContentBlockDelta() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (d *TextDelta) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, d) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (d TextDelta) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// InputJSONDelta appends PartialJSON to the tool_use block at the event's
// Index. The JSON fragment is unparseable in isolation; clients concatenate
// successive PartialJSON values and parse the result once the block stops.
type InputJSONDelta struct {
	Type string `json:"type"`

	PartialJSON string `json:"partial_json"`

	models.DynamicProperties
}

// DeltaType returns the "input_json_delta" discriminator.
func (InputJSONDelta) DeltaType() string { return "input_json_delta" }

func (InputJSONDelta) isContentBlockDelta() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (d *InputJSONDelta) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, d) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (d InputJSONDelta) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// ThinkingDelta appends Thinking to the thinking block at the event's Index.
type ThinkingDelta struct {
	Type string `json:"type"`

	Thinking string `json:"thinking"`

	models.DynamicProperties
}

// DeltaType returns the "thinking_delta" discriminator.
func (ThinkingDelta) DeltaType() string { return "thinking_delta" }

func (ThinkingDelta) isContentBlockDelta() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (d *ThinkingDelta) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, d) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (d ThinkingDelta) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// SignatureDelta carries the cryptographic signature attached to a thinking
// block once it stops.
type SignatureDelta struct {
	Type string `json:"type"`

	Signature string `json:"signature"`

	models.DynamicProperties
}

// DeltaType returns the "signature_delta" discriminator.
func (SignatureDelta) DeltaType() string { return "signature_delta" }

func (SignatureDelta) isContentBlockDelta() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (d *SignatureDelta) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, d) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (d SignatureDelta) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(d) }

// UnknownContentBlockDelta preserves any delta whose discriminator we have not
// modelled. The Type field carries the unknown discriminator and every other
// JSON field lands in DynamicProperties.Extra so the delta round-trips intact.
type UnknownContentBlockDelta struct {
	Type string `json:"type"`

	models.DynamicProperties
}

// DeltaType returns the unknown discriminator value verbatim.
func (d UnknownContentBlockDelta) DeltaType() string { return d.Type }

func (UnknownContentBlockDelta) isContentBlockDelta() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (d *UnknownContentBlockDelta) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, d)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
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

// UnmarshalContentBlockDelta decodes a single Anthropic content block delta,
// dispatching on the "type" discriminator and falling back to
// UnknownContentBlockDelta for any unrecognised value.
func UnmarshalContentBlockDelta(data []byte) (ContentBlockDelta, error) {
	return contentBlockDeltaRegistry.UnmarshalOne(data)
}
