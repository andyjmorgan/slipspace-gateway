package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

func TestHandleVerdict_Flagged(t *testing.T) {
	q := &fakeQueries{
		verdict: store.Verdict{
			State: "flagged", MaxScore: 0.9, TopCategory: "injection.jailbreak",
			FindingCount: 1, Inconclusive: []string{"toxicity"},
		},
		findings: []store.Finding{{
			UnitID: "in:0:0", CheckType: "injection", Category: "injection.jailbreak",
			Score: 0.9, RawLabel: "INJECTION", DetectorID: "d", Localization: "LOCALIZATION_NONE",
		}},
	}
	resp := get(t, newQueryServer(t, q), "/api/v1/verdict/c1", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	var body admin.VerdictResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.CorrelationID != "c1" || body.Verdict == nil || body.Verdict.State != "flagged" {
		t.Fatalf("verdict = %+v", body.Verdict)
	}
	if body.Verdict.TopCategory != "injection.jailbreak" || len(body.Verdict.Inconclusive) != 1 {
		t.Errorf("verdict view = %+v", body.Verdict)
	}
	if len(body.Findings) != 1 || body.Findings[0].Detector != "d" || body.Findings[0].CheckType != "injection" {
		t.Errorf("findings = %+v", body.Findings)
	}
}

func TestHandleVerdict_NoVerdictYet(t *testing.T) {
	// Scan pending / scanner disabled: no verdict row, no findings -> 200 with
	// null verdict (not a 404).
	q := &fakeQueries{verdictErr: store.ErrVerdictNotFound}
	resp := get(t, newQueryServer(t, q), "/api/v1/verdict/c2", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	var body admin.VerdictResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Verdict != nil {
		t.Errorf("verdict = %+v, want nil", body.Verdict)
	}
	if body.Findings == nil {
		t.Error("findings should serialize as [] not null")
	}
}

func TestHandleVerdict_Errors(t *testing.T) {
	// verdict lookup error
	if rec := get(t, newQueryServer(t, &fakeQueries{verdictErr: errors.New("db")}), "/api/v1/verdict/c", true); rec.Code != http.StatusInternalServerError {
		t.Errorf("verdict error status = %d", rec.Code)
	}
	// findings lookup error (verdict ok)
	if rec := get(t, newQueryServer(t, &fakeQueries{findingsErr: errors.New("db")}), "/api/v1/verdict/c", true); rec.Code != http.StatusInternalServerError {
		t.Errorf("findings error status = %d", rec.Code)
	}
	// unauthenticated
	if rec := get(t, newQueryServer(t, &fakeQueries{}), "/api/v1/verdict/c", false); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauth status = %d, want 401", rec.Code)
	}
}
