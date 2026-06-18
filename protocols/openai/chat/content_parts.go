package chat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/andyjmorgan/slipspace-gateway/models"
)

// ContentPart is the polymorphic interface implemented by every concrete
// chat-completions content part — TextContentPart, ImageURLContentPart,
// InputAudioContentPart, OutputAudioContentPart, RefusalContentPart,
// FileContentPart, and the UnknownContentPart fallback.
//
// The discriminator is the "type" field. Unknown discriminator values are
// dispatched to UnknownContentPart, which preserves both the type value AND
// any sibling JSON fields via DynamicProperties so the payload round-trips
// intact.
//
// Decode a JSON content-parts array via UnmarshalContentParts (or let
// MessageContent.AsParts do it for you) rather than calling json.Unmarshal
// into a []ContentPart directly — Go cannot pick the concrete type from a
// bare interface, so registry dispatch is what makes the round-trip work.
type ContentPart interface {
	// PartType returns the wire discriminator value of the concrete part
	// type ("text", "image_url", ...).
	PartType() string

	isContentPart()
}

// TextContentPart is the "text" content part variant — plain text inside a
// chat-completions message content array. Unknown fields round-trip via the
// embedded DynamicProperties.
type TextContentPart struct {
	// Type is the wire "type" discriminator, always "text".
	Type string `json:"type"`

	// Text is the part's literal text content.
	Text string `json:"text"`

	models.DynamicProperties
}

// PartType returns the "text" discriminator.
func (TextContentPart) PartType() string { return "text" }

func (TextContentPart) isContentPart() {}

// UnmarshalJSON decodes data into p, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (p *TextContentPart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON encodes p and merges DynamicProperties.Extra back into the
// resulting object.
func (p TextContentPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// ImageURL is the nested url+detail object on an ImageURLContentPart. Unknown
// fields round-trip via the embedded DynamicProperties.
type ImageURL struct {
	// URL is the http(s) URL or "data:" base64 image URL.
	URL string `json:"url"`

	// Detail selects the image-detail tier ("low", "high", "auto"); empty
	// means "use the server default".
	Detail string `json:"detail,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into i, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (i *ImageURL) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, i) }

// MarshalJSON encodes i and merges DynamicProperties.Extra back into the
// resulting object.
func (i ImageURL) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(i) }

// ImageURLContentPart is the "image_url" content part variant — an inline or
// remote image attached to a chat-completions message. Unknown fields
// round-trip via the embedded DynamicProperties.
type ImageURLContentPart struct {
	// Type is the wire "type" discriminator, always "image_url".
	Type string `json:"type"`

	// ImageURL carries the image's URL and detail hint.
	ImageURL ImageURL `json:"image_url"`

	models.DynamicProperties
}

// PartType returns the "image_url" discriminator.
func (ImageURLContentPart) PartType() string { return "image_url" }

func (ImageURLContentPart) isContentPart() {}

// UnmarshalJSON decodes data into p, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (p *ImageURLContentPart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON encodes p and merges DynamicProperties.Extra back into the
// resulting object.
func (p ImageURLContentPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// InputAudio is the nested audio payload on an InputAudioContentPart. Unknown
// fields round-trip via the embedded DynamicProperties.
type InputAudio struct {
	// Data is the base64-encoded audio payload.
	Data string `json:"data"`

	// Format selects the audio container/codec (e.g., "wav", "mp3").
	Format string `json:"format,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into a, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (a *InputAudio) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, a) }

// MarshalJSON encodes a and merges DynamicProperties.Extra back into the
// resulting object.
func (a InputAudio) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// InputAudioContentPart is the "input_audio" content part variant — audio
// sent to the model from the caller. Unknown fields round-trip via the
// embedded DynamicProperties.
type InputAudioContentPart struct {
	// Type is the wire "type" discriminator, always "input_audio".
	Type string `json:"type"`

	// InputAudio carries the audio data and format.
	InputAudio InputAudio `json:"input_audio"`

	models.DynamicProperties
}

// PartType returns the "input_audio" discriminator.
func (InputAudioContentPart) PartType() string { return "input_audio" }

func (InputAudioContentPart) isContentPart() {}

// UnmarshalJSON decodes data into p, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (p *InputAudioContentPart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON encodes p and merges DynamicProperties.Extra back into the
// resulting object.
func (p InputAudioContentPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// OutputAudioContentPart is the "audio" content part variant emitted by the
// assistant when audio modality is enabled on the request. Unknown fields
// round-trip via the embedded DynamicProperties.
type OutputAudioContentPart struct {
	// Type is the wire "type" discriminator, always "audio".
	Type string `json:"type"`

	// Audio is the assistant's audio reply (ID, base64 data, transcript).
	Audio AudioMessage `json:"audio"`

	models.DynamicProperties
}

// PartType returns the "audio" discriminator.
func (OutputAudioContentPart) PartType() string { return "audio" }

func (OutputAudioContentPart) isContentPart() {}

// UnmarshalJSON decodes data into p, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (p *OutputAudioContentPart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON encodes p and merges DynamicProperties.Extra back into the
// resulting object.
func (p OutputAudioContentPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// RefusalContentPart is the "refusal" content part variant emitted by the
// assistant when it declines to answer. Unknown fields round-trip via the
// embedded DynamicProperties.
type RefusalContentPart struct {
	// Type is the wire "type" discriminator, always "refusal".
	Type string `json:"type"`

	// Refusal carries the assistant's refusal text.
	Refusal string `json:"refusal"`

	models.DynamicProperties
}

// PartType returns the "refusal" discriminator.
func (RefusalContentPart) PartType() string { return "refusal" }

func (RefusalContentPart) isContentPart() {}

// UnmarshalJSON decodes data into p, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (p *RefusalContentPart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON encodes p and merges DynamicProperties.Extra back into the
// resulting object.
func (p RefusalContentPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// File is the nested file reference on a FileContentPart. Exactly one of
// FileID and (FileName + FileData) is set: FileID references a previously
// uploaded file, while FileName/FileData inlines the bytes. Unknown fields
// round-trip via the embedded DynamicProperties.
type File struct {
	// FileID references a previously uploaded file via /v1/files.
	FileID string `json:"file_id,omitempty"`

	// FileName is the display name when inlining bytes via FileData.
	FileName string `json:"file_name,omitempty"`

	// FileData is the base64-encoded inline file payload.
	FileData string `json:"file_data,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into f, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (f *File) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, f) }

// MarshalJSON encodes f and merges DynamicProperties.Extra back into the
// resulting object.
func (f File) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(f) }

// FileContentPart is the "file" content part variant — a reference to a
// previously uploaded file or an inline file payload. Unknown fields
// round-trip via the embedded DynamicProperties.
type FileContentPart struct {
	// Type is the wire "type" discriminator, always "file".
	Type string `json:"type"`

	// File carries the file reference or inline payload.
	File File `json:"file"`

	models.DynamicProperties
}

// PartType returns the "file" discriminator.
func (FileContentPart) PartType() string { return "file" }

func (FileContentPart) isContentPart() {}

// UnmarshalJSON decodes data into p, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (p *FileContentPart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON encodes p and merges DynamicProperties.Extra back into the
// resulting object.
func (p FileContentPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// UnknownContentPart preserves any content part whose "type" discriminator
// this package has not modelled. Type carries the unknown discriminator
// verbatim and every other JSON field lands in DynamicProperties.Extra so the
// part round-trips intact.
type UnknownContentPart struct {
	// Type is the unmodelled wire discriminator, preserved verbatim.
	Type string `json:"type"`

	models.DynamicProperties
}

// PartType returns the unknown discriminator value verbatim.
func (p UnknownContentPart) PartType() string { return p.Type }

func (UnknownContentPart) isContentPart() {}

// UnmarshalJSON decodes data into p, routing every non-type field into
// DynamicProperties.Extra so the unknown part round-trips byte-equivalent.
func (p *UnknownContentPart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON encodes p and merges DynamicProperties.Extra back into the
// resulting object.
func (p UnknownContentPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

var contentPartRegistry = models.PolymorphicRegistry[ContentPart]{
	DiscriminatorField: "type",
	Factories: map[string]func() ContentPart{
		"text":        func() ContentPart { return &TextContentPart{} },
		"image_url":   func() ContentPart { return &ImageURLContentPart{} },
		"input_audio": func() ContentPart { return &InputAudioContentPart{} },
		"audio":       func() ContentPart { return &OutputAudioContentPart{} },
		"refusal":     func() ContentPart { return &RefusalContentPart{} },
		"file":        func() ContentPart { return &FileContentPart{} },
	},
	Fallback: func(disc string) ContentPart { return &UnknownContentPart{Type: disc} },
}

// UnmarshalContentPart decodes a single chat-completions content-part JSON
// object, dispatching on the "type" discriminator. Any type this package has
// not modelled is returned as an UnknownContentPart so the payload still
// round-trips.
func UnmarshalContentPart(data []byte) (ContentPart, error) {
	return contentPartRegistry.UnmarshalOne(data)
}

// UnmarshalContentParts decodes a JSON array of chat-completions content
// parts, dispatching each element on its "type" discriminator. Unmodelled
// types return as UnknownContentPart.
func UnmarshalContentParts(data []byte) ([]ContentPart, error) {
	return contentPartRegistry.UnmarshalSlice(data)
}

// MessageContent is the chat-completions string-or-array content shape: a
// chat message content field may be a plain JSON string OR a JSON array of
// polymorphic ContentPart values. The raw bytes are retained verbatim so a
// MessageContent round-trips byte-equivalent; use IsString/IsArray/IsNull to
// inspect the shape and AsString/AsParts to project into a typed
// representation.
type MessageContent struct {
	raw json.RawMessage
}

// ErrContentNotString is returned by MessageContent.AsString when the content
// is not a JSON string (either absent, null, or an array).
var ErrContentNotString = errors.New("openai/chat: content is not a string")

// ErrContentNotArray is returned by MessageContent.AsParts when the content
// is not a JSON array (either absent, null, or a bare string).
var ErrContentNotArray = errors.New("openai/chat: content is not an array")

// UnmarshalJSON records the raw bytes verbatim, deferring shape detection to
// IsString/IsArray and decoding to AsString/AsParts.
func (c *MessageContent) UnmarshalJSON(data []byte) error {
	c.raw = append(c.raw[:0], data...)
	return nil
}

// MarshalJSON returns the raw content payload. An empty MessageContent emits
// JSON null so it round-trips identically to an absent field.
func (c MessageContent) MarshalJSON() ([]byte, error) {
	if len(c.raw) == 0 {
		return []byte("null"), nil
	}
	return c.raw, nil
}

// Raw returns a defensive copy of the underlying JSON bytes.
func (c MessageContent) Raw() json.RawMessage {
	out := make(json.RawMessage, len(c.raw))
	copy(out, c.raw)
	return out
}

// IsString reports whether the content is a JSON string.
func (c MessageContent) IsString() bool {
	return len(c.raw) > 0 && c.raw[0] == '"'
}

// IsArray reports whether the content is a JSON array.
func (c MessageContent) IsArray() bool {
	return len(c.raw) > 0 && c.raw[0] == '['
}

// IsNull reports whether the content is JSON null or absent.
func (c MessageContent) IsNull() bool {
	if len(c.raw) == 0 {
		return true
	}
	return bytes.Equal(bytes.TrimSpace(c.raw), []byte("null"))
}

// AsString decodes the content as a JSON string. Returns ErrContentNotString
// when the underlying shape is anything else.
func (c MessageContent) AsString() (string, error) {
	if !c.IsString() {
		return "", ErrContentNotString
	}
	var s string
	if err := json.Unmarshal(c.raw, &s); err != nil {
		return "", fmt.Errorf("openai/chat: decode content string: %w", err)
	}
	return s, nil
}

// AsParts decodes the content as a JSON array of polymorphic ContentPart
// values, dispatching unknown discriminators to UnknownContentPart. Returns
// ErrContentNotArray when the underlying shape is anything else.
func (c MessageContent) AsParts() ([]ContentPart, error) {
	if !c.IsArray() {
		return nil, ErrContentNotArray
	}
	return UnmarshalContentParts(c.raw)
}

// NewStringContent builds a MessageContent that marshals as a JSON string.
func NewStringContent(s string) MessageContent {
	raw, _ := json.Marshal(s)
	return MessageContent{raw: raw}
}

// NewPartsContent builds a MessageContent that marshals as a JSON array of
// the given parts. Returns an error only if a part's MarshalJSON fails.
func NewPartsContent(parts []ContentPart) (MessageContent, error) {
	raw, err := json.Marshal(parts)
	if err != nil {
		return MessageContent{}, fmt.Errorf("openai/chat: marshal parts: %w", err)
	}
	return MessageContent{raw: raw}, nil
}
