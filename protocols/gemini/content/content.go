package content

import (
	"encoding/json"
	"fmt"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// GenerateContentRequest is the request body for Gemini's POST
// /v1beta/models/{model}:generateContent endpoint (and its
// :streamGenerateContent streaming variant). Unknown fields round-trip via
// the embedded DynamicProperties.
type GenerateContentRequest struct {
	// Contents is the conversation history; each Content carries a role
	// plus a slice of polymorphic Parts.
	Contents []Content `json:"contents"`

	// Tools advertises the callable tool catalogue (function
	// declarations and built-in tools).
	Tools []Tool `json:"tools,omitempty"`

	// ToolConfig overrides the default tool-calling behaviour.
	ToolConfig *ToolConfig `json:"toolConfig,omitempty"`

	// SafetySettings overrides default safety thresholds per category.
	SafetySettings []SafetySetting `json:"safetySettings,omitempty"`

	// SystemInstruction is the optional system-style prompt; modelled as
	// a Content so it accepts the same polymorphic Parts shape.
	SystemInstruction *Content `json:"systemInstruction,omitempty"`

	// GenerationConfig controls sampling, output shape, and reasoning
	// behaviour.
	GenerationConfig *GenerationConfig `json:"generationConfig,omitempty"`

	// CachedContent references a previously cached context payload.
	CachedContent *string `json:"cachedContent,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into r, routing any field not declared on the
// struct into DynamicProperties.Extra. The nested Content values dispatch
// their polymorphic Parts via their own UnmarshalJSON.
func (r *GenerateContentRequest) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, r)
}

// MarshalJSON encodes r and merges DynamicProperties.Extra back into the
// resulting object.
func (r GenerateContentRequest) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// Content is a single turn in the conversation: a role plus a slice of
// polymorphic Parts. Unknown fields round-trip via the embedded
// DynamicProperties.
type Content struct {
	// Role is "user" or "model" (Gemini uses "model" where OpenAI uses
	// "assistant").
	Role string `json:"role,omitempty"`

	// Parts is the polymorphic content sequence for the turn — text,
	// inline data, function calls, etc.
	Parts []Part `json:"parts,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c. Each element of "parts" is dispatched
// through UnmarshalPart so unknown part shapes round-trip via UnknownPart;
// any other unknown top-level field lands in DynamicProperties.Extra.
func (c *Content) UnmarshalJSON(data []byte) error {
	var shadow contentRaw
	if err := models.UnmarshalDynamic(data, &shadow); err != nil {
		return err
	}
	*c = Content{
		Role:              shadow.Role,
		DynamicProperties: shadow.DynamicProperties,
	}
	if len(shadow.Parts) == 0 || string(shadow.Parts) == "null" {
		return nil
	}
	parts, err := UnmarshalParts(shadow.Parts)
	if err != nil {
		return fmt.Errorf("gemini: content parts: %w", err)
	}
	c.Parts = parts
	return nil
}

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c Content) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// contentRaw mirrors Content but keeps Parts as a RawMessage so
// UnmarshalDynamic ignores its polymorphic shape; the parent UnmarshalJSON
// dispatches the parts afterwards.
type contentRaw struct {
	Role string `json:"role,omitempty"`

	Parts json.RawMessage `json:"parts,omitempty"`

	models.DynamicProperties
}

// GenerationConfig controls sampling, output shape, and reasoning behaviour
// for a single generateContent call. Unknown fields round-trip via the
// embedded DynamicProperties.
type GenerationConfig struct {
	// Temperature is the sampling temperature; nil means "use the server
	// default".
	Temperature *float64 `json:"temperature,omitempty"`

	// TopP is the nucleus-sampling cutoff.
	TopP *float64 `json:"topP,omitempty"`

	// TopK caps the candidate token set considered at each sampling
	// step.
	TopK *int `json:"topK,omitempty"`

	// CandidateCount is the number of completions to return.
	CandidateCount *int `json:"candidateCount,omitempty"`

	// MaxOutputTokens caps generated tokens.
	MaxOutputTokens *int `json:"maxOutputTokens,omitempty"`

	// StopSequences lists substrings the model must stop at.
	StopSequences []string `json:"stopSequences,omitempty"`

	// ResponseMimeType constrains the response payload's media type
	// (e.g., "application/json").
	ResponseMimeType string `json:"responseMimeType,omitempty"`

	// ResponseSchema is the JSON-Schema document the response must match
	// when ResponseMimeType is "application/json". Kept raw so callers
	// build schemas without going through a typed schema model.
	ResponseSchema json.RawMessage `json:"responseSchema,omitempty"`

	// ResponseModalities lists the modalities to enable in the response
	// (e.g., "TEXT", "IMAGE", "AUDIO").
	ResponseModalities []string `json:"responseModalities,omitempty"`

	// Seed pins sampling for best-effort determinism.
	Seed *int `json:"seed,omitempty"`

	// ThinkingConfig configures the thinking/reasoning budget.
	ThinkingConfig *ThinkingConfig `json:"thinkingConfig,omitempty"`

	// MediaResolution selects the input-media resolution tier ("LOW",
	// "MEDIUM", "HIGH").
	MediaResolution string `json:"mediaResolution,omitempty"`

	// PresencePenalty biases the model against repeating content.
	PresencePenalty *float64 `json:"presencePenalty,omitempty"`

	// FrequencyPenalty biases the model against frequent tokens.
	FrequencyPenalty *float64 `json:"frequencyPenalty,omitempty"`

	// Logprobs requests N log-probabilities per generated token.
	Logprobs *int `json:"logprobs,omitempty"`

	// ResponseLogprobs enables log-probability output on the response.
	ResponseLogprobs *bool `json:"responseLogprobs,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into g, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (g *GenerationConfig) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, g)
}

// MarshalJSON encodes g and merges DynamicProperties.Extra back into the
// resulting object.
func (g GenerationConfig) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(g) }

// ThinkingConfig configures the thinking/reasoning budget for a Gemini
// request. Unknown fields round-trip via the embedded DynamicProperties.
type ThinkingConfig struct {
	// IncludeThoughts surfaces the model's thinking trace in the
	// response when true.
	IncludeThoughts *bool `json:"includeThoughts,omitempty"`

	// ThinkingBudget caps tokens spent on thinking.
	ThinkingBudget *int `json:"thinkingBudget,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into t, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (t *ThinkingConfig) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON encodes t and merges DynamicProperties.Extra back into the
// resulting object.
func (t ThinkingConfig) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// Tool advertises a capability available to the model — function
// declarations or one of the built-in tools (Google Search, code execution,
// URL context). The top-level keys are mostly mutually exclusive;
// DynamicProperties catches any new built-in tool Google introduces before
// this package models it.
type Tool struct {
	// FunctionDeclarations is the catalogue of caller-defined functions
	// the model may call.
	FunctionDeclarations []FunctionDeclaration `json:"functionDeclarations,omitempty"`

	// GoogleSearch enables live Google Search grounding (empty marker
	// object).
	GoogleSearch *GoogleSearchTool `json:"googleSearch,omitempty"`

	// GoogleSearchRetrieval enables legacy retrieval-augmented Google
	// Search grounding.
	GoogleSearchRetrieval *GoogleSearchRetrievalTool `json:"googleSearchRetrieval,omitempty"`

	// CodeExecution enables the hosted code-execution tool.
	CodeExecution *CodeExecutionTool `json:"codeExecution,omitempty"`

	// URLContext enables Gemini's URL-context grounding tool.
	URLContext *URLContextTool `json:"urlContext,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into t, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (t *Tool) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON encodes t and merges DynamicProperties.Extra back into the
// resulting object.
func (t Tool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// GoogleSearchTool is the empty marker that enables the live Google Search
// grounding tool. Unknown fields round-trip via the embedded
// DynamicProperties so future config knobs land here.
type GoogleSearchTool struct {
	models.DynamicProperties
}

// UnmarshalJSON decodes data into t, routing every field into
// DynamicProperties.Extra (the type defines no concrete fields today).
func (t *GoogleSearchTool) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, t)
}

// MarshalJSON encodes t and merges DynamicProperties.Extra back into the
// resulting object.
func (t GoogleSearchTool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// GoogleSearchRetrievalTool enables retrieval-augmented Google Search
// grounding on legacy models. Unknown fields round-trip via the embedded
// DynamicProperties.
type GoogleSearchRetrievalTool struct {
	// DynamicRetrievalConfig configures the retrieval thresholding.
	// Kept raw because Google's schema for this field has changed
	// across releases.
	DynamicRetrievalConfig json.RawMessage `json:"dynamicRetrievalConfig,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into t, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (t *GoogleSearchRetrievalTool) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, t)
}

// MarshalJSON encodes t and merges DynamicProperties.Extra back into the
// resulting object.
func (t GoogleSearchRetrievalTool) MarshalJSON() ([]byte, error) {
	return models.MarshalDynamic(t)
}

// CodeExecutionTool is the empty marker that enables the hosted code
// execution tool. Unknown fields round-trip via the embedded
// DynamicProperties so future config knobs land here.
type CodeExecutionTool struct {
	models.DynamicProperties
}

// UnmarshalJSON decodes data into t, routing every field into
// DynamicProperties.Extra.
func (t *CodeExecutionTool) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, t)
}

// MarshalJSON encodes t and merges DynamicProperties.Extra back into the
// resulting object.
func (t CodeExecutionTool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// URLContextTool enables Gemini's URL-context grounding tool. Unknown
// fields round-trip via the embedded DynamicProperties so future config
// knobs land here.
type URLContextTool struct {
	models.DynamicProperties
}

// UnmarshalJSON decodes data into t, routing every field into
// DynamicProperties.Extra.
func (t *URLContextTool) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, t)
}

// MarshalJSON encodes t and merges DynamicProperties.Extra back into the
// resulting object.
func (t URLContextTool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// FunctionDeclaration describes a single function the model may call.
// Unknown fields round-trip via the embedded DynamicProperties.
type FunctionDeclaration struct {
	// Name is the function identifier the model invokes when calling
	// this tool.
	Name string `json:"name"`

	// Description is the natural-language description the model uses to
	// decide when to call the function.
	Description string `json:"description,omitempty"`

	// Parameters describes the function's arguments as a Gemini Schema
	// (the OpenAPI 3.0 subset). Kept raw so callers build/inspect schemas
	// without going through a typed schema model.
	Parameters json.RawMessage `json:"parameters,omitempty"`

	// ParametersJsonSchema describes the function's arguments as a full
	// JSON Schema (draft 2020-12) — the newer alternative to Parameters
	// that recent SDKs (incl. gemini-cli) emit. Mutually exclusive with
	// Parameters upstream; kept raw for the same reason.
	ParametersJsonSchema json.RawMessage `json:"parametersJsonSchema,omitempty"`

	// Response is the function's return shape as a Gemini Schema. Kept raw
	// for the same reason as Parameters.
	Response json.RawMessage `json:"response,omitempty"`

	// ResponseJsonSchema is the function's return shape as a full JSON
	// Schema — the JSON-Schema counterpart to Response.
	ResponseJsonSchema json.RawMessage `json:"responseJsonSchema,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into f, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (f *FunctionDeclaration) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, f)
}

// MarshalJSON encodes f and merges DynamicProperties.Extra back into the
// resulting object.
func (f FunctionDeclaration) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(f) }

// ToolConfig overrides default tool-calling behaviour on a
// GenerateContentRequest. Unknown fields round-trip via the embedded
// DynamicProperties.
type ToolConfig struct {
	// FunctionCallingConfig pins the tool-calling mode.
	FunctionCallingConfig *FunctionCallingConfig `json:"functionCallingConfig,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (c *ToolConfig) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c ToolConfig) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// FunctionCallingConfig pins the tool-calling mode for the request. Unknown
// fields round-trip via the embedded DynamicProperties.
type FunctionCallingConfig struct {
	// Mode selects the tool-calling mode ("AUTO", "ANY", "NONE").
	Mode string `json:"mode,omitempty"`

	// AllowedFunctionNames restricts the tools the model may call when
	// Mode == "ANY".
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (c *FunctionCallingConfig) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, c)
}

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c FunctionCallingConfig) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// SafetySetting overrides the default safety threshold for a single
// category. Unknown fields round-trip via the embedded DynamicProperties.
type SafetySetting struct {
	// Category is Gemini's safety category (e.g., "HARM_CATEGORY_HARASSMENT").
	Category string `json:"category"`

	// Threshold is the block threshold (e.g., "BLOCK_MEDIUM_AND_ABOVE").
	Threshold string `json:"threshold"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into s, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (s *SafetySetting) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, s) }

// MarshalJSON encodes s and merges DynamicProperties.Extra back into the
// resulting object.
func (s SafetySetting) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(s) }
