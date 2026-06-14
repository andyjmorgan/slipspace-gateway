package admin

// VerdictResponse is the SlipSpace Arbiter security verdict plus findings for
// one request, served at GET /api/v1/verdict/{correlation_id} and rendered in
// the console's Security pane. Verdict is nil when the scan has not reached
// quiescence yet (no verdict row); Findings is empty for a clean request.
type VerdictResponse struct {
	// CorrelationID is the request this verdict describes.
	CorrelationID string `json:"correlation_id"`

	// Verdict is the reduced per-request outcome, or nil when none exists yet.
	Verdict *VerdictView `json:"verdict,omitempty"`

	// Findings is the per-hit detail behind the verdict (empty when clean).
	Findings []FindingView `json:"findings"`
}

// VerdictView is the reduced verdict shown as a badge in the console.
type VerdictView struct {
	// State is "flagged", "partial", or "clean" (ADR-017).
	State string `json:"state"`

	// MaxScore is the highest finding score (highest-risk-wins).
	MaxScore float32 `json:"max_score"`

	// TopCategory is the category of the highest-scoring finding.
	TopCategory string `json:"top_category,omitempty"`

	// FindingCount is the number of findings on this request.
	FindingCount int `json:"finding_count"`

	// Inconclusive lists the check types that timed out or failed — the set
	// that raises the request to PARTIAL. Never read as clean.
	Inconclusive []string `json:"inconclusive,omitempty"`
}

// FindingView is one detector hit shown in the console's findings list.
type FindingView struct {
	// UnitID identifies the content block the finding came from.
	UnitID string `json:"unit_id"`

	// CheckType is the check that produced it ("injection", "toxicity", "pii").
	CheckType string `json:"check_type"`

	// Category is the normalized taxonomy entry, e.g. "pii.email".
	Category string `json:"category"`

	// Score is the detector confidence in [0,1].
	Score float32 `json:"score"`

	// RawLabel is the detector's native label, kept as provenance.
	RawLabel string `json:"raw_label,omitempty"`

	// Detector identifies the detector that produced the finding.
	Detector string `json:"detector,omitempty"`

	// Localization records how the finding's span was derived.
	Localization string `json:"localization,omitempty"`
}
