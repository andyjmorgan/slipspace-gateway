package arbiter

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"time"

	detectv1 "github.com/andyjmorgan/sluice-gateway/gen/slipspace/detect/v1"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
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
		s.complete(ctx, task, statusFailed, resultErr(statusFailed, fmt.Errorf("no detector for %q", task.CheckType)))
		return
	}

	unit, ok := s.resolveUnit(ctx, task)
	if !ok {
		// Span or unit gone / unscannable — we could not scan, so this is not
		// clean: mark inconclusive so the verdict reflects partial coverage.
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
		s.storeFindings(ctx, task, det, resp, text)
		s.complete(ctx, task, statusCompleted, resultOf(statusCompleted, det, resp))
	case detectv1.Status_STATUS_INCONCLUSIVE:
		s.complete(ctx, task, statusInconclusive, resultOf(statusInconclusive, det, resp))
	default:
		s.handleFailure(ctx, task, fmt.Errorf("detector status %s: %s", resp.GetStatus(), resp.GetError()))
	}
}

// storeFindings persists each hit plus one encrypted evidence row per unit (the
// offending field is the whole unit, kept for dashboard context — ADR-018).
func (s *Scanner) storeFindings(ctx context.Context, task store.CheckTask, det detector, resp *detectv1.DetectResponse, text string) {
	if len(resp.GetFindings()) == 0 {
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

	detID, detVer := det.checkType, ""
	if d := resp.GetDetector(); d != nil {
		if d.GetId() != "" {
			detID = d.GetId()
		}
		detVer = d.GetVersion()
	}
	for _, f := range resp.GetFindings() {
		fin := store.Finding{
			CorrelationID:   task.CorrelationID,
			UnitID:          task.UnitID,
			CheckType:       task.CheckType,
			Category:        f.GetCategory(),
			Score:           f.GetScore(),
			RawLabel:        f.GetRawLabel(),
			Localization:    f.GetLocalization().String(),
			DetectorID:      detID,
			DetectorVersion: detVer,
		}
		if sp := f.GetSpan(); sp != nil {
			start, end := int(sp.GetStart()), int(sp.GetEnd())
			fin.SpanStart, fin.SpanEnd, fin.SpanBasis = &start, &end, sp.GetBasis().String()
		}
		if err := s.store.InsertFinding(ctx, fin); err != nil {
			s.logger.Warn("arbiter: insert finding", "error", err)
		}
	}
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

func (s *Scanner) complete(ctx context.Context, task store.CheckTask, status string, result json.RawMessage) {
	if err := s.store.CompleteCheckTask(ctx, task.CorrelationID, task.UnitID, task.CheckType, status, result); err != nil {
		s.logger.Warn("arbiter: complete check task", "error", err)
	}
}

func resultOf(status string, det detector, resp *detectv1.DetectResponse) json.RawMessage {
	var maxScore float32
	for _, f := range resp.GetFindings() {
		if f.GetScore() > maxScore {
			maxScore = f.GetScore()
		}
	}
	b, _ := json.Marshal(taskResult{
		Status:   status,
		Findings: len(resp.GetFindings()),
		MaxScore: maxScore,
		Detector: det.checkType,
	})
	return b
}

func resultErr(status string, cause error) json.RawMessage {
	b, _ := json.Marshal(taskResult{Status: status, Error: cause.Error()})
	return b
}
