// Package messages models Anthropic's POST /v1/messages wire surface — the
// request body, the non-streaming response, and the streaming SSE event
// hierarchy (see stream.go).
//
// Every exported struct embeds models.DynamicProperties so fields Anthropic
// ships after this package was built still round-trip when a request or
// response flows through the gateway. ContentBlock, ContentBlockDelta, and
// StreamEvent are polymorphic; the dedicated UnmarshalX helpers dispatch
// unknown discriminator values to UnknownX fallbacks rather than dropping
// them.
package messages

import (
	"encoding/json"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// ContentBlock is the polymorphic interface implemented by every Anthropic
// content block type — TextBlock, ImageBlock, ToolUseBlock, ToolResultBlock,
// ThinkingBlock, RedactedThinkingBlock, and the UnknownBlock fallback.
//
// The discriminator is the "type" field. Unknown discriminator values are
// dispatched to UnknownBlock, which preserves both the type value AND any
// sibling JSON fields via DynamicProperties so the payload round-trips
// intact.
//
// Decode a content-block array via UnmarshalContentBlocks (or let
// Message.ContentAsBlocks do it for you) rather than calling json.Unmarshal
// into a []ContentBlock directly — Go cannot pick the concrete type from a
// bare interface, so registry dispatch is what makes the round-trip work.
type ContentBlock interface {
	// BlockType returns the wire discriminator value of the concrete
	// block type ("text", "image", ...).
	BlockType() string

	isContentBlock()
}

// TextBlock is the "text" content block variant — plain text inside an
// Anthropic message's content array. Unknown fields round-trip via the
// embedded DynamicProperties.
type TextBlock struct {
	// Type is the wire "type" discriminator, always "text".
	Type string `json:"type"`

	// Text is the block's literal text content.
	Text string `json:"text"`

	// Citations lists the source citations Anthropic attaches to this text
	// when the request enabled citations on a document or search_result
	// input block ({"citations":{"enabled":true}}). Nil/omitted otherwise.
	// Ref: https://docs.anthropic.com/en/docs/build-with-claude/citations
	Citations []Citation `json:"citations,omitempty"`

	models.DynamicProperties
}

// Citation is one source reference Anthropic attaches to a TextBlock when
// citations are enabled on an input document or search result. The concrete
// shape varies by Type (e.g. "char_location", "page_location",
// "content_block_location", "web_search_result_location"); the common fields
// are modelled and any location-specific fields (start/end indices, document
// index, etc.) round-trip via the embedded DynamicProperties.
// Ref: https://docs.anthropic.com/en/docs/build-with-claude/citations
type Citation struct {
	// Type is the citation kind (e.g. "web_search_result_location",
	// "char_location", "page_location").
	Type string `json:"type"`

	// CitedText is the exact source span the model cited.
	CitedText string `json:"cited_text,omitempty"`

	// URL is the cited source URL (present on web_search_result_location).
	URL string `json:"url,omitempty"`

	// Title is the cited source title when the source carries one.
	Title string `json:"title,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (c *Citation) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c Citation) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// BlockType returns the "text" discriminator.
func (TextBlock) BlockType() string { return "text" }

func (TextBlock) isContentBlock() {}

// UnmarshalJSON decodes data into b, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (b *TextBlock) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, b) }

// MarshalJSON encodes b and merges DynamicProperties.Extra back into the
// resulting object.
func (b TextBlock) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(b) }

// ImageSource is the nested source object on an ImageBlock. Anthropic
// supports both base64 ("type":"base64" + MediaType + Data) and URL
// ("type":"url" + URL) sources. Unknown fields round-trip via the embedded
// DynamicProperties.
type ImageSource struct {
	// Type is the source kind ("base64" or "url").
	Type string `json:"type"`

	// MediaType is the IANA media type (e.g., "image/png") when Type ==
	// "base64".
	MediaType string `json:"media_type,omitempty"`

	// Data is the base64-encoded payload when Type == "base64".
	Data string `json:"data,omitempty"`

	// URL is the image URL when Type == "url".
	URL string `json:"url,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into s, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (s *ImageSource) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, s) }

// MarshalJSON encodes s and merges DynamicProperties.Extra back into the
// resulting object.
func (s ImageSource) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(s) }

// ImageBlock is the "image" content block variant — an inline or
// URL-referenced image inside a message. Unknown fields round-trip via the
// embedded DynamicProperties.
type ImageBlock struct {
	// Type is the wire "type" discriminator, always "image".
	Type string `json:"type"`

	// Source carries the image's media type and bytes (or URL).
	Source ImageSource `json:"source"`

	models.DynamicProperties
}

// BlockType returns the "image" discriminator.
func (ImageBlock) BlockType() string { return "image" }

func (ImageBlock) isContentBlock() {}

// UnmarshalJSON decodes data into b, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (b *ImageBlock) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, b) }

// MarshalJSON encodes b and merges DynamicProperties.Extra back into the
// resulting object.
func (b ImageBlock) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(b) }

// ToolUseBlock is the "tool_use" content block variant emitted by the model
// when it wants to invoke a tool. Unknown fields round-trip via the
// embedded DynamicProperties.
type ToolUseBlock struct {
	// Type is the wire "type" discriminator, always "tool_use".
	Type string `json:"type"`

	// ID is the Anthropic-assigned tool-call identifier; echoed back on
	// the corresponding ToolResultBlock to correlate the result.
	ID string `json:"id"`

	// Name is the tool identifier the model chose to invoke.
	Name string `json:"name"`

	// Input is the structured argument payload (JSON object). Kept raw so
	// callers can route it without going through a typed schema model.
	Input json.RawMessage `json:"input,omitempty"`

	// Caller attributes the tool call to its invoker (e.g. {"type":"direct"}
	// for a top-level call). Anthropic emits this on tool_use blocks to
	// distinguish direct calls from those made by a sub-agent.
	Caller *ToolCaller `json:"caller,omitempty"`

	models.DynamicProperties
}

// ToolCaller attributes a ToolUseBlock to its invoker. Unknown fields
// round-trip via the embedded DynamicProperties.
type ToolCaller struct {
	// Type is the caller kind, e.g. "direct".
	Type string `json:"type,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (c *ToolCaller) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c ToolCaller) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// BlockType returns the "tool_use" discriminator.
func (ToolUseBlock) BlockType() string { return "tool_use" }

func (ToolUseBlock) isContentBlock() {}

// UnmarshalJSON decodes data into b, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (b *ToolUseBlock) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, b) }

// MarshalJSON encodes b and merges DynamicProperties.Extra back into the
// resulting object.
func (b ToolUseBlock) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(b) }

// ToolResultBlock is the "tool_result" content block variant the client
// sends back to the model carrying the outcome of a prior ToolUseBlock.
// Unknown fields round-trip via the embedded DynamicProperties.
type ToolResultBlock struct {
	// Type is the wire "type" discriminator, always "tool_result".
	Type string `json:"type"`

	// ToolUseID echoes the ID of the ToolUseBlock this result answers.
	ToolUseID string `json:"tool_use_id"`

	// Content carries the tool's output. Anthropic accepts both a bare
	// string and an array of content blocks here, so it's kept raw.
	Content json.RawMessage `json:"content,omitempty"`

	// IsError flags the result as a tool failure (so the model can react
	// accordingly).
	IsError *bool `json:"is_error,omitempty"`

	models.DynamicProperties
}

// BlockType returns the "tool_result" discriminator.
func (ToolResultBlock) BlockType() string { return "tool_result" }

func (ToolResultBlock) isContentBlock() {}

// UnmarshalJSON decodes data into b, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (b *ToolResultBlock) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, b) }

// MarshalJSON encodes b and merges DynamicProperties.Extra back into the
// resulting object.
func (b ToolResultBlock) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(b) }

// ThinkingBlock is the "thinking" content block variant carrying the model's
// extended-thinking trace. The non-streaming response embeds it directly; the
// streaming response delivers it as a thinking content_block_start followed by
// thinking_delta fragments and a terminal signature_delta (see stream.go).
//
// Signature is a cryptographic attestation over the thinking content that the
// client MUST echo back verbatim on the assistant turn for tool use to resume.
// It is load-bearing — see the round-trip invariant in CLAUDE.md. Unknown
// fields round-trip via the embedded DynamicProperties.
type ThinkingBlock struct {
	// Type is the wire "type" discriminator, always "thinking".
	Type string `json:"type"`

	// Thinking is the model's reasoning trace.
	Thinking string `json:"thinking"`

	// Signature is the cryptographic signature over the thinking trace.
	// Empty on a content_block_start; populated by the terminal
	// signature_delta when streaming.
	Signature string `json:"signature,omitempty"`

	models.DynamicProperties
}

// BlockType returns the "thinking" discriminator.
func (ThinkingBlock) BlockType() string { return "thinking" }

func (ThinkingBlock) isContentBlock() {}

// UnmarshalJSON decodes data into b, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (b *ThinkingBlock) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, b) }

// MarshalJSON encodes b and merges DynamicProperties.Extra back into the
// resulting object.
func (b ThinkingBlock) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(b) }

// RedactedThinkingBlock is the "redacted_thinking" content block variant —
// thinking the provider has encrypted because it tripped a safety classifier.
// Data is an opaque blob that carries no human-readable content but MUST be
// echoed back verbatim alongside any sibling thinking blocks. Unknown fields
// round-trip via the embedded DynamicProperties.
type RedactedThinkingBlock struct {
	// Type is the wire "type" discriminator, always "redacted_thinking".
	Type string `json:"type"`

	// Data is the opaque encrypted thinking payload.
	Data string `json:"data"`

	models.DynamicProperties
}

// BlockType returns the "redacted_thinking" discriminator.
func (RedactedThinkingBlock) BlockType() string { return "redacted_thinking" }

func (RedactedThinkingBlock) isContentBlock() {}

// UnmarshalJSON decodes data into b, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (b *RedactedThinkingBlock) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, b)
}

// MarshalJSON encodes b and merges DynamicProperties.Extra back into the
// resulting object.
func (b RedactedThinkingBlock) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(b) }

// UnknownBlock preserves any content block whose "type" discriminator this
// package has not modelled. Type carries the unknown discriminator verbatim
// and every other JSON field lands in DynamicProperties.Extra so the block
// round-trips intact — critical for forward-compatibility with new block
// kinds Anthropic ships between releases.
type UnknownBlock struct {
	// Type is the unmodelled wire discriminator, preserved verbatim.
	Type string `json:"type"`

	models.DynamicProperties
}

// BlockType returns the unknown discriminator value verbatim.
func (b UnknownBlock) BlockType() string { return b.Type }

func (UnknownBlock) isContentBlock() {}

// UnmarshalJSON decodes data into b, routing every non-type field into
// DynamicProperties.Extra so the unknown block round-trips byte-equivalent.
func (b *UnknownBlock) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, b) }

// MarshalJSON encodes b and merges DynamicProperties.Extra back into the
// resulting object.
func (b UnknownBlock) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(b) }

var blockRegistry = models.PolymorphicRegistry[ContentBlock]{
	DiscriminatorField: "type",
	Factories: map[string]func() ContentBlock{
		"text":              func() ContentBlock { return &TextBlock{} },
		"image":             func() ContentBlock { return &ImageBlock{} },
		"tool_use":          func() ContentBlock { return &ToolUseBlock{} },
		"tool_result":       func() ContentBlock { return &ToolResultBlock{} },
		"thinking":          func() ContentBlock { return &ThinkingBlock{} },
		"redacted_thinking": func() ContentBlock { return &RedactedThinkingBlock{} },
	},
	Fallback: func(disc string) ContentBlock { return &UnknownBlock{Type: disc} },
}

// UnmarshalContentBlock decodes a single Anthropic content-block JSON object,
// dispatching on the "type" discriminator. Any type this package has not
// modelled is returned as an UnknownBlock so the payload still round-trips.
func UnmarshalContentBlock(data []byte) (ContentBlock, error) {
	return blockRegistry.UnmarshalOne(data)
}

// UnmarshalContentBlocks decodes a JSON array of Anthropic content blocks,
// dispatching each element on its "type" discriminator. Unmodelled types
// return as UnknownBlock.
func UnmarshalContentBlocks(data []byte) ([]ContentBlock, error) {
	return blockRegistry.UnmarshalSlice(data)
}
