package content

import (
	"encoding/json"

	"github.com/andyjmorgan/slipspace-gateway/models"
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

	// Error carries Google's top-level error envelope
	// ({code, message, status, details}) when generateContent fails
	// without an HTTP-transport error. Kept raw because the details array
	// is polymorphic.
	Error json.RawMessage `json:"error,omitempty"`

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

	// FinishMessage is the human-readable elaboration of FinishReason
	// (e.g. "Model generated function call(s)."). Nil while in progress.
	FinishMessage *string `json:"finishMessage,omitempty"`

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
	// when grounding tools were enabled.
	GroundingMetadata *GroundingMetadata `json:"groundingMetadata,omitempty"`

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

// GroundingMetadata carries the Google Search / web-grounding evidence the
// model used when a grounding tool was enabled. Unknown fields round-trip
// via the embedded DynamicProperties.
type GroundingMetadata struct {
	// SearchEntryPoint is the rendered Google Search suggestion widget the
	// caller is required to display alongside grounded content.
	SearchEntryPoint *SearchEntryPoint `json:"searchEntryPoint,omitempty"`

	// GroundingChunks are the retrieved sources the grounded answer drew on.
	GroundingChunks []GroundingChunk `json:"groundingChunks,omitempty"`

	// GroundingSupports map spans of the answer text to the chunks that
	// support them.
	GroundingSupports []GroundingSupport `json:"groundingSupports,omitempty"`

	// WebSearchQueries are the queries the model issued to ground the answer.
	WebSearchQueries []string `json:"webSearchQueries,omitempty"`

	// RetrievalMetadata carries dynamic-retrieval scoring when configured.
	// Kept raw because the shape is sparsely documented and rarely present.
	RetrievalMetadata json.RawMessage `json:"retrievalMetadata,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into m, routing any field not declared on the
// struct into DynamicProperties.Extra.
func (m *GroundingMetadata) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, m)
}

// MarshalJSON encodes m and merges DynamicProperties.Extra back into the
// resulting object.
func (m GroundingMetadata) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(m) }

// SearchEntryPoint is the rendered Google Search suggestion widget returned
// with grounded content. Unknown fields round-trip via DynamicProperties.
type SearchEntryPoint struct {
	// RenderedContent is the HTML/CSS snippet the caller must render.
	RenderedContent string `json:"renderedContent,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into s, routing unknown fields into Extra.
func (s *SearchEntryPoint) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, s)
}

// MarshalJSON encodes s and merges DynamicProperties.Extra back in.
func (s SearchEntryPoint) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(s) }

// GroundingChunk is one retrieved source backing a grounded answer. Unknown
// fields (and future chunk kinds beyond web) round-trip via DynamicProperties.
type GroundingChunk struct {
	// Web is the web-source chunk; nil for non-web chunk kinds.
	Web *GroundingChunkWeb `json:"web,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into c, routing unknown fields into Extra.
func (c *GroundingChunk) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, c) }

// MarshalJSON encodes c and merges DynamicProperties.Extra back in.
func (c GroundingChunk) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// GroundingChunkWeb locates a web source contributing to a grounded answer.
// Unknown fields round-trip via DynamicProperties.
type GroundingChunkWeb struct {
	// URI is the (often Vertex-redirected) source URL.
	URI string `json:"uri,omitempty"`

	// Title is the source's display title.
	Title string `json:"title,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into w, routing unknown fields into Extra.
func (w *GroundingChunkWeb) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, w) }

// MarshalJSON encodes w and merges DynamicProperties.Extra back in.
func (w GroundingChunkWeb) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(w) }

// GroundingSupport ties a span of the answer text to the chunks supporting
// it. Unknown fields round-trip via DynamicProperties.
type GroundingSupport struct {
	// Segment is the span of answer text this support covers.
	Segment *Segment `json:"segment,omitempty"`

	// GroundingChunkIndices indexes into GroundingMetadata.GroundingChunks.
	GroundingChunkIndices []int `json:"groundingChunkIndices,omitempty"`

	// ConfidenceScores parallels GroundingChunkIndices with per-chunk
	// confidence in [0,1].
	ConfidenceScores []float64 `json:"confidenceScores,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into s, routing unknown fields into Extra.
func (s *GroundingSupport) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, s) }

// MarshalJSON encodes s and merges DynamicProperties.Extra back in.
func (s GroundingSupport) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(s) }

// Segment locates a span within the candidate answer text. Unknown fields
// round-trip via DynamicProperties.
type Segment struct {
	// PartIndex is the index of the Part the segment falls in.
	PartIndex *int `json:"partIndex,omitempty"`

	// StartIndex is the segment's inclusive start byte offset.
	StartIndex *int `json:"startIndex,omitempty"`

	// EndIndex is the segment's exclusive end byte offset.
	EndIndex *int `json:"endIndex,omitempty"`

	// Text is the segment's literal text.
	Text string `json:"text,omitempty"`

	models.DynamicProperties
}

// UnmarshalJSON decodes data into s, routing unknown fields into Extra.
func (s *Segment) UnmarshalJSON(data []byte) error { return models.UnmarshalDynamic(data, s) }

// MarshalJSON encodes s and merges DynamicProperties.Extra back in.
func (s Segment) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(s) }

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

	// ToolUsePromptTokensDetails breaks ToolUsePromptTokenCount down by
	// modality.
	ToolUsePromptTokensDetails []ModalityTokenCount `json:"toolUsePromptTokensDetails,omitempty"`

	// ThoughtsTokenCount counts tokens billed for the model's hidden
	// thinking trace.
	ThoughtsTokenCount *int `json:"thoughtsTokenCount,omitempty"`

	// ServiceTier is the processing tier Google served the request on
	// (e.g. "standard"). Mirrors the tier echoed by other providers.
	ServiceTier *string `json:"serviceTier,omitempty"`

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
