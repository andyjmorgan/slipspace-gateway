package content

import (
	"encoding/json"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// GenerateContentResponse is the non-streaming response payload returned by
// generateContent. The streaming variant (streamGenerateContent) emits a
// sequence of values with the same shape, one per chunk, so we reuse this
// type for both — there is no separate event hierarchy in v1.0.
type GenerateContentResponse struct {
	Candidates []Candidate `json:"candidates,omitempty"`

	PromptFeedback *PromptFeedback `json:"promptFeedback,omitempty"`

	UsageMetadata *UsageMetadata `json:"usageMetadata,omitempty"`

	ModelVersion string `json:"modelVersion,omitempty"`

	ResponseID string `json:"responseId,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (r *GenerateContentResponse) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, r)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (r GenerateContentResponse) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// Candidate is one of N candidate completions emitted by the model.
type Candidate struct {
	Content *Content `json:"content,omitempty"`

	FinishReason *string `json:"finishReason,omitempty"`

	Index *int `json:"index,omitempty"`

	SafetyRatings []SafetyRating `json:"safetyRatings,omitempty"`

	CitationMetadata *CitationMetadata `json:"citationMetadata,omitempty"`

	TokenCount *int `json:"tokenCount,omitempty"`

	GroundingMetadata json.RawMessage `json:"groundingMetadata,omitempty"`

	URLContextMetadata json.RawMessage `json:"urlContextMetadata,omitempty"`

	LogprobsResult json.RawMessage `json:"logprobsResult,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *Candidate) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c Candidate) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// SafetyRating describes the model's per-category safety assessment for a
// candidate or prompt.
type SafetyRating struct {
	Category string `json:"category"`

	Probability string `json:"probability,omitempty"`

	ProbabilityScore *float64 `json:"probabilityScore,omitempty"`

	Severity string `json:"severity,omitempty"`

	SeverityScore *float64 `json:"severityScore,omitempty"`

	Blocked *bool `json:"blocked,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (r *SafetyRating) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, r) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (r SafetyRating) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(r) }

// CitationMetadata bundles the citation sources that contributed to a
// candidate's content.
type CitationMetadata struct {
	CitationSources []CitationSource `json:"citationSources,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (m *CitationMetadata) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, m)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (m CitationMetadata) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// CitationSource locates a specific contribution within the candidate text.
type CitationSource struct {
	StartIndex *int `json:"startIndex,omitempty"`

	EndIndex *int `json:"endIndex,omitempty"`

	URI string `json:"uri,omitempty"`

	License string `json:"license,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (s *CitationSource) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, s) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (s CitationSource) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(s) }

// PromptFeedback reports why a prompt was blocked (if it was) and the safety
// ratings the model assigned to the prompt itself.
type PromptFeedback struct {
	BlockReason string `json:"blockReason,omitempty"`

	SafetyRatings []SafetyRating `json:"safetyRatings,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (f *PromptFeedback) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, f) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (f PromptFeedback) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(f) }

// UsageMetadata accounts for tokens charged against the request.
type UsageMetadata struct {
	PromptTokenCount *int `json:"promptTokenCount,omitempty"`

	CandidatesTokenCount *int `json:"candidatesTokenCount,omitempty"`

	TotalTokenCount *int `json:"totalTokenCount,omitempty"`

	CachedContentTokenCount *int `json:"cachedContentTokenCount,omitempty"`

	ToolUsePromptTokenCount *int `json:"toolUsePromptTokenCount,omitempty"`

	ThoughtsTokenCount *int `json:"thoughtsTokenCount,omitempty"`

	PromptTokensDetails []ModalityTokenCount `json:"promptTokensDetails,omitempty"`

	CacheTokensDetails []ModalityTokenCount `json:"cacheTokensDetails,omitempty"`

	CandidatesTokensDetails []ModalityTokenCount `json:"candidatesTokensDetails,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (u *UsageMetadata) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, u) }

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (u UsageMetadata) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(u) }

// ModalityTokenCount accounts tokens for a single modality (text, image,
// audio, video).
type ModalityTokenCount struct {
	Modality string `json:"modality,omitempty"`

	TokenCount *int `json:"tokenCount,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (m *ModalityTokenCount) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, m)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (m ModalityTokenCount) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }
