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

	// Severity is the worst operator-assigned level across the request's
	// findings ("info"/"warning"/"error"); empty when there are none.
	Severity string `json:"severity,omitempty"`

	// FindingCount is the number of findings on this request.
	FindingCount int `json:"finding_count"`

	// Inconclusive lists the check types that timed out or failed — the set
	// that raises the request to PARTIAL. Never read as clean.
	Inconclusive []string `json:"inconclusive,omitempty"`
}

// FindingsListResponse is the operator Security view: a flat list of detector
// findings, newest first, served at GET /api/v1/findings. The same shape backs
// two surfaces — the top-level Security page (recent findings across all
// sessions) and the session view's Security tab (?session=<id>, all findings in
// one session) — so both render with one reusable table. It is finding-centric,
// not verdict-centric: each row is one hit, linking back to BOTH the message it
// came from (correlation_id) and the session it came from (session_id).
type FindingsListResponse struct {
	// Items is the page of findings, ordered by observed_at desc.
	Items []FindingRow `json:"items"`
}

// FindingRow is one detector finding joined to its request's facts: the finding
// itself (category, check_type, score, raw_label, the offending unit's kind/role)
// plus the deep-link targets (correlation_id → message inspector, session_id →
// session view) and the request context (observed_at, model, configuration) the
// operator triages by. Kept deliberately lean — the row shows the finding and
// where it came from, nothing more.
type FindingRow struct {
	// Category is the normalized taxonomy entry, e.g. "pii.email".
	Category string `json:"category"`

	// CheckType is the check that produced it ("injection", "toxicity", "pii").
	CheckType string `json:"check_type"`

	// Score is the detector confidence in [0,1].
	Score float32 `json:"score"`

	// RawLabel is the detector's native label, kept as provenance.
	RawLabel string `json:"raw_label,omitempty"`

	// UnitKind is the kind of the offending content block (e.g. "text",
	// "tool_use"), projected from the check task's unit locator; empty when the
	// locator carried none.
	UnitKind string `json:"unit_kind,omitempty"`

	// UnitRole is the role of the offending content block (e.g. "user",
	// "assistant"), projected from the same locator; empty when none.
	UnitRole string `json:"unit_role,omitempty"`

	// CorrelationID is the request this finding came from — the row's deep-link
	// into the message inspector.
	CorrelationID string `json:"correlation_id"`

	// SessionID is the session this finding came from — the row's deep-link into
	// the session view; empty when the request carried no session.
	SessionID string `json:"session_id,omitempty"`

	// ObservedAt is the source request's observation time (RFC3339), the list's
	// sort key; empty when the finding's request_events row has aged out.
	ObservedAt string `json:"observed_at,omitempty"`

	// Model / Configuration are the post-rule gateway facts on the source
	// request; empty when the request_events row has aged out of retention.
	Model         string `json:"model,omitempty"`
	Configuration string `json:"configuration,omitempty"`

	// OffendingText is the flagged text the finding fired on — the exact span
	// substring for localized hits, the whole offending unit otherwise. Shown
	// inline in the Security report so the operator sees WHAT was flagged.
	OffendingText string `json:"offending_text,omitempty"`

	// Severity is the operator-assigned level for this finding's category
	// ("info"/"warning"/"error"); empty on findings scanned before severity
	// classification existed.
	Severity string `json:"severity,omitempty"`
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

	// OffendingText is the flagged text the finding fired on — the exact span
	// substring for localized hits, the whole offending unit otherwise.
	OffendingText string `json:"offending_text,omitempty"`

	// Severity is the operator-assigned level for this finding's category
	// ("info"/"warning"/"error"); empty when unclassified.
	Severity string `json:"severity,omitempty"`
}
