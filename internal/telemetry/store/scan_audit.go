package store

import (
	"context"
	"fmt"
	"time"
)

// The scan-audit log reads the scan_audit table directly, the same
// invariant-#4-safe exception the security dashboard rollups take (see
// dashboard_security.go): scan_audit is a compact derived table on the OTLP
// span feed — one row per operational scan failure, written by the Arbiter
// worker — NOT the S3/spool Record channel invariant #4 guards the dashboard
// against. It carries the failure dimension (reason, check_type, detector) no
// OTel meter exposes, and it is window-only (no provider/configuration/model
// columns), so the dashboard's equality filters do not narrow it.

// ScanAuditReason enumerates the operational scan-failure reasons the Arbiter
// worker records. These are the non-clean operational terminations — never a
// clean completion or a finding.
const (
	// ScanReasonTimeout is a detector call that hit the per-call context deadline.
	ScanReasonTimeout = "timeout"
	// ScanReasonUnreachable is a detector transport / dial error.
	ScanReasonUnreachable = "unreachable"
	// ScanReasonDetectorError is a detector that returned STATUS_ERROR.
	ScanReasonDetectorError = "detector_error"
	// ScanReasonNoDetector is a check type with no configured detector.
	ScanReasonNoDetector = "no_detector"
	// ScanReasonUnitMissing is a scanned unit that could not be found on the span.
	ScanReasonUnitMissing = "unit_missing"
)

// ScanAuditEntry is one row of the scan_audit log (migration v16): a single
// security scan that did not complete cleanly, tagged with a reason. OccurredAt
// is output-only — on insert the DB defaults it to now(); reads populate it.
type ScanAuditEntry struct {
	// CorrelationID is the request the failed scan belonged to.
	CorrelationID string
	// UnitID identifies the content block within the span the scan targeted.
	UnitID string
	// CheckType is the logical check that failed (injection / pii / toxicity).
	CheckType string
	// DetectorID is the detector that failed, when known (empty for no_detector
	// / unit_missing, where no detector call was made).
	DetectorID string
	// Reason is the operational-failure reason (one of the ScanReason* constants).
	Reason string
	// Detail is a short human-readable failure description (e.g. the error text).
	Detail string
	// Attempts is how many times the scan had been attempted when it failed.
	Attempts int
	// OccurredAt is when the failure was recorded (DB default on insert).
	OccurredAt time.Time
}

// InsertScanAudit appends one operational scan-failure row. occurred_at is left
// to the DB default (now()) — the worker never backdates an audit event. The
// table is append-only; there is no update or delete path. correlation_id and
// reason must be non-empty (mirrors InsertFinding's validation posture: a row
// with no request or no reason is meaningless to the dashboard).
func (s *Store) InsertScanAudit(ctx context.Context, e ScanAuditEntry) error {
	if e.CorrelationID == "" || e.Reason == "" {
		return fmt.Errorf("store: insert scan audit: correlation_id and reason are required")
	}
	_, err := s.db.Exec(ctx,
		`INSERT INTO scan_audit (correlation_id, unit_id, check_type, detector_id, reason, attempts, detail)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		e.CorrelationID, e.UnitID, e.CheckType, e.DetectorID, e.Reason, e.Attempts, e.Detail)
	if err != nil {
		return fmt.Errorf("store: insert scan audit: %w", err)
	}
	return nil
}

// ListScanAudit returns the operational scan failures in [from, to), newest
// first, capped at limit — the dashboard's scan-failure panel. The occurred_at
// DESC index (migration v16) backs the window scan.
func (s *Store) ListScanAudit(ctx context.Context, from, to time.Time, limit int) ([]ScanAuditEntry, error) {
	rows, err := s.db.Query(ctx,
		`SELECT correlation_id, unit_id, check_type, detector_id, reason, attempts, detail, occurred_at
		 FROM scan_audit
		 WHERE occurred_at >= $1 AND occurred_at < $2
		 ORDER BY occurred_at DESC
		 LIMIT $3`, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list scan audit: %w", err)
	}
	defer rows.Close()

	var out []ScanAuditEntry
	for rows.Next() {
		var e ScanAuditEntry
		if err := rows.Scan(&e.CorrelationID, &e.UnitID, &e.CheckType, &e.DetectorID,
			&e.Reason, &e.Attempts, &e.Detail, &e.OccurredAt); err != nil {
			return nil, fmt.Errorf("store: scan scan-audit row: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
