package arbiter

import (
	"context"
	"encoding/json"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/arbiter/store"
)

// Verdict states (ADR-017). Precedence FLAGGED ▸ PARTIAL ▸ CLEAN: a hit
// dominates; else any inconclusive/failed check raises PARTIAL; else CLEAN.
// inconclusive is never read as clean.
const (
	StateFlagged = "flagged"
	StatePartial = "partial"
	StateClean   = "clean"
)

// reduceLoop sweeps for spans whose checks are all terminal (quiescence) and
// writes their verdict. A periodic sweep — rather than firing off the last
// worker — keeps the verdict path independent of worker timing and naturally
// resumes after a restart.
func (s *Scanner) reduceLoop(ctx context.Context) {
	t := time.NewTicker(s.reduceInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ids, err := s.store.CorrelationsReadyForVerdict(ctx, reduceBatch)
			if err != nil {
				if ctx.Err() == nil {
					s.logger.Warn("arbiter: correlations ready for verdict", "error", err)
				}
				continue
			}
			for _, id := range ids {
				s.reduce(ctx, id)
			}
		}
	}
}

// reduce computes and writes one span's verdict from its stored findings and
// inconclusive set.
func (s *Scanner) reduce(ctx context.Context, correlationID string) {
	findings, err := s.store.ListFindings(ctx, correlationID)
	if err != nil {
		s.logger.Warn("arbiter: list findings", "correlation_id", correlationID, "error", err)
		return
	}
	inconclusive, err := s.store.InconclusiveCheckTypes(ctx, correlationID)
	if err != nil {
		s.logger.Warn("arbiter: inconclusive check types", "correlation_id", correlationID, "error", err)
		return
	}
	v := reduceVerdict(correlationID, findings, inconclusive)
	if err := s.store.UpsertVerdict(ctx, v); err != nil {
		s.logger.Warn("arbiter: upsert verdict", "correlation_id", correlationID, "error", err)
		return
	}
	// Re-emit the verdict as an enriched span (ADR-002); no-op unless configured.
	s.emitter.EmitVerdict(ctx, correlationID, v)
}

// reduceVerdict is the pure reduce: highest-risk-wins over the hit set, then the
// FLAGGED ▸ PARTIAL ▸ CLEAN state machine. Provenance records the inconclusive
// set and the winning score so a historical verdict stays interpretable.
func reduceVerdict(correlationID string, findings []store.Finding, inconclusive []string) store.Verdict {
	v := store.Verdict{
		CorrelationID: correlationID,
		FindingCount:  len(findings),
		Inconclusive:  inconclusive,
	}
	levels := make([]string, 0, len(findings))
	for _, f := range findings {
		if f.Score > v.MaxScore {
			v.MaxScore = f.Score
			v.TopCategory = f.Category
		}
		levels = append(levels, f.Severity)
	}
	// Roll the operator-assigned severities up to the span's worst level
	// (info < warning < error); empty when there are no findings.
	v.Severity = maxSeverity(levels)
	switch {
	case len(findings) > 0:
		v.State = StateFlagged
	case len(inconclusive) > 0:
		v.State = StatePartial
	default:
		v.State = StateClean
	}
	prov, _ := json.Marshal(map[string]any{
		"inconclusive":  inconclusive,
		"max_score":     v.MaxScore,
		"finding_count": v.FindingCount,
		"severity":      v.Severity,
	})
	v.Provenance = prov
	return v
}
