//go:build e2e

package arbiter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
)

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestScanner_OutboxLifecycle exercises the Arbiter scanner SQL end to end
// against real Postgres (migration v13 applied): the transactional outbox
// explode, the SKIP LOCKED drain + lease, finding/evidence inserts, the
// quiescence query, and the verdict round-trip.
func TestScanner_OutboxLifecycle(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	const corr = "e2e-scan-lifecycle"

	e := store.RequestEvent{CorrelationID: corr, ObservedAt: time.Now(), SpanEvent: []byte(`{"gen_ai_content":{}}`)}
	checks := []store.CheckTask{
		{CorrelationID: corr, UnitID: "in:0:0", CheckType: "injection", UnitLocator: []byte(`{"kind":"text"}`)},
		{CorrelationID: corr, UnitID: "in:0:1", CheckType: "toxicity", UnitLocator: []byte(`{}`)},
	}

	// Atomic explode, twice — the second is a re-ingest and must NOT duplicate
	// tasks (ON CONFLICT DO NOTHING).
	if err := st.UpsertRequestEventWithChecks(ctx, e, checks); err != nil {
		t.Fatalf("upsert+checks: %v", err)
	}
	if err := st.UpsertRequestEventWithChecks(ctx, e, checks); err != nil {
		t.Fatalf("re-ingest: %v", err)
	}

	// Real SKIP LOCKED drain.
	claimed, err := st.ClaimCheckTasks(ctx, 10, 120)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d, want 2 (re-ingest must not duplicate)", len(claimed))
	}
	for _, c := range claimed {
		if c.Attempt != 1 {
			t.Errorf("attempt = %d, want 1 after first claim", c.Attempt)
		}
	}
	// Re-claim immediately returns nothing — the lease is held.
	if again, _ := st.ClaimCheckTasks(ctx, 10, 120); len(again) != 0 {
		t.Errorf("re-claim got %d, want 0 while leased", len(again))
	}
	// Not quiescent while tasks are processing.
	if ready, _ := st.CorrelationsReadyForVerdict(ctx, 50); containsStr(ready, corr) {
		t.Error("ready for verdict while tasks still processing")
	}

	// One hit (+ evidence), one inconclusive.
	if err := st.InsertFinding(ctx, store.Finding{
		CorrelationID: corr, UnitID: "in:0:0", CheckType: "injection",
		Category: "injection.jailbreak", Score: 0.95, RawLabel: "INJECTION", DetectorID: "d", DetectorVersion: "1",
		Severity: "error",
	}); err != nil {
		t.Fatalf("finding: %v", err)
	}
	exp := time.Now().Add(time.Hour)
	if err := st.InsertEvidence(ctx, store.Evidence{
		CorrelationID: corr, UnitID: "in:0:0", CheckType: "injection",
		Ciphertext: []byte{1, 2, 3}, Nonce: []byte{4, 5, 6}, KeyID: "k1", ExpiresAt: &exp,
	}); err != nil {
		t.Fatalf("evidence: %v", err)
	}
	if err := st.CompleteCheckTask(ctx, corr, "in:0:0", "injection", "completed", []byte(`{"status":"completed"}`)); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := st.CompleteCheckTask(ctx, corr, "in:0:1", "toxicity", "inconclusive", nil); err != nil {
		t.Fatalf("complete inconclusive: %v", err)
	}

	// Now quiescent.
	if ready, _ := st.CorrelationsReadyForVerdict(ctx, 50); !containsStr(ready, corr) {
		t.Fatal("not ready for verdict after all tasks terminal")
	}
	findings, err := st.ListFindings(ctx, corr)
	if err != nil || len(findings) != 1 || findings[0].Category != "injection.jailbreak" || findings[0].Score != 0.95 {
		t.Fatalf("findings = %+v, err %v", findings, err)
	}
	// Severity round-trips through the v17 column.
	if findings[0].Severity != "error" {
		t.Errorf("finding severity = %q, want error", findings[0].Severity)
	}
	inc, err := st.InconclusiveCheckTypes(ctx, corr)
	if err != nil || len(inc) != 1 || inc[0] != "toxicity" {
		t.Fatalf("inconclusive = %+v, err %v", inc, err)
	}

	// Verdict round-trip.
	if err := st.UpsertVerdict(ctx, store.Verdict{
		CorrelationID: corr, State: "flagged", MaxScore: 0.95, TopCategory: "injection.jailbreak",
		FindingCount: 1, Inconclusive: []string{"toxicity"}, Severity: "error", Provenance: []byte(`{"max_score":0.95}`),
	}); err != nil {
		t.Fatalf("upsert verdict: %v", err)
	}
	v, err := st.GetVerdict(ctx, corr)
	if err != nil {
		t.Fatalf("get verdict: %v", err)
	}
	if v.State != "flagged" || v.MaxScore != 0.95 || v.TopCategory != "injection.jailbreak" ||
		len(v.Inconclusive) != 1 || v.Inconclusive[0] != "toxicity" || v.Severity != "error" {
		t.Errorf("verdict = %+v", v)
	}
	// Once a verdict exists, the span drops out of the ready set.
	if ready, _ := st.CorrelationsReadyForVerdict(ctx, 50); containsStr(ready, corr) {
		t.Error("still ready for verdict after verdict written")
	}
}

func TestScanner_RetryAndNotFound(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	const corr = "e2e-scan-retry"

	if err := st.UpsertRequestEventWithChecks(ctx, store.RequestEvent{CorrelationID: corr, ObservedAt: time.Now()},
		[]store.CheckTask{{CorrelationID: corr, UnitID: "u", CheckType: "injection"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimCheckTasks(ctx, 10, 120); err != nil {
		t.Fatal(err)
	}
	// Retry with a FUTURE next_attempt_at — not yet claimable.
	if err := st.RetryCheckTask(ctx, corr, "u", "injection", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if c, _ := st.ClaimCheckTasks(ctx, 10, 120); len(c) != 0 {
		t.Errorf("claimed a task whose retry is in the future: %d", len(c))
	}
	// Retry with a PAST next_attempt_at — claimable again, attempt increments.
	if err := st.RetryCheckTask(ctx, corr, "u", "injection", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	c, err := st.ClaimCheckTasks(ctx, 10, 120)
	if err != nil || len(c) != 1 {
		t.Fatalf("re-claim after due retry = %d, err %v", len(c), err)
	}
	if c[0].Attempt != 2 {
		t.Errorf("attempt = %d, want 2 after a retry+reclaim", c[0].Attempt)
	}

	if _, err := st.GetVerdict(ctx, "absent"); !errors.Is(err, store.ErrVerdictNotFound) {
		t.Errorf("GetVerdict(absent) err = %v, want ErrVerdictNotFound", err)
	}
}

// TestScanner_RecentFindingsWindow proves the Security page's from/to window
// filters real rows: two findings whose source events sit on either side of a
// midpoint, asserted against real Postgres through ListRecentFindings — the
// unbounded scan sees both, a from bound keeps only the newer, a to bound only
// the older. Guards the SQL the operator's relative-window preset depends on.
func TestScanner_RecentFindingsWindow(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)

	old := time.Now().Add(-48 * time.Hour)
	recent := time.Now().Add(-1 * time.Hour)
	mid := time.Now().Add(-24 * time.Hour)

	seed := func(corr string, observed time.Time) {
		if err := st.UpsertRequestEventWithChecks(ctx,
			store.RequestEvent{CorrelationID: corr, ObservedAt: observed},
			[]store.CheckTask{{CorrelationID: corr, UnitID: "u", CheckType: "injection", UnitLocator: []byte(`{"kind":"text","role":"user"}`)}}); err != nil {
			t.Fatalf("seed event %s: %v", corr, err)
		}
		if err := st.InsertFinding(ctx, store.Finding{
			CorrelationID: corr, UnitID: "u", CheckType: "injection",
			Category: "injection.jailbreak", Score: 0.9, RawLabel: "INJECTION", DetectorID: "d", DetectorVersion: "1", Severity: "error",
		}); err != nil {
			t.Fatalf("seed finding %s: %v", corr, err)
		}
	}
	seed("e2e-win-old", old)
	seed("e2e-win-recent", recent)

	corrs := func(rows []store.FindingRow) []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r.CorrelationID
		}
		return out
	}

	// Unbounded: both findings, newest first.
	all, err := st.ListRecentFindings(ctx, time.Time{}, time.Time{}, 0)
	if err != nil {
		t.Fatalf("unbounded: %v", err)
	}
	if !containsStr(corrs(all), "e2e-win-old") || !containsStr(corrs(all), "e2e-win-recent") {
		t.Fatalf("unbounded window missing rows: %v", corrs(all))
	}

	// from=mid -> only the recent finding.
	fromOnly, err := st.ListRecentFindings(ctx, mid, time.Time{}, 0)
	if err != nil {
		t.Fatalf("from-only: %v", err)
	}
	if containsStr(corrs(fromOnly), "e2e-win-old") || !containsStr(corrs(fromOnly), "e2e-win-recent") {
		t.Errorf("from=mid window = %v, want only recent", corrs(fromOnly))
	}

	// to=mid -> only the old finding.
	toOnly, err := st.ListRecentFindings(ctx, time.Time{}, mid, 0)
	if err != nil {
		t.Fatalf("to-only: %v", err)
	}
	if !containsStr(corrs(toOnly), "e2e-win-old") || containsStr(corrs(toOnly), "e2e-win-recent") {
		t.Errorf("to=mid window = %v, want only old", corrs(toOnly))
	}
}
