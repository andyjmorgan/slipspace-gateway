package content

import (
	"encoding/json"
	"fmt"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// GenerateContentRequest is the JSON body posted to
// /v1beta/models/{model}:generateContent (and the streaming variant).
type GenerateContentRequest struct {
	Contents []Content `json:"contents"`

	Tools []Tool `json:"tools,omitempty"`

	ToolConfig *ToolConfig `json:"toolConfig,omitempty"`

	SafetySettings []SafetySetting `json:"safetySettings,omitempty"`

	SystemInstruction *Content `json:"systemInstruction,omitempty"`

	GenerationConfig *GenerationConfig `json:"generationConfig,omitempty"`

	CachedContent *string `json:"cachedContent,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties. The nested
// Content values dispatch their polymorphic Parts via their own UnmarshalJSON.
func (r *GenerateContentRequest) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, r)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (r GenerateContentRequest) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// Content is a single turn in the conversation: a role plus a slice of
// polymorphic Parts.
type Content struct {
	Role string `json:"role,omitempty"`

	Parts []Part `json:"parts,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON dispatches each element of "parts" through UnmarshalPart so
// unknown part shapes round-trip via UnknownPart, and routes every other
// field through DynamicProperties.
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

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
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
// for a single generateContent call.
type GenerationConfig struct {
	Temperature *float64 `json:"temperature,omitempty"`

	TopP *float64 `json:"topP,omitempty"`

	TopK *int `json:"topK,omitempty"`

	CandidateCount *int `json:"candidateCount,omitempty"`

	MaxOutputTokens *int `json:"maxOutputTokens,omitempty"`

	StopSequences []string `json:"stopSequences,omitempty"`

	ResponseMimeType string `json:"responseMimeType,omitempty"`

	ResponseSchema json.RawMessage `json:"responseSchema,omitempty"`

	ResponseModalities []string `json:"responseModalities,omitempty"`

	Seed *int `json:"seed,omitempty"`

	ThinkingConfig *ThinkingConfig `json:"thinkingConfig,omitempty"`

	MediaResolution string `json:"mediaResolution,omitempty"`

	PresencePenalty *float64 `json:"presencePenalty,omitempty"`

	FrequencyPenalty *float64 `json:"frequencyPenalty,omitempty"`

	Logprobs *int `json:"logprobs,omitempty"`

	ResponseLogprobs *bool `json:"responseLogprobs,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (g *GenerationConfig) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, g)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (g GenerationConfig) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(g) }

// ThinkingConfig configures the thinking/reasoning budget for a request.
type ThinkingConfig struct {
	IncludeThoughts *bool `json:"includeThoughts,omitempty"`

	ThinkingBudget *int `json:"thinkingBudget,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (t *ThinkingConfig) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (t ThinkingConfig) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// Tool advertises a capability — function declarations or one of the built-in
// tools (Google Search, code execution, URL context). The top-level keys are
// mostly mutually exclusive; DynamicProperties catches any new built-in tool
// Google introduces before we model it.
type Tool struct {
	FunctionDeclarations []FunctionDeclaration `json:"functionDeclarations,omitempty"`

	GoogleSearch *GoogleSearchTool `json:"googleSearch,omitempty"`

	GoogleSearchRetrieval *GoogleSearchRetrievalTool `json:"googleSearchRetrieval,omitempty"`

	CodeExecution *CodeExecutionTool `json:"codeExecution,omitempty"`

	URLContext *URLContextTool `json:"urlContext,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (t *Tool) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, t) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (t Tool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// GoogleSearchTool is the empty marker that enables the live Google Search
// grounding tool.
type GoogleSearchTool struct {
	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (t *GoogleSearchTool) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, t)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (t GoogleSearchTool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// GoogleSearchRetrievalTool enables retrieval-augmented Google Search
// grounding on legacy models.
type GoogleSearchRetrievalTool struct {
	DynamicRetrievalConfig json.RawMessage `json:"dynamicRetrievalConfig,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (t *GoogleSearchRetrievalTool) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, t)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (t GoogleSearchRetrievalTool) MarshalJSON() ([]byte, error) {
	return models.MarshalDynamic(t)
}

// CodeExecutionTool is the empty marker that enables the code execution tool.
type CodeExecutionTool struct {
	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (t *CodeExecutionTool) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, t)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (t CodeExecutionTool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// URLContextTool enables Gemini's URL-context grounding tool.
type URLContextTool struct {
	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (t *URLContextTool) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, t)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (t URLContextTool) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(t) }

// FunctionDeclaration describes a single function the model may call.
type FunctionDeclaration struct {
	Name string `json:"name"`

	Description string `json:"description,omitempty"`

	Parameters json.RawMessage `json:"parameters,omitempty"`

	Response json.RawMessage `json:"response,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (f *FunctionDeclaration) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, f)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (f FunctionDeclaration) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(f) }

// ToolConfig overrides default tool-calling behaviour.
type ToolConfig struct {
	FunctionCallingConfig *FunctionCallingConfig `json:"functionCallingConfig,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *ToolConfig) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c ToolConfig) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// FunctionCallingConfig pins the tool-calling mode for the request.
type FunctionCallingConfig struct {
	Mode string `json:"mode,omitempty"`

	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *FunctionCallingConfig) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, c)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c FunctionCallingConfig) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// SafetySetting overrides the default safety threshold for a single category.
type SafetySetting struct {
	Category string `json:"category"`

	Threshold string `json:"threshold"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (s *SafetySetting) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, s) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (s SafetySetting) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(s) }
