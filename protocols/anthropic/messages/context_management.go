package messages

import (
	"encoding/json"

	"github.com/andyjmorgan/slipspace-gateway/models"
)

// ContextManagementConfig is the request-side context-management configuration
// carried by MessagesRequest.ContextManagement under the context-management
// beta (requires the "anthropic-beta: context-management-2025-06-27" header).
//
// It is DISTINCT from the response-side ContextManagement type (the
// server-applied-edits report in response.go): this is the caller's request
// for context clearing/compaction, that is the server's report of what it
// actually did. The two share the JSON key "context_management" but in
// opposite directions, so they are modelled separately.
//
// Edits is polymorphic — each element is discriminated by its own "type"
// field — so it is dispatched through the ContextEdit registry; unknown edit
// discriminators round-trip via UnknownContextEdit. Unknown top-level fields
// round-trip via the embedded DynamicProperties.
//
// Ref: anthropic-sdk-python beta_context_management_config_param.py —
// BetaContextManagementConfigParam.edits (List[Edit]):
// https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/beta/beta_context_management_config_param.py
type ContextManagementConfig struct {
	// Edits is the ordered list of context-management edit strategies the
	// caller asks the server to apply (e.g. clear_tool_uses_20250919,
	// clear_thinking_20251015, compact_20260112). Each element is a typed
	// object discriminated by its "type" field; unknown discriminators land
	// in UnknownContextEdit so the payload round-trips intact. Kept raw on
	// the wire and dispatched in UnmarshalJSON because Go cannot pick a
	// concrete type from a bare interface slice.
	//
	// omitempty normalizes an empty/absent edits list to an omitted field:
	// the SDK field is total=False (an empty list and an omitted field both
	// mean "no edits"), so collapsing "edits":[] to an omitted key is
	// semantically lossless — unlike the response-side applied_edits report,
	// which keeps its array raw because an explicit [] there is meaningful.
	Edits []ContextEdit `json:"edits,omitempty"`

	models.DynamicProperties
}

// contextManagementConfigRaw mirrors ContextManagementConfig but keeps Edits
// as a RawMessage so UnmarshalDynamic ignores its polymorphic shape; the
// parent UnmarshalJSON dispatches the edit elements afterwards.
type contextManagementConfigRaw struct {
	// Edits is the raw edits array, decoded by the parent UnmarshalJSON. This
	// shadow struct is decode-only (never marshaled), so no omitempty.
	Edits json.RawMessage `json:"edits"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, dispatching the Edits array through the
// polymorphic ContextEdit registry so unknown edit discriminators round-trip
// via UnknownContextEdit; any other unknown top-level field lands in
// DynamicProperties.Extra.
func (c *ContextManagementConfig) UnmarshalJSON(data []byte) error {
	var shadow contextManagementConfigRaw
	if err := models.UnmarshalDynamic(data, &shadow); err != nil {
		return err
	}

	*c = ContextManagementConfig{DynamicProperties: shadow.DynamicProperties}

	if len(shadow.Edits) == 0 || string(shadow.Edits) == "null" {
		return nil
	}
	edits, err := UnmarshalContextEdits(shadow.Edits)
	if err != nil {
		return err
	}
	c.Edits = edits
	return nil
}

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c ContextManagementConfig) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// ContextEdit is the polymorphic interface implemented by every request-side
// context-management edit strategy — ClearToolUsesEdit, ClearThinkingEdit,
// CompactEdit, and the UnknownContextEdit fallback.
//
// The discriminator is the "type" field. Unknown discriminator values are
// dispatched to UnknownContextEdit, which preserves both the type value AND
// any sibling JSON fields via DynamicProperties so the payload round-trips
// intact.
//
// Decode an edit array via UnmarshalContextEdits rather than calling
// json.Unmarshal into a []ContextEdit directly — Go cannot pick the concrete
// type from a bare interface, so registry dispatch is what makes the
// round-trip work.
type ContextEdit interface {
	// EditType returns the wire discriminator value of the concrete edit
	// type ("clear_tool_uses_20250919", "compact_20260112", ...).
	EditType() string

	isContextEdit()
}

// ClearToolUsesEdit is the "clear_tool_uses_20250919" context-management edit:
// it clears older tool-use/tool-result pairs from the context when triggered.
//
// The nested union-typed fields (ClearAtLeast, ClearToolInputs, Keep, Trigger)
// are kept as json.RawMessage rather than fully typed: each is itself a
// polymorphic union in the Anthropic SDK (e.g. Trigger is
// input_tokens|tool_uses, ClearToolInputs is bool|[]string), and issue #315
// only requires typing context_management at the top level. RawMessage +
// DynamicProperties round-trips every nested shape losslessly; promote these
// to typed structs only when a captured sample justifies it. Unknown fields
// round-trip via the embedded DynamicProperties.
//
// Ref: anthropic-sdk-python beta_clear_tool_uses_20250919_edit_param.py:
// https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/beta/beta_clear_tool_uses_20250919_edit_param.py
type ClearToolUsesEdit struct {
	// Type is the wire "type" discriminator, always
	// "clear_tool_uses_20250919".
	Type string `json:"type"`

	// ClearAtLeast is the minimum number of input tokens that must be
	// cleared for the edit to apply ({"type":"input_tokens","value":N}).
	// Kept raw because it is a nested typed object; nil/omitted when unset.
	ClearAtLeast json.RawMessage `json:"clear_at_least,omitempty"`

	// ClearToolInputs selects which tool inputs to clear: a bool (clear all)
	// or an array of tool names. Kept raw because it is a bool|[]string
	// union; nil/omitted when unset.
	ClearToolInputs json.RawMessage `json:"clear_tool_inputs,omitempty"`

	// ExcludeTools lists tool names whose uses are preserved from clearing.
	ExcludeTools []string `json:"exclude_tools,omitempty"`

	// Keep is the number of tool uses to retain in the conversation
	// ({"type":"tool_uses","value":N}). Kept raw because it is a nested
	// typed object; nil/omitted when unset.
	Keep json.RawMessage `json:"keep,omitempty"`

	// Trigger is the condition that triggers the strategy — an
	// input_tokens|tool_uses union ({"type":"input_tokens","value":N}).
	// Kept raw because it is a nested polymorphic object; nil/omitted when
	// unset.
	Trigger json.RawMessage `json:"trigger,omitempty"`

	models.DynamicProperties
}

// EditType returns the "clear_tool_uses_20250919" discriminator.
func (ClearToolUsesEdit) EditType() string { return "clear_tool_uses_20250919" }

func (ClearToolUsesEdit) isContextEdit() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *ClearToolUsesEdit) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e ClearToolUsesEdit) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// ClearThinkingEdit is the "clear_thinking_20251015" context-management edit:
// it removes thinking blocks from older assistant turns when triggered.
//
// Keep is kept as json.RawMessage because it is a union in the Anthropic SDK
// (thinking_turns object | all_thinking_turns object | the literal string
// "all"); RawMessage round-trips every shape losslessly. Unknown fields
// round-trip via the embedded DynamicProperties.
//
// Ref: anthropic-sdk-python beta_clear_thinking_20251015_edit_param.py:
// https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/beta/beta_clear_thinking_20251015_edit_param.py
type ClearThinkingEdit struct {
	// Type is the wire "type" discriminator, always
	// "clear_thinking_20251015".
	Type string `json:"type"`

	// Keep is the number of most recent assistant turns to keep thinking
	// blocks for — a thinking_turns|all_thinking_turns object or the literal
	// string "all". Kept raw because it is a union; nil/omitted when unset.
	Keep json.RawMessage `json:"keep,omitempty"`

	models.DynamicProperties
}

// EditType returns the "clear_thinking_20251015" discriminator.
func (ClearThinkingEdit) EditType() string { return "clear_thinking_20251015" }

func (ClearThinkingEdit) isContextEdit() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *ClearThinkingEdit) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e ClearThinkingEdit) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// CompactEdit is the "compact_20260112" context-management edit: it
// automatically compacts (summarizes) older context when the trigger
// threshold is reached.
//
// Trigger is kept as json.RawMessage because it is a nested typed object
// (input_tokens trigger, {"type":"input_tokens","value":N}); RawMessage
// round-trips it losslessly. Unknown fields round-trip via the embedded
// DynamicProperties.
//
// Ref: anthropic-sdk-python beta_compact_20260112_edit_param.py:
// https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/beta/beta_compact_20260112_edit_param.py
type CompactEdit struct {
	// Type is the wire "type" discriminator, always "compact_20260112".
	Type string `json:"type"`

	// Instructions carries additional instructions for the summarization.
	Instructions string `json:"instructions,omitempty"`

	// PauseAfterCompaction requests the server pause after compaction and
	// return the compaction block to the caller. Pointer so an explicit
	// false round-trips distinctly from an omitted field.
	PauseAfterCompaction *bool `json:"pause_after_compaction,omitempty"`

	// Trigger is the condition that triggers compaction — an input_tokens
	// object ({"type":"input_tokens","value":N}), defaulting to 150000 input
	// tokens server-side. Kept raw because it is a nested typed object;
	// nil/omitted when unset.
	Trigger json.RawMessage `json:"trigger,omitempty"`

	models.DynamicProperties
}

// EditType returns the "compact_20260112" discriminator.
func (CompactEdit) EditType() string { return "compact_20260112" }

func (CompactEdit) isContextEdit() {}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *CompactEdit) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, e) }

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e CompactEdit) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// UnknownContextEdit preserves any context-management edit whose "type"
// discriminator this package has not modelled. Type carries the unknown
// discriminator verbatim and every other JSON field lands in
// DynamicProperties.Extra so the edit round-trips intact — critical for
// forward-compatibility with new edit strategies Anthropic ships between
// releases (the strategy names are date-versioned and grow over time).
type UnknownContextEdit struct {
	// Type is the unmodelled wire discriminator, preserved verbatim.
	Type string `json:"type"`

	models.DynamicProperties
}

// EditType returns the unknown discriminator value verbatim.
func (e UnknownContextEdit) EditType() string { return e.Type }

func (UnknownContextEdit) isContextEdit() {}

// UnmarshalJSON decodes data into e, routing every non-type field into
// DynamicProperties.Extra so the unknown edit round-trips byte-equivalent.
func (e *UnknownContextEdit) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, e)
}

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e UnknownContextEdit) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// contextEditRegistry dispatches a context-management edit JSON object to its
// concrete type on the "type" discriminator, falling back to UnknownContextEdit
// for any strategy this package has not modelled.
var contextEditRegistry = models.PolymorphicRegistry[ContextEdit]{
	DiscriminatorField: "type",
	Factories: map[string]func() ContextEdit{
		"clear_tool_uses_20250919": func() ContextEdit { return &ClearToolUsesEdit{} },
		"clear_thinking_20251015":  func() ContextEdit { return &ClearThinkingEdit{} },
		"compact_20260112":         func() ContextEdit { return &CompactEdit{} },
	},
	Fallback: func(disc string) ContextEdit { return &UnknownContextEdit{Type: disc} },
}

// UnmarshalContextEdit decodes a single Anthropic context-management edit JSON
// object, dispatching on the "type" discriminator. Any type this package has
// not modelled is returned as an UnknownContextEdit so the payload still
// round-trips.
func UnmarshalContextEdit(data []byte) (ContextEdit, error) {
	return contextEditRegistry.UnmarshalOne(data)
}

// UnmarshalContextEdits decodes a JSON array of Anthropic context-management
// edits, dispatching each element on its "type" discriminator. Unmodelled
// types return as UnknownContextEdit.
func UnmarshalContextEdits(data []byte) ([]ContextEdit, error) {
	return contextEditRegistry.UnmarshalSlice(data)
}
