// Package messages models the Anthropic /v1/messages wire surface.
//
// Only the polymorphic ContentBlock layer lives here today; the full request
// and response types land in a follow-up task. ContentBlock is the canonical
// demonstration of models.PolymorphicRegistry combined with
// models.DynamicProperties: every concrete block embeds DynamicProperties so
// unknown fields round-trip, and UnknownBlock catches any discriminator value
// Anthropic introduces before we model it.
package messages

import (
	"encoding/json"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// ContentBlock is implemented by every Anthropic content block type, including
// the UnknownBlock fallback used when Anthropic introduces a new discriminator.
type ContentBlock interface {
	BlockType() string

	isContentBlock()
}

// TextBlock is the "text" content block variant.
type TextBlock struct {
	Type string `json:"type"`

	Text string `json:"text"`

	models.DynamicProperties
}

// BlockType returns the "text" discriminator.
func (TextBlock) BlockType() string { return "text" }

func (TextBlock) isContentBlock() {}

// UnmarshalJSON delegates to models.UnmarshalDynamic so unknown fields land in
// DynamicProperties.Extra.
func (b *TextBlock) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, b) }

// MarshalJSON delegates to models.MarshalDynamic so DynamicProperties.Extra is
// merged back into the wire payload.
func (b TextBlock) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(b) }

// ImageSource is the nested source object on an ImageBlock.
type ImageSource struct {
	Type string `json:"type"`

	MediaType string `json:"media_type,omitempty"`

	Data string `json:"data,omitempty"`

	URL string `json:"url,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (s *ImageSource) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, s) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (s ImageSource) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(s) }

// ImageBlock is the "image" content block variant.
type ImageBlock struct {
	Type string `json:"type"`

	Source ImageSource `json:"source"`

	models.DynamicProperties
}

// BlockType returns the "image" discriminator.
func (ImageBlock) BlockType() string { return "image" }

func (ImageBlock) isContentBlock() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (b *ImageBlock) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, b) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (b ImageBlock) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(b) }

// ToolUseBlock is the "tool_use" content block variant emitted by the model
// when it wants to invoke a tool.
type ToolUseBlock struct {
	Type string `json:"type"`

	ID string `json:"id"`

	Name string `json:"name"`

	Input json.RawMessage `json:"input,omitempty"`

	models.DynamicProperties
}

// BlockType returns the "tool_use" discriminator.
func (ToolUseBlock) BlockType() string { return "tool_use" }

func (ToolUseBlock) isContentBlock() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (b *ToolUseBlock) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, b) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (b ToolUseBlock) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(b) }

// ToolResultBlock is the "tool_result" content block variant the client sends
// back to the model after running a tool.
type ToolResultBlock struct {
	Type string `json:"type"`

	ToolUseID string `json:"tool_use_id"`

	Content json.RawMessage `json:"content,omitempty"`

	IsError *bool `json:"is_error,omitempty"`

	models.DynamicProperties
}

// BlockType returns the "tool_result" discriminator.
func (ToolResultBlock) BlockType() string { return "tool_result" }

func (ToolResultBlock) isContentBlock() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (b *ToolResultBlock) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, b) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (b ToolResultBlock) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(b) }

// UnknownBlock preserves any content block whose discriminator we have not
// modelled. The Type field carries the unknown discriminator and every other
// JSON field lands in DynamicProperties.Extra so the block round-trips intact.
type UnknownBlock struct {
	Type string `json:"type"`

	models.DynamicProperties
}

// BlockType returns the unknown discriminator value verbatim.
func (b UnknownBlock) BlockType() string { return b.Type }

func (UnknownBlock) isContentBlock() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (b *UnknownBlock) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, b) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (b UnknownBlock) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(b) }

var blockRegistry = models.PolymorphicRegistry[ContentBlock]{
	DiscriminatorField: "type",
	Factories: map[string]func() ContentBlock{
		"text":        func() ContentBlock { return &TextBlock{} },
		"image":       func() ContentBlock { return &ImageBlock{} },
		"tool_use":    func() ContentBlock { return &ToolUseBlock{} },
		"tool_result": func() ContentBlock { return &ToolResultBlock{} },
	},
	Fallback: func(disc string) ContentBlock { return &UnknownBlock{Type: disc} },
}

// UnmarshalContentBlock decodes a single Anthropic content block, dispatching
// on the "type" discriminator and falling back to UnknownBlock for any
// unrecognised value.
func UnmarshalContentBlock(data []byte) (ContentBlock, error) {
	return blockRegistry.UnmarshalOne(data)
}

// UnmarshalContentBlocks decodes a JSON array of Anthropic content blocks.
func UnmarshalContentBlocks(data []byte) ([]ContentBlock, error) {
	return blockRegistry.UnmarshalSlice(data)
}
