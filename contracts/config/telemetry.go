package config

// DefaultMessagesMaxBytes is the per text-field cap applied to
// input.messages / output.messages content (text parts' content, tool-call
// arguments, tool-call response results) when ContentCaptureCaps does not
// override it. Picked to keep a single runaway turn from bloating a
// telemetry record while preserving enough of typical traffic to be
// useful; the unredacted body still flows through the connector spool.
const DefaultMessagesMaxBytes = 32 * 1024

// DefaultSystemInstructionsMaxBytes is the per text-field cap applied to
// the system-instructions parts on a telemetry record when
// ContentCaptureCaps does not override it. Tracks DefaultMessagesMaxBytes
// today — separate knob because operator mental models often distinguish
// "system prompt" from "conversation turn".
const DefaultSystemInstructionsMaxBytes = 32 * 1024

// DefaultToolDefinitionsMaxBytes is the cap on the combined size of every
// tool definition's parameters JSON schema. Larger than the per-message
// cap because schemas legitimately are; once exceeded, parameters are
// dropped wholesale from every definition and only type/name/description
// are emitted.
const DefaultToolDefinitionsMaxBytes = 64 * 1024

// Telemetry is the `telemetry:` top-level block in admin.yaml. Carries
// operator-tunable knobs that shape what the gateway emits to its
// telemetry signals without changing the wire path or the connector
// spool. Today it only nests ContentCapture; the block exists so future
// telemetry-shaping knobs land here rather than spawning a new top-level
// key per feature.
type Telemetry struct {
	// ContentCapture caps the bytes the gateway includes in GenAI span /
	// event content fields. Zero value (block omitted) resolves to the
	// built-in defaults via Resolve().
	ContentCapture ContentCaptureCaps `yaml:"content_capture,omitempty" json:"content_capture,omitempty"`
}

// ContentCaptureCaps carries the three byte caps the reporter consults
// before emitting prompt / response / tool-definition content on the
// GenAI span and operation-details event. Each knob is a *int so the
// loader can distinguish "absent" (nil → default at Resolve) from
// "operator explicitly set zero" (&0 → unbounded). The pointer encoding
// stays inside this package — the resolved form below is plain ints with
// zero meaning unbounded, which is what the reporter wants on the hot
// path.
type ContentCaptureCaps struct {
	// MessagesMaxBytes caps every text field carried under
	// input.messages / output.messages: text parts' content, tool-call
	// response results, and tool-call arguments (whole-document drop on
	// overflow to keep the JSON well-formed). It also caps each tool
	// definition's description field. Nil = default; &0 = unbounded.
	MessagesMaxBytes *int `yaml:"messages_max_bytes,omitempty" json:"messages_max_bytes,omitempty"`

	// SystemInstructionsMaxBytes caps the per-part content under
	// gen_ai.system_instructions. Nil = default; &0 = unbounded.
	SystemInstructionsMaxBytes *int `yaml:"system_instructions_max_bytes,omitempty" json:"system_instructions_max_bytes,omitempty"`

	// ToolDefinitionsMaxBytes caps the combined `parameters` JSON
	// schema size across every tool definition. When the sum exceeds
	// the cap, parameters are dropped wholesale from every definition
	// (type / name / description still emitted). Nil = default;
	// &0 = unbounded.
	ToolDefinitionsMaxBytes *int `yaml:"tool_definitions_max_bytes,omitempty" json:"tool_definitions_max_bytes,omitempty"`
}

// ResolvedContentCaps is the loader-resolved form of ContentCaptureCaps
// the reporter consumes. Each field is a plain int; a value of 0 means
// "no cap", matching the semantics operators see in YAML when they
// explicitly write `messages_max_bytes: 0`. Absent YAML keys are folded
// into the built-in defaults by Resolve() before this struct is built,
// so the reporter never sees nils.
type ResolvedContentCaps struct {
	// Messages is the per-text-field cap on input.messages /
	// output.messages content. 0 = unbounded.
	Messages int

	// SystemInstructions is the per-text-field cap on
	// gen_ai.system_instructions parts. 0 = unbounded.
	SystemInstructions int

	// ToolDefinitions is the combined-bytes cap on tool definitions'
	// parameters JSON schemas. 0 = unbounded.
	ToolDefinitions int
}

// Resolve folds the operator-supplied pointers into a flat
// ResolvedContentCaps the reporter can read on the hot path. Nil pointer
// → built-in default; &0 → unbounded (encoded as 0 in the resolved
// form); &N for any N>0 → N. Resolve is idempotent: feeding its output
// back through pointer-encoded form would yield the same resolved
// struct.
func (c ContentCaptureCaps) Resolve() ResolvedContentCaps {
	return ResolvedContentCaps{
		Messages:           resolveCap(c.MessagesMaxBytes, DefaultMessagesMaxBytes),
		SystemInstructions: resolveCap(c.SystemInstructionsMaxBytes, DefaultSystemInstructionsMaxBytes),
		ToolDefinitions:    resolveCap(c.ToolDefinitionsMaxBytes, DefaultToolDefinitionsMaxBytes),
	}
}

// resolveCap implements the three-state nil/&0/&N rule for one knob.
// Pulled out so each field follows the same logic and the test surface
// is one helper rather than three near-identical branches.
func resolveCap(p *int, def int) int {
	if p == nil {
		return def
	}
	if *p < 0 {
		return def
	}
	return *p
}
