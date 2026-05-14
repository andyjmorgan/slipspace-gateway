// Package content models the Google Gemini v1beta generateContent and
// streamGenerateContent wire surface. Every exported struct embeds
// models.DynamicProperties so provider-added fields round-trip intact.
//
// Parts are polymorphic by key-presence rather than a discriminator field:
// Gemini distinguishes {"text": "..."}, {"inlineData": {...}},
// {"functionCall": {...}}, etc. by which top-level key is set rather than by
// a "type" tag. models.PolymorphicRegistry assumes a single discriminator
// field, so Part dispatch is handled by unmarshalPart below instead of the
// shared registry.
package content

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// ErrEmptyPart is returned when a Part JSON object has no keys at all.
var ErrEmptyPart = errors.New("gemini: part is an empty object")

// Part is implemented by every Gemini content part variant, including the
// UnknownPart fallback used when Gemini introduces a new key we have not
// modelled yet.
type Part interface {
	PartKind() string

	isPart()
}

// TextPart is the {"text": "..."} part variant.
type TextPart struct {
	Text string `json:"text"`

	Thought *bool `json:"thought,omitempty"`

	ThoughtSignature *string `json:"thoughtSignature,omitempty"`

	models.DynamicProperties
}

// PartKind returns "text".
func (TextPart) PartKind() string { return "text" }

func (TextPart) isPart() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (p *TextPart) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, p) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (p TextPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// Blob is the inline binary payload carried by an InlineDataPart.
type Blob struct {
	MimeType string `json:"mimeType,omitempty"`

	Data string `json:"data,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (b *Blob) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, b) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (b Blob) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(b) }

// InlineDataPart is the {"inlineData": {...}} part variant.
type InlineDataPart struct {
	InlineData Blob `json:"inlineData"`

	models.DynamicProperties
}

// PartKind returns "inlineData".
func (InlineDataPart) PartKind() string { return "inlineData" }

func (InlineDataPart) isPart() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (p *InlineDataPart) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, p) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (p InlineDataPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// FileData is the URI-referenced payload carried by a FileDataPart.
type FileData struct {
	MimeType string `json:"mimeType,omitempty"`

	FileURI string `json:"fileUri,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (f *FileData) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, f) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (f FileData) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(f) }

// FileDataPart is the {"fileData": {...}} part variant.
type FileDataPart struct {
	FileData FileData `json:"fileData"`

	models.DynamicProperties
}

// PartKind returns "fileData".
func (FileDataPart) PartKind() string { return "fileData" }

func (FileDataPart) isPart() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (p *FileDataPart) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, p) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (p FileDataPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// FunctionCall is the model's request to invoke a registered tool.
type FunctionCall struct {
	ID string `json:"id,omitempty"`

	Name string `json:"name,omitempty"`

	Args json.RawMessage `json:"args,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *FunctionCall) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c FunctionCall) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// FunctionCallPart is the {"functionCall": {...}} part variant emitted by the
// model when it wants the client to run a tool.
type FunctionCallPart struct {
	FunctionCall FunctionCall `json:"functionCall"`

	models.DynamicProperties
}

// PartKind returns "functionCall".
func (FunctionCallPart) PartKind() string { return "functionCall" }

func (FunctionCallPart) isPart() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (p *FunctionCallPart) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, p) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (p FunctionCallPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// FunctionResponse is the client's reply describing the result of a tool call.
type FunctionResponse struct {
	ID string `json:"id,omitempty"`

	Name string `json:"name,omitempty"`

	Response json.RawMessage `json:"response,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (r *FunctionResponse) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, r) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (r FunctionResponse) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// FunctionResponsePart is the {"functionResponse": {...}} part variant.
type FunctionResponsePart struct {
	FunctionResponse FunctionResponse `json:"functionResponse"`

	models.DynamicProperties
}

// PartKind returns "functionResponse".
func (FunctionResponsePart) PartKind() string { return "functionResponse" }

func (FunctionResponsePart) isPart() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (p *FunctionResponsePart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (p FunctionResponsePart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// ExecutableCode describes a snippet the model intends the code-execution tool
// to run.
type ExecutableCode struct {
	Language string `json:"language,omitempty"`

	Code string `json:"code,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (e *ExecutableCode) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, e) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (e ExecutableCode) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(e) }

// ExecutableCodePart is the {"executableCode": {...}} part variant.
type ExecutableCodePart struct {
	ExecutableCode ExecutableCode `json:"executableCode"`

	models.DynamicProperties
}

// PartKind returns "executableCode".
func (ExecutableCodePart) PartKind() string { return "executableCode" }

func (ExecutableCodePart) isPart() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (p *ExecutableCodePart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (p ExecutableCodePart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// CodeExecutionResult is the outcome of running an ExecutableCode snippet.
type CodeExecutionResult struct {
	Outcome string `json:"outcome,omitempty"`

	Output string `json:"output,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *CodeExecutionResult) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, c)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c CodeExecutionResult) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// CodeExecutionResultPart is the {"codeExecutionResult": {...}} part variant.
type CodeExecutionResultPart struct {
	CodeExecutionResult CodeExecutionResult `json:"codeExecutionResult"`

	models.DynamicProperties
}

// PartKind returns "codeExecutionResult".
func (CodeExecutionResultPart) PartKind() string { return "codeExecutionResult" }

func (CodeExecutionResultPart) isPart() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (p *CodeExecutionResultPart) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, p)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (p CodeExecutionResultPart) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(p) }

// UnknownPart preserves any Part whose key shape we do not recognise. The
// entire JSON payload is stored in DynamicProperties.Extra so the part
// round-trips byte-equivalent.
type UnknownPart struct {
	models.DynamicProperties
}

// PartKind returns "unknown" — UnknownPart has no canonical key. The original
// JSON keys are preserved verbatim via DynamicProperties.Extra.
func (UnknownPart) PartKind() string { return "unknown" }

func (UnknownPart) isPart() {}

// UnmarshalJSON routes every field through DynamicProperties.
func (p *UnknownPart) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, p) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
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

// UnmarshalPart decodes a single Gemini Part by inspecting which top-level key
// is present. Unknown shapes round-trip via UnknownPart.
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

// UnmarshalParts decodes a JSON array of Gemini Parts.
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
