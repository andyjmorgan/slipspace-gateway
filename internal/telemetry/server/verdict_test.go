package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

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

func TestHandleFindings_Recent(t *testing.T) {
	observed := time.Date(2026, 6, 14, 9, 30, 0, 0, time.UTC)
	q := &fakeQueries{findingRows: []store.FindingRow{
		{Category: "injection.jailbreak", CheckType: "injection", Score: 0.9, RawLabel: "INJECTION", UnitKind: "text", UnitRole: "user", CorrelationID: "c1", SessionID: "s1", ObservedAt: observed, Model: "gpt-4o", Configuration: "default"},
		{Category: "pii.email", CheckType: "pii", Score: 0.6, CorrelationID: "c2"},
	}}
	resp := get(t, newQueryServer(t, q), "/api/v1/findings?limit=25", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	var body admin.FindingsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(body.Items))
	}
	if body.Items[0].CorrelationID != "c1" || body.Items[0].Category != "injection.jailbreak" || body.Items[0].UnitRole != "user" {
		t.Errorf("item[0] = %+v", body.Items[0])
	}
	if body.Items[0].ObservedAt != "2026-06-14T09:30:00Z" {
		t.Errorf("observed_at = %q, want RFC3339 UTC", body.Items[0].ObservedAt)
	}
	// a finding whose event aged out has a zero observed_at -> empty string.
	if body.Items[1].ObservedAt != "" {
		t.Errorf("aged-out observed_at = %q, want empty", body.Items[1].ObservedAt)
	}
	// no ?session -> the recent path fired, with ?limit plumbed through.
	if q.lastFindingsLimit != 25 || q.lastFindingsSession != "" {
		t.Errorf("recent path args: limit=%d session=%q", q.lastFindingsLimit, q.lastFindingsSession)
	}
}

func TestHandleFindings_BySession(t *testing.T) {
	q := &fakeQueries{findingRows: []store.FindingRow{{Category: "pii.email", CheckType: "pii", CorrelationID: "c1", SessionID: "sess-7"}}}
	resp := get(t, newQueryServer(t, q), "/api/v1/findings?session=sess-7", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	// ?session -> the per-session path fired with that id (recent path untouched).
	if q.lastFindingsSession != "sess-7" || q.lastFindingsLimit != 0 {
		t.Errorf("session path args: session=%q limit=%d", q.lastFindingsSession, q.lastFindingsLimit)
	}
}

func TestHandleFindings_EmptyIsArray(t *testing.T) {
	// no rows -> items serializes as [] not null.
	resp := get(t, newQueryServer(t, &fakeQueries{}), "/api/v1/findings", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["items"]) != "[]" {
		t.Errorf("items = %s, want []", raw["items"])
	}
}

func TestHandleFindings_Errors(t *testing.T) {
	// recent-path error
	if rec := get(t, newQueryServer(t, &fakeQueries{findingRowsErr: errors.New("db")}), "/api/v1/findings", true); rec.Code != http.StatusInternalServerError {
		t.Errorf("recent error status = %d", rec.Code)
	}
	// session-path error
	if rec := get(t, newQueryServer(t, &fakeQueries{findingRowsErr: errors.New("db")}), "/api/v1/findings?session=s", true); rec.Code != http.StatusInternalServerError {
		t.Errorf("session error status = %d", rec.Code)
	}
	// unauthenticated
	if rec := get(t, newQueryServer(t, &fakeQueries{}), "/api/v1/findings", false); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauth status = %d, want 401", rec.Code)
	}
}
