package arbiter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	detectv1 "github.com/andyjmorgan/sluice-gateway/gen/slipspace/detect/v1"
	"github.com/andyjmorgan/sluice-gateway/internal/arbiter/store"
)

// Terminal check-task statuses.
const (
	statusCompleted    = "completed"
	statusInconclusive = "inconclusive"
	statusFailed       = "failed"
)

// taskResult is the compact summary stored on check_tasks.result for the
// console/inspector — never the offending content (that is the encrypted
// evidence row).
type taskResult struct {
	Status   string  `json:"status"`
	Findings int     `json:"findings"`
	MaxScore float32 `json:"max_score,omitempty"`
	Detector string  `json:"detector,omitempty"`
	Error    string  `json:"error,omitempty"`
}

// process runs one claimed check task end to end: resolve the unit text from
// the span, call the detector under the per-call budget, then store findings +
// evidence and mark the task terminal — or schedule a retry / inconclusive on
// failure.
func (s *Scanner) process(ctx context.Context, task store.CheckTask) {
	det, ok := s.detectors[task.CheckType]
	if !ok {
		// No detector configured for this check type (config changed under us).
		s.audit(ctx, task, "", store.ScanReasonNoDetector, fmt.Sprintf("no detector for %q", task.CheckType))
		s.complete(ctx, task, statusFailed, resultErr(statusFailed, fmt.Errorf("no detector for %q", task.CheckType)))
		return
	}

	unit, ok := s.resolveUnit(ctx, task)
	if !ok {
		// Span or unit gone / unscannable — we could not scan, so this is not
		// clean: mark inconclusive so the verdict reflects partial coverage.
		s.audit(ctx, task, det.checkType, store.ScanReasonUnitMissing, fmt.Sprintf("unit %q not found", task.UnitID))
		s.complete(ctx, task, statusInconclusive, resultErr(statusInconclusive, fmt.Errorf("unit %q not found", task.UnitID)))
		return
	}

	req := &detectv1.DetectRequest{
		SchemaVersion: schemaVersion,
		CorrelationId: task.CorrelationID,
		Unit: &detectv1.Unit{
			Id:   unit.ID,
			Kind: unit.Kind,
			Role: unit.Role,
			Text: unit.Text,
		},
		Options: &detectv1.Options{Threshold: det.threshold},
	}

	callCtx, cancel := context.WithTimeout(ctx, s.detectTimeout)
	defer cancel()
	resp, err := s.client.Detect(callCtx, det.endpoint, req)
	if err != nil {
		// A detector call that didn't return: timeout when it hit the per-call
		// deadline, otherwise a transport/dial failure.
		reason := store.ScanReasonUnreachable
		if errors.Is(err, context.DeadlineExceeded) {
			reason = store.ScanReasonTimeout
		}
		s.audit(ctx, task, det.checkType, reason, err.Error())
		s.handleFailure(ctx, task, err)
		return
	}
	s.handleResponse(ctx, task, det, resp, unit.Text)
}

// resolveUnit re-derives the unit text from the stored span by locator. The
// text is not duplicated on the check task — the span is the source of truth.
func (s *Scanner) resolveUnit(ctx context.Context, task store.CheckTask) (Unit, bool) {
	e, err := s.store.GetRequestEvent(ctx, task.CorrelationID)
	if err != nil {
		return Unit{}, false
	}
	for _, u := range BuildUnits(e) {
		if u.ID == task.UnitID {
			return u, u.Text != ""
		}
	}
	return Unit{}, false
}

// handleResponse maps a detector response to a terminal task state. OK with
// findings stores them; INCONCLUSIVE stays distinct from clean (ADR-017); any
// error status is treated as a failure (retry then inconclusive).
func (s *Scanner) handleResponse(ctx context.Context, task store.CheckTask, det detector, resp *detectv1.DetectResponse, text string) {
	switch resp.GetStatus() {
	case detectv1.Status_STATUS_OK:
		// Filter + classify before persisting so evidence, the finding table, the
		// task result, and the verdict all see the same surviving set.
		findings := s.survivingFindings(task, det, resp, text)
		s.storeFindings(ctx, task, findings, text)
		s.complete(ctx, task, statusCompleted, resultOf(statusCompleted, det, findings))
	case detectv1.Status_STATUS_INCONCLUSIVE:
		s.complete(ctx, task, statusInconclusive, resultOf(statusInconclusive, det, nil))
	default:
		// The detector answered with an error status (e.g. STATUS_ERROR) — the
		// call completed but the scan did not, an operational failure.
		err := fmt.Errorf("detector status %s: %s", resp.GetStatus(), resp.GetError())
		s.audit(ctx, task, det.checkType, store.ScanReasonDetectorError, err.Error())
		s.handleFailure(ctx, task, err)
	}
}

// survivingFindings maps a detector's OK response to the findings we persist:
// each hit minus the categories in scanner.finding_exclude, with its severity
// stamped from scanner.severity. Pure — no I/O.
func (s *Scanner) survivingFindings(task store.CheckTask, det detector, resp *detectv1.DetectResponse, text string) []store.Finding {
	raw := resp.GetFindings()
	if len(raw) == 0 {
		return nil
	}
	detID, detVer := det.checkType, ""
	if d := resp.GetDetector(); d != nil {
		if d.GetId() != "" {
			detID = d.GetId()
		}
		detVer = d.GetVersion()
	}
	out := make([]store.Finding, 0, len(raw))
	for _, f := range raw {
		category := f.GetCategory()
		// Exclude-only result filter: a category an operator suppressed is
		// dropped before any persistence (and so cannot flag the verdict).
		if matchesAnyGlob(s.findingExclude, category) {
			continue
		}
		fin := store.Finding{
			CorrelationID:   task.CorrelationID,
			UnitID:          task.UnitID,
			CheckType:       task.CheckType,
			Category:        category,
			Score:           f.GetScore(),
			RawLabel:        f.GetRawLabel(),
			Localization:    f.GetLocalization().String(),
			DetectorID:      detID,
			DetectorVersion: detVer,
			OffendingText:   offendingText(text, f.GetSpan()),
			Severity:        s.severity.level(category),
		}
		if sp := f.GetSpan(); sp != nil {
			start, end := int(sp.GetStart()), int(sp.GetEnd())
			fin.SpanStart, fin.SpanEnd, fin.SpanBasis = &start, &end, sp.GetBasis().String()
		}
		out = append(out, fin)
	}
	return out
}

// storeFindings persists the surviving findings plus one encrypted evidence row
// per unit (the offending field is the whole unit, kept for dashboard context —
// ADR-018). When nothing survives finding_exclude it writes neither: there is
// nothing left to evidence.
func (s *Scanner) storeFindings(ctx context.Context, task store.CheckTask, findings []store.Finding, text string) {
	if len(findings) == 0 {
		return
	}
	if ct, nonce, err := s.enc.seal(text); err == nil {
		expires := time.Now().Add(s.retention)
		if err := s.store.InsertEvidence(ctx, store.Evidence{
			CorrelationID: task.CorrelationID, UnitID: task.UnitID, CheckType: task.CheckType,
			Ciphertext: ct, Nonce: nonce, KeyID: s.enc.keyID, ExpiresAt: &expires,
		}); err != nil {
			s.logger.Warn("arbiter: insert evidence", "error", err)
		}
	} else {
		s.logger.Warn("arbiter: seal evidence", "error", err)
	}

	for _, fin := range findings {
		if err := s.store.InsertFinding(ctx, fin); err != nil {
			s.logger.Warn("arbiter: insert finding", "error", err)
		}
	}
}

// offendingText returns the flagged text for a finding, denormalized onto the
// finding row so the Security report can show it without a join or decrypt: the
// exact span substring when the detector localized the hit (UTF-8 byte offsets,
// the contract's only basis), the whole unit otherwise. A malformed or
// out-of-range span falls back to the whole unit rather than panicking — better
// to show too much context than to drop the evidence.
func offendingText(unit string, sp *detectv1.Span) string {
	if sp == nil || sp.GetBasis() != detectv1.OffsetBasis_OFFSET_BASIS_UTF8_BYTE {
		return unit
	}
	b := []byte(unit)
	start, end := int(sp.GetStart()), int(sp.GetEnd())
	if end > len(b) || start >= end {
		return unit
	}
	return string(b[start:end])
}

// handleFailure retries with backoff while attempts remain, else marks the task
// inconclusive — a timed-out / unreachable detector is not a clean result
// (ADR-008, ADR-017).
func (s *Scanner) handleFailure(ctx context.Context, task store.CheckTask, cause error) {
	if task.Attempt < s.maxAttempts {
		delay := s.backoffFor(task.Attempt)
		if err := s.store.RetryCheckTask(ctx, task.CorrelationID, task.UnitID, task.CheckType, time.Now().Add(delay)); err != nil {
			s.logger.Warn("arbiter: schedule retry", "error", err)
		}
		return
	}
	s.logger.Warn("arbiter: detector exhausted, inconclusive",
		"correlation_id", task.CorrelationID, "check", task.CheckType, "cause", cause)
	s.complete(ctx, task, statusInconclusive, resultErr(statusInconclusive, cause))
}

// backoffFor returns the retry delay for a (post-claim) attempt count, with up
// to 25% positive jitter so retries de-correlate across pods.
func (s *Scanner) backoffFor(attempt int) time.Duration {
	i := attempt - 1
	if i < 0 {
		i = 0
	}
	if i >= len(s.backoff) {
		i = len(s.backoff) - 1
	}
	base := s.backoff[i]
	return base + time.Duration(rand.Int64N(int64(base)/4+1)) //nolint:gosec // jitter only; backoff de-correlation is not security-sensitive
}

// audit appends one operational scan-failure row to the append-only scan_audit
// log so the dashboard can show WHEN scans fail, not just the aggregate
// inconclusive count. Non-fatal by design: a failed audit insert must never
// fail the task path (the scan failure is already being handled), so an insert
// error is logged at Warn and swallowed. detectorID is the detector that failed
// when known (empty for the no-detector site, where no detector call was made).
func (s *Scanner) audit(ctx context.Context, task store.CheckTask, detectorID, reason, detail string) {
	if err := s.store.InsertScanAudit(ctx, store.ScanAuditEntry{
		CorrelationID: task.CorrelationID,
		UnitID:        task.UnitID,
		CheckType:     task.CheckType,
		DetectorID:    detectorID,
		Reason:        reason,
		Attempts:      task.Attempt,
		Detail:        detail,
	}); err != nil {
		s.logger.Warn("arbiter: insert scan audit", "error", err, "reason", reason,
			"correlation_id", task.CorrelationID, "check", task.CheckType)
	}
}

func (s *Scanner) complete(ctx context.Context, task store.CheckTask, status string, result json.RawMessage) {
	if err := s.store.CompleteCheckTask(ctx, task.CorrelationID, task.UnitID, task.CheckType, status, result); err != nil {
		s.logger.Warn("arbiter: complete check task", "error", err)
	}
}

// resultOf builds the compact task-result summary from the SURVIVING findings
// (post finding_exclude), so the inspector count matches what was persisted.
func resultOf(status string, det detector, findings []store.Finding) json.RawMessage {
	var maxScore float32
	for _, f := range findings {
		if f.Score > maxScore {
			maxScore = f.Score
		}
	}
	b, _ := json.Marshal(taskResult{
		Status:   status,
		Findings: len(findings),
		MaxScore: maxScore,
		Detector: det.checkType,
	})
	return b
}

func resultErr(status string, cause error) json.RawMessage {
	b, _ := json.Marshal(taskResult{Status: status, Error: cause.Error()})
	return b
}
