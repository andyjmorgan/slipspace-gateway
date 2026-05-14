package content

import (
	"encoding/json"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// GenerateContentResponse is the response body returned by Gemini's POST
// /v1beta/models/{model}:generateContent endpoint. The streaming variant
// (streamGenerateContent) emits a sequence of values with this same shape,
// one per chunk, so this type is reused for both — Gemini has no separate
// streaming event hierarchy in v1.0. Unknown fields round-trip via the
// embedded DynamicProperties.
type GenerateContentResponse struct {
	// Candidates is the list of completion candidates the model
	// produced (length == GenerationConfig.CandidateCount).
	Candidates []Candidate `json:"candidates,omitempty"`

	// PromptFeedback reports safety ratings (and a block reason if the
	// prompt itself was rejected) for the request input.
	PromptFeedback *PromptFeedback `json:"promptFeedback,omitempty"`

	// UsageMetadata is the token accounting for the response.
	UsageMetadata *UsageMetadata `json:"usageMetadata,omitempty"`

	// ModelVersion is the resolved model version Google billed against.
	ModelVersion string `json:"modelVersion,omitempty"`

	// ResponseID is Google's response identifier.
	ResponseID string `json:"responseId,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into r, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (r *GenerateContentResponse) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, r)
}

// MarshalJSON encodes r and merges DynamicProperties.Extra back into the
// resulting object.
func (r GenerateContentResponse) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// Candidate is one of N candidate completions emitted by the model.
// Unknown fields round-trip via the embedded DynamicProperties.
type Candidate struct {
	// Content is the candidate's reply content. Nil on streaming
	// chunks that carry only metadata (e.g., FinishReason on the
	// terminal chunk).
	Content *Content `json:"content,omitempty"`

	// FinishReason is Gemini's machine-readable termination cause
	// ("STOP", "MAX_TOKENS", "SAFETY", "RECITATION", "OTHER", ...).
	// Nil while still in progress.
	FinishReason *string `json:"finishReason,omitempty"`

	// Index is the candidate's zero-based position within Candidates.
	Index *int `json:"index,omitempty"`

	// SafetyRatings is the per-category safety assessment for the
	// candidate's content.
	SafetyRatings []SafetyRating `json:"safetyRatings,omitempty"`

	// CitationMetadata bundles the citation sources that contributed to
	// the candidate.
	CitationMetadata *CitationMetadata `json:"citationMetadata,omitempty"`

	// TokenCount counts tokens in this candidate's reply.
	TokenCount *int `json:"tokenCount,omitempty"`

	// GroundingMetadata carries Google Search / web-grounding metadata
	// when grounding tools were enabled. Kept raw because the shape
	// evolves frequently.
	GroundingMetadata json.RawMessage `json:"groundingMetadata,omitempty"`

	// URLContextMetadata carries URL-context-tool metadata. Kept raw
	// for the same reason as GroundingMetadata.
	URLContextMetadata json.RawMessage `json:"urlContextMetadata,omitempty"`

	// LogprobsResult carries the log-probability output when
	// GenerationConfig.ResponseLogprobs was true. Kept raw because
	// Google's logprob schema has changed across releases.
	LogprobsResult json.RawMessage `json:"logprobsResult,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (c *Candidate) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON encodes c and merges DynamicProperties.Extra back into the
// resulting object.
func (c Candidate) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// SafetyRating describes the model's per-category safety assessment for a
// candidate or prompt. Unknown fields round-trip via the embedded
// DynamicProperties.
type SafetyRating struct {
	// Category is Gemini's safety category (e.g.,
	// "HARM_CATEGORY_HARASSMENT").
	Category string `json:"category"`

	// Probability is the bucketed harm probability ("NEGLIGIBLE",
	// "LOW", "MEDIUM", "HIGH").
	Probability string `json:"probability,omitempty"`

	// ProbabilityScore is the numeric harm-probability score.
	ProbabilityScore *float64 `json:"probabilityScore,omitempty"`

	// Severity is the bucketed harm severity.
	Severity string `json:"severity,omitempty"`

	// SeverityScore is the numeric harm-severity score.
	SeverityScore *float64 `json:"severityScore,omitempty"`

	// Blocked reports whether the rating crossed the configured block
	// threshold for this category.
	Blocked *bool `json:"blocked,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into r, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (r *SafetyRating) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, r) }

// MarshalJSON encodes r and merges DynamicProperties.Extra back into the
// resulting object.
func (r SafetyRating) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// CitationMetadata bundles the citation sources that contributed to a
// candidate's content. Unknown fields round-trip via the embedded
// DynamicProperties.
type CitationMetadata struct {
	// CitationSources lists the individual cited sources.
	CitationSources []CitationSource `json:"citationSources,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into m, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (m *CitationMetadata) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, m)
}

// MarshalJSON encodes m and merges DynamicProperties.Extra back into the
// resulting object.
func (m CitationMetadata) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// CitationSource locates a specific contribution within the candidate text.
// Unknown fields round-trip via the embedded DynamicProperties.
type CitationSource struct {
	// StartIndex is the citation's inclusive start position in the
	// candidate text.
	StartIndex *int `json:"startIndex,omitempty"`

	// EndIndex is the citation's exclusive end position in the
	// candidate text.
	EndIndex *int `json:"endIndex,omitempty"`

	// URI is the cited source URI.
	URI string `json:"uri,omitempty"`

	// License is the cited source's license identifier when known.
	License string `json:"license,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into s, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (s *CitationSource) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, s) }

// MarshalJSON encodes s and merges DynamicProperties.Extra back into the
// resulting object.
func (s CitationSource) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(s) }

// PromptFeedback reports why a prompt was blocked (if it was) and the
// safety ratings the model assigned to the prompt itself. Unknown fields
// round-trip via the embedded DynamicProperties.
type PromptFeedback struct {
	// BlockReason is Gemini's machine-readable block cause (e.g.,
	// "SAFETY"). Empty when the prompt was not blocked.
	BlockReason string `json:"blockReason,omitempty"`

	// SafetyRatings is the per-category safety assessment of the
	// prompt.
	SafetyRatings []SafetyRating `json:"safetyRatings,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into f, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (f *PromptFeedback) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, f) }

// MarshalJSON encodes f and merges DynamicProperties.Extra back into the
// resulting object.
func (f PromptFeedback) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(f) }

// UsageMetadata accounts for tokens charged against a Gemini request.
// Unknown fields round-trip via the embedded DynamicProperties.
type UsageMetadata struct {
	// PromptTokenCount counts tokens billed for the request prompt.
	PromptTokenCount *int `json:"promptTokenCount,omitempty"`

	// CandidatesTokenCount counts tokens billed across all returned
	// candidates.
	CandidatesTokenCount *int `json:"candidatesTokenCount,omitempty"`

	// TotalTokenCount is the sum of prompt + candidates + thoughts
	// tokens (mirrored by the provider).
	TotalTokenCount *int `json:"totalTokenCount,omitempty"`

	// CachedContentTokenCount counts tokens served from
	// GenerateContentRequest.CachedContent.
	CachedContentTokenCount *int `json:"cachedContentTokenCount,omitempty"`

	// ToolUsePromptTokenCount counts tokens billed for server-side
	// tool calls (e.g., grounded search).
	ToolUsePromptTokenCount *int `json:"toolUsePromptTokenCount,omitempty"`

	// ThoughtsTokenCount counts tokens billed for the model's hidden
	// thinking trace.
	ThoughtsTokenCount *int `json:"thoughtsTokenCount,omitempty"`

	// PromptTokensDetails breaks PromptTokenCount down by modality.
	PromptTokensDetails []ModalityTokenCount `json:"promptTokensDetails,omitempty"`

	// CacheTokensDetails breaks CachedContentTokenCount down by
	// modality.
	CacheTokensDetails []ModalityTokenCount `json:"cacheTokensDetails,omitempty"`

	// CandidatesTokensDetails breaks CandidatesTokenCount down by
	// modality.
	CandidatesTokensDetails []ModalityTokenCount `json:"candidatesTokensDetails,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into u, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (u *UsageMetadata) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, u) }

// MarshalJSON encodes u and merges DynamicProperties.Extra back into the
// resulting object.
func (u UsageMetadata) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(u) }

// ModalityTokenCount accounts tokens for a single modality (text, image,
// audio, video). Unknown fields round-trip via the embedded
// DynamicProperties.
type ModalityTokenCount struct {
	// Modality is the modality identifier ("TEXT", "IMAGE", "AUDIO",
	// "VIDEO").
	Modality string `json:"modality,omitempty"`

	// TokenCount is the count of tokens charged for this modality.
	TokenCount *int `json:"tokenCount,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into m, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (m *ModalityTokenCount) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, m)
}

// MarshalJSON encodes m and merges DynamicProperties.Extra back into the
// resulting object.
func (m ModalityTokenCount) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }
