package responses

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/andyjmorgan/slipspace-gateway/models"
)

// ToolDefinition is the polymorphic interface implemented by every tool a
// ResponsesRequest may advertise in its tools[] array — FunctionTool,
// CustomTool, ToolSearchTool, WebSearchTool, ImageGenerationTool, and the
// UnknownTool fallback.
//
// The discriminator is the "type" field. Unknown discriminator values are
// dispatched to UnknownTool, which preserves both the type value AND any
// sibling JSON fields via DynamicProperties so the tool round-trips intact —
// the ChatGPT-backend Responses surface (Codex) ships tool kinds beyond the
// public OpenAI catalogue, and the gateway must not drop them.
//
// Decode a tools array via UnmarshalTools (or let ToolList.UnmarshalJSON do it
// for you) rather than calling json.Unmarshal into a []ToolDefinition directly
// — Go cannot pick the concrete type from a bare interface, so registry
// dispatch is what makes the round-trip work.
type ToolDefinition interface {
	// ToolType returns the wire discriminator value of the concrete tool
	// type ("function", "custom", "web_search", ...).
	ToolType() string

	isToolDefinition()
}

// FunctionTool is a developer-declared callable function — the dominant tool
// variant. Unknown fields round-trip via the embedded DynamicProperties.
type FunctionTool struct {
	// Type is the discriminator, always "function".
	Type string `json:"type"`

	// Name is the function identifier the model emits when calling the tool.
	Name string `json:"name,omitempty"`

	// Description is the natural-language description the model uses to
	// decide when to call the tool.
	Description string `json:"description,omitempty"`

	// Parameters is the JSON-Schema document describing the function's
	// arguments. Kept raw so callers can build or inspect schemas without
	// going through a typed schema model.
	Parameters json.RawMessage `json:"parameters,omitempty"`

	// Strict requests strict JSON-Schema enforcement on the model's
	// generated arguments.
	Strict *bool `json:"strict,omitempty"`

	models.DynamicProperties
}

// ToolType returns the function discriminator.
func (FunctionTool) ToolType() string  { return "function" }
func (FunctionTool) isToolDefinition() {}

// UnmarshalJSON decodes data into t, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (t *FunctionTool) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON encodes t and merges DynamicProperties.Extra back into the
// resulting object.
func (t FunctionTool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// CustomTool is a free-form custom tool whose input is constrained by a Format
// grammar rather than a JSON-Schema parameter document — Codex's apply_patch
// tool is the canonical example. Unknown fields round-trip via the embedded
// DynamicProperties.
type CustomTool struct {
	// Type is the discriminator, always "custom".
	Type string `json:"type"`

	// Name is the tool identifier the model emits when calling the tool.
	Name string `json:"name,omitempty"`

	// Description is the natural-language description the model uses to
	// decide when to call the tool.
	Description string `json:"description,omitempty"`

	// Format constrains the tool's free-form input (e.g. a Lark grammar).
	Format CustomToolFormat `json:"format,omitempty"`

	models.DynamicProperties
}

// ToolType returns the custom discriminator.
func (CustomTool) ToolType() string  { return "custom" }
func (CustomTool) isToolDefinition() {}

// UnmarshalJSON decodes data into t, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (t *CustomTool) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON encodes t and merges DynamicProperties.Extra back into the
// resulting object.
func (t CustomTool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// CustomToolFormat is the grammar constraining a CustomTool's free-form input.
// It is its own Unknown-guarded struct so a future Format shape round-trips via
// DynamicProperties rather than being dropped.
type CustomToolFormat struct {
	// Type is the format kind (e.g. "grammar").
	Type string `json:"type,omitempty"`

	// Syntax is the grammar syntax when Type == "grammar" (e.g. "lark").
	Syntax string `json:"syntax,omitempty"`

	// Definition is the grammar definition text in the declared Syntax.
	Definition string `json:"definition,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into f, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (f *CustomToolFormat) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, f) }

// MarshalJSON encodes f and merges DynamicProperties.Extra back into the
// resulting object.
func (f CustomToolFormat) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(f) }

// ToolSearchTool advertises client-side / deferred tool discovery (MCP-style),
// where the host resolves the concrete tool set at call time. Unknown fields
// round-trip via the embedded DynamicProperties.
type ToolSearchTool struct {
	// Type is the discriminator, always "tool_search".
	Type string `json:"type"`

	// Execution selects where the search runs (e.g. "client").
	Execution string `json:"execution,omitempty"`

	// Description is the natural-language description of the searchable tool
	// surface.
	Description string `json:"description,omitempty"`

	// Parameters is the JSON-Schema document scoping the search. Kept raw so
	// callers can build or inspect schemas without a typed schema model.
	Parameters json.RawMessage `json:"parameters,omitempty"`

	models.DynamicProperties
}

// ToolType returns the tool_search discriminator.
func (ToolSearchTool) ToolType() string  { return "tool_search" }
func (ToolSearchTool) isToolDefinition() {}

// UnmarshalJSON decodes data into t, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (t *ToolSearchTool) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON encodes t and merges DynamicProperties.Extra back into the
// resulting object.
func (t ToolSearchTool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// WebSearchTool advertises the built-in web-search tool. Unknown fields
// round-trip via the embedded DynamicProperties.
type WebSearchTool struct {
	// Type is the discriminator, always "web_search".
	Type string `json:"type"`

	// ExternalWebAccess enables fetching pages outside the model's cached
	// index when non-nil and true; nil defers to the server default.
	ExternalWebAccess *bool `json:"external_web_access,omitempty"`

	// SearchContentTypes lists the content modalities to search over (e.g.
	// ["text","image"]).
	SearchContentTypes []string `json:"search_content_types,omitempty"`

	models.DynamicProperties
}

// ToolType returns the web_search discriminator.
func (WebSearchTool) ToolType() string  { return "web_search" }
func (WebSearchTool) isToolDefinition() {}

// UnmarshalJSON decodes data into t, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (t *WebSearchTool) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON encodes t and merges DynamicProperties.Extra back into the
// resulting object.
func (t WebSearchTool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// ImageGenerationTool advertises the built-in image-generation tool. Unknown
// fields round-trip via the embedded DynamicProperties.
type ImageGenerationTool struct {
	// Type is the discriminator, always "image_generation".
	Type string `json:"type"`

	// OutputFormat is the generated-image encoding (e.g. "png").
	OutputFormat string `json:"output_format,omitempty"`

	models.DynamicProperties
}

// ToolType returns the image_generation discriminator.
func (ImageGenerationTool) ToolType() string  { return "image_generation" }
func (ImageGenerationTool) isToolDefinition() {}

// UnmarshalJSON decodes data into t, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (t *ImageGenerationTool) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, t)
}

// MarshalJSON encodes t and merges DynamicProperties.Extra back into the
// resulting object.
func (t ImageGenerationTool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// UnknownTool preserves any tool whose "type" discriminator this package has
// not modelled. Type carries the unknown discriminator verbatim and every
// other JSON field lands in DynamicProperties.Extra so the tool round-trips
// intact — critical for forward-compatibility with tool kinds OpenAI/Codex
// ship between releases.
type UnknownTool struct {
	// Type is the unmodelled wire discriminator, preserved verbatim.
	Type string `json:"type"`

	models.DynamicProperties
}

// ToolType returns the unknown discriminator value verbatim.
func (t UnknownTool) ToolType() string { return t.Type }
func (UnknownTool) isToolDefinition()  {}

// UnmarshalJSON decodes data into t, routing every non-type field into
// DynamicProperties.Extra so the unknown tool round-trips byte-equivalent.
func (t *UnknownTool) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON encodes t and merges DynamicProperties.Extra back into the
// resulting object.
func (t UnknownTool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

var toolRegistry = models.PolymorphicRegistry[ToolDefinition]{
	DiscriminatorField: "type",
	Factories: map[string]func() ToolDefinition{
		"function":         func() ToolDefinition { return &FunctionTool{} },
		"custom":           func() ToolDefinition { return &CustomTool{} },
		"tool_search":      func() ToolDefinition { return &ToolSearchTool{} },
		"web_search":       func() ToolDefinition { return &WebSearchTool{} },
		"image_generation": func() ToolDefinition { return &ImageGenerationTool{} },
	},
	Fallback: func(disc string) ToolDefinition { return &UnknownTool{Type: disc} },
}

// UnmarshalTool decodes a single Responses tool JSON object, dispatching on the
// "type" discriminator. Any type this package has not modelled is returned as
// an UnknownTool so the payload still round-trips.
func UnmarshalTool(data []byte) (ToolDefinition, error) {
	return toolRegistry.UnmarshalOne(data)
}

// UnmarshalTools decodes a JSON array of Responses tools, dispatching each
// element on its "type" discriminator. Unmodelled types return as UnknownTool.
func UnmarshalTools(data []byte) ([]ToolDefinition, error) {
	return toolRegistry.UnmarshalSlice(data)
}

// ToolList is the typed, range-able form of a ResponsesRequest tools[] array.
// It holds concrete ToolDefinition variants while preserving polymorphic
// round-tripping: UnmarshalJSON dispatches each element through the tool
// registry, MarshalJSON re-emits each element via its concrete MarshalJSON.
type ToolList []ToolDefinition

// UnmarshalJSON decodes a tools array into tl, dispatching each element on its
// "type" discriminator. A JSON null leaves tl nil.
func (tl *ToolList) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*tl = nil
		return nil
	}
	tools, err := UnmarshalTools(data)
	if err != nil {
		return fmt.Errorf("responses: unmarshal tools: %w", err)
	}
	*tl = tools
	return nil
}

// MarshalJSON encodes tl as a JSON array, delegating each element to its
// concrete MarshalJSON so DynamicProperties round-trip.
func (tl ToolList) MarshalJSON() ([]byte, error) {
	return json.Marshal([]ToolDefinition(tl))
}
