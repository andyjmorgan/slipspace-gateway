package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/contracts/admin"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
)

// handleVerdict serves the Arbiter security verdict + findings for one request
// by correlation id. A missing verdict (scan pending, or the scanner disabled)
// is not an error: it returns 200 with a null verdict and whatever findings
// exist, so the console can render "no verdict yet" rather than a 404.
func (s *Server) handleVerdict(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	resp := admin.VerdictResponse{CorrelationID: id, Findings: []admin.FindingView{}}

	v, err := s.queries.GetVerdict(r.Context(), id)
	switch {
	case err == nil:
		resp.Verdict = &admin.VerdictView{
			State:        v.State,
			MaxScore:     v.MaxScore,
			TopCategory:  v.TopCategory,
			Severity:     v.Severity,
			FindingCount: v.FindingCount,
			Inconclusive: v.Inconclusive,
		}
	case errors.Is(err, store.ErrVerdictNotFound):
		// leave Verdict nil
	default:
		s.queryError(w, "get verdict", err)
		return
	}

	findings, err := s.queries.ListFindings(r.Context(), id)
	if err != nil {
		s.queryError(w, "list findings", err)
		return
	}
	for _, f := range findings {
		resp.Findings = append(resp.Findings, admin.FindingView{
			UnitID:        f.UnitID,
			CheckType:     f.CheckType,
			Category:      f.Category,
			Score:         f.Score,
			RawLabel:      f.RawLabel,
			Detector:      f.DetectorID,
			Localization:  f.Localization,
			OffendingText: f.OffendingText,
			Severity:      f.Severity,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleFindings serves the operator Security view: a flat list of findings,
// newest first. With ?session=<id> it returns every finding in that one session
// (the session view's Security tab); without it, the most recent findings across
// all sessions (the top-level Security page), bounded by ?limit (store default
// ~100). One endpoint, one row shape — the reusable FindingsTable renders both.
func (s *Server) handleFindings(w http.ResponseWriter, r *http.Request) {
	var (
		rows []store.FindingRow
		err  error
	)
	if session := r.URL.Query().Get("session"); session != "" {
		rows, err = s.queries.ListFindingsBySession(r.Context(), session)
	} else {
		rows, err = s.queries.ListRecentFindings(r.Context(), limitParam(r, 0))
	}
	if err != nil {
		s.queryError(w, "list findings", err)
		return
	}
	resp := admin.FindingsListResponse{Items: make([]admin.FindingRow, 0, len(rows))}
	for _, f := range rows {
		var observed string
		if !f.ObservedAt.IsZero() {
			observed = f.ObservedAt.UTC().Format(time.RFC3339)
		}
		resp.Items = append(resp.Items, admin.FindingRow{
			Category:      f.Category,
			CheckType:     f.CheckType,
			Score:         f.Score,
			RawLabel:      f.RawLabel,
			UnitKind:      f.UnitKind,
			UnitRole:      f.UnitRole,
			CorrelationID: f.CorrelationID,
			SessionID:     f.SessionID,
			ObservedAt:    observed,
			Model:         f.Model,
			Configuration: f.Configuration,
			OffendingText: f.OffendingText,
			Severity:      f.Severity,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}
