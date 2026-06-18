// Package content models Google Gemini's v1beta generateContent and
// streamGenerateContent wire surface — the request body, the response
// shape, and the polymorphic Part hierarchy that lives inside Content.
//
// Every exported struct embeds models.DynamicProperties so fields Google
// ships after this package was built still round-trip when a request or
// response flows through the gateway.
//
// Parts are polymorphic by key-presence rather than a discriminator field:
// Gemini distinguishes {"text": "..."}, {"inlineData": {...}},
// {"functionCall": {...}}, etc. by which top-level key is set rather than by
// a "type" tag. models.PolymorphicRegistry assumes a single discriminator
// field, so Part dispatch is handled by UnmarshalPart below instead of the
// shared registry.
package content

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/andyjmorgan/slipspace-gateway/models"
)

// ErrEmptyPart is returned by UnmarshalPart when the JSON payload is an
// object with no keys at all — there is no key to dispatch on and no fields
// to round-trip, so the decoder refuses rather than silently producing an
// UnknownPart.
var ErrEmptyPart = errors.New("gemini: part is an empty object")

// Part is the polymorphic interface implemented by every Gemini content-part
// variant — TextPart, InlineDataPart, FileDataPart, FunctionCallPart,
// FunctionResponsePart, ExecutableCodePart, CodeExecutionResultPart, and the
// UnknownPart fallback.
//
// Unlike the OpenAI/Anthropic content blocks, Gemini Parts have no "type"
// discriminator field; the variant is determined by which top-level key is
// present in the JSON object. Unknown key shapes are dispatched to
// UnknownPart, which preserves every field via DynamicProperties so the
// part round-trips byte-equivalent.
//
// Decode a Parts JSON array via UnmarshalParts (or let Content.UnmarshalJSON
// do it for you) rather than calling json.Unmarshal into a []Part directly
// — Go cannot pick the concrete type from a bare interface, and the
// key-presence dispatch lives in UnmarshalPart, not in the json package.
type Part interface {
	// PartKind returns a stable identifier for the concrete part type
	// ("text", "inlineData", ..., or "unknown"). The string is not a
	// wire discriminator; it is for caller-side dispatch only.
	PartKind() string

	isPart()
}

// TextPart is the {"text": "..."} part variant — plain text inside a
// Gemini Content's parts array. Unknown fields round-trip via the embedded
// DynamicProperties.
type TextPart struct {
	// Text is the part's literal text content.
	Text string `json:"text"`

	// Thought marks the part as a thinking-trace fragment (only emitted
	// when GenerationConfig.ThinkingConfig.IncludeThoughts is true).
	Thought *bool `json:"thought,omitempty"`

	// ThoughtSignature is the cryptographic signature attached to a
	// thinking part.
	ThoughtSignature *string `json:"thoughtSignature,omitempty"`

	models.DynamicProperties
}

// PartKind returns "text".
func (TextPart) PartKind() string { return "text" }

func (TextPart) isPart() {}

// UnmarshalJSON decodes data into p, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (p *TextPart) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, p) }

// MarshalJSON encodes p and merges DynamicProperties.Extra back into the
// resulting object.
func (p TextPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// Blob is the inline binary payload carried by an InlineDataPart. Unknown
// fields round-trip via the embedded DynamicProperties.
type Blob struct {
	// MimeType is the IANA media type of the inline data (e.g.,
	// "image/png").
	MimeType string `json:"mimeType,omitempty"`

	// Data is the base64-encoded payload.
	Data string `json:"data,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into b, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (b *Blob) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, b) }

// MarshalJSON encodes b and merges DynamicProperties.Extra back into the
// resulting object.
func (b Blob) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(b) }

// InlineDataPart is the {"inlineData": {...}} part variant — a media blob
// inlined as base64. Unknown fields round-trip via the embedded
// DynamicProperties.
type InlineDataPart struct {
	// InlineData carries the blob's media type and bytes.
	InlineData Blob `json:"inlineData"`

	models.DynamicProperties
}

// PartKind returns "inlineData".
func (InlineDataPart) PartKind() string { return "inlineData" }

func (InlineDataPart) isPart() {}

// UnmarshalJSON decodes data into p, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (p *InlineDataPart) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, p) }

// MarshalJSON encodes p and merges DynamicProperties.Extra back into the
// resulting object.
func (p InlineDataPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// FileData is the URI-referenced payload carried by a FileDataPart. Unknown
// fields round-trip via the embedded DynamicProperties.
type FileData struct {
	// MimeType is the IANA media type of the referenced file.
	MimeType string `json:"mimeType,omitempty"`

	// FileURI references a previously uploaded file (typically a
	// gs:// or generative-language URI).
	FileURI string `json:"fileUri,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into f, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (f *FileData) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, f) }

// MarshalJSON encodes f and merges DynamicProperties.Extra back into the
// resulting object.
func (f FileData) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(f) }

// FileDataPart is the {"fileData": {...}} part variant — a media reference
// to a previously uploaded file. Unknown fields round-trip via the
// embedded DynamicProperties.
type FileDataPart struct {
	// FileData carries the file's media type and URI.
	FileData FileData `json:"fileData"`

	models.DynamicProperties
}

// PartKind returns "fileData".
func (FileDataPart) PartKind() string { return "fileData" }

func (FileDataPart) isPart() {}

// UnmarshalJSON decodes data into p, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (p *FileDataPart) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, p) }

// MarshalJSON encodes p and merges DynamicProperties.Extra back into the
// resulting object.
func (p FileDataPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// FunctionCall is the model's request to invoke a registered tool. Unknown
// fields round-trip via the embedded DynamicProperties.
type FunctionCall struct {
	// ID is the Gemini-assigned call identifier when the model emits
	// one; echoed back on the corresponding FunctionResponse.
	ID string `json:"id,omitempty"`

	// Name is the function identifier the model chose to invoke.
	Name string `json:"name,omitempty"`

	// Args is the structured argument payload (JSON object). Kept raw
	// so callers route it without going through a typed schema model.
	Args json.RawMessage `json:"args,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (c *FunctionCall) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c FunctionCall) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// FunctionCallPart is the {"functionCall": {...}} part variant emitted by
// the model when it wants the client to run a tool. Unknown fields
// round-trip via the embedded DynamicProperties.
type FunctionCallPart struct {
	// FunctionCall carries the call's name and arguments.
	FunctionCall FunctionCall `json:"functionCall"`

	// ThoughtSignature is the cryptographic signature Gemini 2.5 attaches
	// to a function-call part produced after a thinking step. It must
	// round-trip verbatim — the client echoes it back on the next turn so
	// the model can resume the thought it interrupted to call the tool;
	// dropping it breaks multi-turn thinking with tools.
	ThoughtSignature *string `json:"thoughtSignature,omitempty"`

	models.DynamicProperties
}

// PartKind returns "functionCall".
func (FunctionCallPart) PartKind() string { return "functionCall" }

func (FunctionCallPart) isPart() {}

// UnmarshalJSON decodes data into p, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (p *FunctionCallPart) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, p) }

// MarshalJSON encodes p and merges DynamicProperties.Extra back into the
// resulting object.
func (p FunctionCallPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// FunctionResponse is the client's reply describing the result of a tool
// call. Unknown fields round-trip via the embedded DynamicProperties.
type FunctionResponse struct {
	// ID echoes the FunctionCall.ID this response answers.
	ID string `json:"id,omitempty"`

	// Name is the function identifier this response corresponds to.
	Name string `json:"name,omitempty"`

	// Response is the structured result payload (JSON object). Kept raw
	// so callers route it without going through a typed schema model.
	Response json.RawMessage `json:"response,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into r, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (r *FunctionResponse) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, r) }

// MarshalJSON encodes r and merges DynamicProperties.Extra back into the
// resulting object.
func (r FunctionResponse) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// FunctionResponsePart is the {"functionResponse": {...}} part variant —
// the client's reply carrying the result of a prior FunctionCallPart.
// Unknown fields round-trip via the embedded DynamicProperties.
type FunctionResponsePart struct {
	// FunctionResponse carries the response's name and result payload.
	FunctionResponse FunctionResponse `json:"functionResponse"`

	models.DynamicProperties
}

// PartKind returns "functionResponse".
func (FunctionResponsePart) PartKind() string { return "functionResponse" }

func (FunctionResponsePart) isPart() {}

// UnmarshalJSON decodes data into p, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (p *FunctionResponsePart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON encodes p and merges DynamicProperties.Extra back into the
// resulting object.
func (p FunctionResponsePart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// ExecutableCode describes a snippet the model intends the code-execution
// tool to run. Unknown fields round-trip via the embedded DynamicProperties.
type ExecutableCode struct {
	// Language is the snippet's language (e.g., "PYTHON").
	Language string `json:"language,omitempty"`

	// Code is the source the tool will execute.
	Code string `json:"code,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into e, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (e *ExecutableCode) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, e) }

// MarshalJSON encodes e and merges DynamicProperties.Extra back into the
// resulting object.
func (e ExecutableCode) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// ExecutableCodePart is the {"executableCode": {...}} part variant — code
// the model emitted to be run by the code-execution tool. Unknown fields
// round-trip via the embedded DynamicProperties.
type ExecutableCodePart struct {
	// ExecutableCode carries the snippet's language and source.
	ExecutableCode ExecutableCode `json:"executableCode"`

	models.DynamicProperties
}

// PartKind returns "executableCode".
func (ExecutableCodePart) PartKind() string { return "executableCode" }

func (ExecutableCodePart) isPart() {}

// UnmarshalJSON decodes data into p, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (p *ExecutableCodePart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON encodes p and merges DynamicProperties.Extra back into the
// resulting object.
func (p ExecutableCodePart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// CodeExecutionResult is the outcome of running an ExecutableCode snippet.
// Unknown fields round-trip via the embedded DynamicProperties.
type CodeExecutionResult struct {
	// Outcome is Gemini's machine-readable result ("OUTCOME_OK",
	// "OUTCOME_FAILED", ...).
	Outcome string `json:"outcome,omitempty"`

	// Output is the captured stdout/stderr from the execution.
	Output string `json:"output,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (c *CodeExecutionResult) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, c)
}

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c CodeExecutionResult) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// CodeExecutionResultPart is the {"codeExecutionResult": {...}} part
// variant — the outcome of running a prior ExecutableCodePart. Unknown
// fields round-trip via the embedded DynamicProperties.
type CodeExecutionResultPart struct {
	// CodeExecutionResult carries the run's outcome and output.
	CodeExecutionResult CodeExecutionResult `json:"codeExecutionResult"`

	models.DynamicProperties
}

// PartKind returns "codeExecutionResult".
func (CodeExecutionResultPart) PartKind() string { return "codeExecutionResult" }

func (CodeExecutionResultPart) isPart() {}

// UnmarshalJSON decodes data into p, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (p *CodeExecutionResultPart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON encodes p and merges DynamicProperties.Extra back into the
// resulting object.
func (p CodeExecutionResultPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// UnknownPart preserves any Part whose key shape this package does not
// recognise. The entire JSON payload is stored in DynamicProperties.Extra
// so the part round-trips byte-equivalent — critical for
// forward-compatibility with new Part kinds Google ships between releases.
type UnknownPart struct {
	models.DynamicProperties
}

// PartKind returns "unknown" — UnknownPart has no canonical key; the
// original JSON keys are preserved verbatim via DynamicProperties.Extra.
func (UnknownPart) PartKind() string { return "unknown" }

func (UnknownPart) isPart() {}

// UnmarshalJSON decodes data into p, routing every field into
// DynamicProperties.Extra so the unknown part round-trips byte-equivalent.
func (p *UnknownPart) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, p) }

// MarshalJSON encodes p and merges DynamicProperties.Extra back into the
// resulting object.
func (p UnknownPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// partFactories maps a Gemini Part's identifying top-level key to the factory
// that produces the matching concrete type.
var partFactories = map[string]func() Part{
	"text":                func() Part { return &TextPart{} },
	"inlineData":          func() Part { return &InlineDataPart{} },
	"fileData":            func() Part { return &FileDataPart{} },
	"functionCall":        func() Part { return &FunctionCallPart{} },
	"functionResponse":    func() Part { return &FunctionResponsePart{} },
	"executableCode":      func() Part { return &ExecutableCodePart{} },
	"codeExecutionResult": func() Part { return &CodeExecutionResultPart{} },
}

// UnmarshalPart decodes a single Gemini Part JSON object by inspecting
// which top-level key is present. Returns ErrEmptyPart when the object has
// no keys at all; any other shape this package has not modelled is returned
// as an UnknownPart so the payload still round-trips.
func UnmarshalPart(data []byte) (Part, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("gemini: unmarshal part: %w", err)
	}
	if len(raw) == 0 {
		return nil, ErrEmptyPart
	}
	for key, factory := range partFactories {
		if _, ok := raw[key]; ok {
			v := factory()
			if err := json.Unmarshal(data, v); err != nil {
				return nil, fmt.Errorf("gemini: unmarshal %q part: %w", key, err)
			}
			return v, nil
		}
	}
	v := &UnknownPart{}
	if err := json.Unmarshal(data, v); err != nil {
		return nil, fmt.Errorf("gemini: unmarshal unknown part: %w", err)
	}
	return v, nil
}

// UnmarshalParts decodes a JSON array of Gemini Parts, dispatching each
// element via UnmarshalPart. Unmodelled shapes return as UnknownPart.
func UnmarshalParts(data []byte) ([]Part, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, fmt.Errorf("gemini: unmarshal parts array: %w", err)
	}
	out := make([]Part, len(raws))
	for i, raw := range raws {
		v, err := UnmarshalPart(raw)
		if err != nil {
			return nil, fmt.Errorf("gemini: part %d: %w", i, err)
		}
		out[i] = v
	}
	return out, nil
}
