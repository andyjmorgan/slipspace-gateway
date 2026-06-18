//go:build e2e

package arbiter_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/andyjmorgan/sluice-gateway/internal/arbiter/store"
)

// TestDashboardSecurityRollup proves QueryDashboardSecurity end-to-end on the
// real Postgres engine: the verdict posture split (scanned/flagged/partial/
// clean), the finding breakdown by check type, and the top-categories cap — all
// over the migration-v15 time indexes on finding/verdict. It seeds verdicts and
// findings directly (the scan pipeline is exercised elsewhere) then asserts the
// store rollup.
func TestDashboardSecurityRollup(t *testing.T) {
	st := migratedStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// verdict/finding carry no test-scoping column and decided_at/created_at
	// default to now(), so a recent-window count would see other tests' rows.
	// The package runs tests sequentially (no t.Parallel) and each is
	// self-contained, so a truncate here is a safe deterministic slate.
	conn, err := pgx.Connect(ctx, sharedDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, "TRUNCATE finding, verdict RESTART IDENTITY"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	// Verdicts: 1 flagged, 1 partial, 2 clean → scanned = 4.
	verdicts := []store.Verdict{
		{CorrelationID: "ds-v1", State: "flagged", MaxScore: 0.9, TopCategory: "pii.email", FindingCount: 3},
		{CorrelationID: "ds-v2", State: "partial"},
		{CorrelationID: "ds-v3", State: "clean"},
		{CorrelationID: "ds-v4", State: "clean"},
	}
	for _, v := range verdicts {
		if err := st.UpsertVerdict(ctx, v); err != nil {
			t.Fatalf("seed verdict %s: %v", v.CorrelationID, err)
		}
	}

	// Findings: 6 distinct categories so the top-5 cap drops the smallest.
	// pii dominates (4), injection (2), toxicity (1) for the check-type split.
	findings := []store.Finding{
		{CorrelationID: "ds-v1", UnitID: "in:0:0", CheckType: "pii", Category: "pii.email"},
		{CorrelationID: "ds-v1", UnitID: "in:0:1", CheckType: "pii", Category: "pii.email"},
		{CorrelationID: "ds-v1", UnitID: "in:0:2", CheckType: "pii", Category: "pii.phone"},
		{CorrelationID: "ds-v1", UnitID: "in:0:3", CheckType: "pii", Category: "pii.address"},
		{CorrelationID: "ds-v2", UnitID: "in:0:0", CheckType: "injection", Category: "injection.jailbreak"},
		{CorrelationID: "ds-v2", UnitID: "in:0:1", CheckType: "injection", Category: "injection.exfiltration"},
		{CorrelationID: "ds-v2", UnitID: "in:0:2", CheckType: "toxicity", Category: "toxicity.severe"},
	}
	for i, f := range findings {
		f.Score = 0.8
		if err := st.InsertFinding(ctx, f); err != nil {
			t.Fatalf("seed finding %d: %v", i, err)
		}
	}

	from := time.Now().Add(-time.Hour)
	to := time.Now().Add(time.Minute)
	sec, err := st.QueryDashboardSecurity(ctx, from, to)
	if err != nil {
		t.Fatalf("QueryDashboardSecurity: %v", err)
	}

	if sec.Scanned != 4 || sec.Flagged != 1 || sec.Partial != 1 || sec.Clean != 2 {
		t.Fatalf("posture = %+v, want scanned 4 / flagged 1 / partial 1 / clean 2", sec)
	}

	// By check type: pii 4, injection 2, toxicity 1 — descending.
	byType := map[string]int64{}
	for _, c := range sec.ByCheckType {
		byType[c.Key] = c.Count
	}
	if byType["pii"] != 4 || byType["injection"] != 2 || byType["toxicity"] != 1 {
		t.Fatalf("by check type = %+v", sec.ByCheckType)
	}
	if len(sec.ByCheckType) > 0 && sec.ByCheckType[0].Key != "pii" {
		t.Fatalf("by check type should be count-descending, got %+v", sec.ByCheckType)
	}

	// Top categories: 6 distinct categories seeded, capped at 5. pii.email (2)
	// leads; one of the single-hit categories is dropped by the cap.
	if len(sec.TopCategories) != 5 {
		t.Fatalf("top categories should cap at 5, got %d: %+v", len(sec.TopCategories), sec.TopCategories)
	}
	if sec.TopCategories[0].Key != "pii.email" || sec.TopCategories[0].Count != 2 {
		t.Fatalf("top category should be pii.email=2, got %+v", sec.TopCategories[0])
	}

	// Window exclusion: a far-past window sees nothing.
	old, err := st.QueryDashboardSecurity(ctx, from.Add(-48*time.Hour), from.Add(-47*time.Hour))
	if err != nil {
		t.Fatalf("QueryDashboardSecurity (old window): %v", err)
	}
	if old.Scanned != 0 || len(old.ByCheckType) != 0 || len(old.TopCategories) != 0 {
		t.Fatalf("far-past window should be empty, got %+v", old)
	}
}
