package server

import (
	"errors"
	"net/http"

	"github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
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
			UnitID:       f.UnitID,
			CheckType:    f.CheckType,
			Category:     f.Category,
			Score:        f.Score,
			RawLabel:     f.RawLabel,
			Detector:     f.DetectorID,
			Localization: f.Localization,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}
