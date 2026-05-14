package chat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// ContentPart is implemented by every concrete content-part type and by
// UnknownContentPart. The discriminator is the "type" field.
type ContentPart interface {
	PartType() string

	isContentPart()
}

// TextContentPart is the "text" content part variant.
type TextContentPart struct {
	Type string `json:"type"`

	Text string `json:"text"`

	models.DynamicProperties
}

// PartType returns the "text" discriminator.
func (TextContentPart) PartType() string { return "text" }

func (TextContentPart) isContentPart() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (p *TextContentPart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (p TextContentPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// ImageURL is the nested url+detail object on an ImageURLContentPart.
type ImageURL struct {
	URL string `json:"url"`

	Detail string `json:"detail,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (i *ImageURL) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, i) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (i ImageURL) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(i) }

// ImageURLContentPart is the "image_url" content part variant.
type ImageURLContentPart struct {
	Type string `json:"type"`

	ImageURL ImageURL `json:"image_url"`

	models.DynamicProperties
}

// PartType returns the "image_url" discriminator.
func (ImageURLContentPart) PartType() string { return "image_url" }

func (ImageURLContentPart) isContentPart() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (p *ImageURLContentPart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (p ImageURLContentPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// InputAudio is the nested audio payload on an InputAudioContentPart.
type InputAudio struct {
	Data string `json:"data"`

	Format string `json:"format,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (a *InputAudio) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, a) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (a InputAudio) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// InputAudioContentPart is the "input_audio" content part variant.
type InputAudioContentPart struct {
	Type string `json:"type"`

	InputAudio InputAudio `json:"input_audio"`

	models.DynamicProperties
}

// PartType returns the "input_audio" discriminator.
func (InputAudioContentPart) PartType() string { return "input_audio" }

func (InputAudioContentPart) isContentPart() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (p *InputAudioContentPart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (p InputAudioContentPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// OutputAudioContentPart is the "audio" content part variant emitted by the
// assistant when audio modality is enabled.
type OutputAudioContentPart struct {
	Type string `json:"type"`

	Audio AudioMessage `json:"audio"`

	models.DynamicProperties
}

// PartType returns the "audio" discriminator.
func (OutputAudioContentPart) PartType() string { return "audio" }

func (OutputAudioContentPart) isContentPart() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (p *OutputAudioContentPart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (p OutputAudioContentPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// RefusalContentPart is the "refusal" content part variant emitted by the
// assistant when it declines to answer.
type RefusalContentPart struct {
	Type string `json:"type"`

	Refusal string `json:"refusal"`

	models.DynamicProperties
}

// PartType returns the "refusal" discriminator.
func (RefusalContentPart) PartType() string { return "refusal" }

func (RefusalContentPart) isContentPart() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (p *RefusalContentPart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (p RefusalContentPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// File is the nested file reference on a FileContentPart.
type File struct {
	FileID string `json:"file_id,omitempty"`

	FileName string `json:"file_name,omitempty"`

	FileData string `json:"file_data,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (f *File) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, f) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (f File) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(f) }

// FileContentPart is the "file" content part variant referencing a previously
// uploaded file or inline file payload.
type FileContentPart struct {
	Type string `json:"type"`

	File File `json:"file"`

	models.DynamicProperties
}

// PartType returns the "file" discriminator.
func (FileContentPart) PartType() string { return "file" }

func (FileContentPart) isContentPart() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (p *FileContentPart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (p FileContentPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// UnknownContentPart preserves any content part whose discriminator we have
// not modelled. Type carries the unknown discriminator and every other JSON
// field lands in DynamicProperties.Extra so the part round-trips intact.
type UnknownContentPart struct {
	Type string `json:"type"`

	models.DynamicProperties
}

// PartType returns the unknown discriminator value verbatim.
func (p UnknownContentPart) PartType() string { return p.Type }

func (UnknownContentPart) isContentPart() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (p *UnknownContentPart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
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

// UnmarshalContentPart decodes a single chat content part, dispatching on the
// "type" discriminator and falling back to UnknownContentPart for any
// unrecognised value.
func UnmarshalContentPart(data []byte) (ContentPart, error) {
	return contentPartRegistry.UnmarshalOne(data)
}

// UnmarshalContentParts decodes a JSON array of chat content parts.
func UnmarshalContentParts(data []byte) ([]ContentPart, error) {
	return contentPartRegistry.UnmarshalSlice(data)
}

// MessageContent is the OpenAI string-or-array content shape: a chat message
// content field may be a plain string OR a JSON array of polymorphic
// ContentPart values. The raw bytes are retained verbatim; callers use
// AsString and AsParts to project into the representation they need.
type MessageContent struct {
	raw json.RawMessage
}

// ErrContentNotString is returned by AsString when the content is not a JSON
// string.
var ErrContentNotString = errors.New("openai/chat: content is not a string")

// ErrContentNotArray is returned by AsParts when the content is not a JSON
// array.
var ErrContentNotArray = errors.New("openai/chat: content is not an array")

// UnmarshalJSON records the raw bytes verbatim.
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

// Raw returns the raw JSON bytes of the content.
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

// AsString decodes the content as a JSON string.
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

// AsParts decodes the content as a JSON array of polymorphic content parts.
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
// the given parts.
func NewPartsContent(parts []ContentPart) (MessageContent, error) {
	raw, err := json.Marshal(parts)
	if err != nil {
		return MessageContent{}, fmt.Errorf("openai/chat: marshal parts: %w", err)
	}
	return MessageContent{raw: raw}, nil
}
