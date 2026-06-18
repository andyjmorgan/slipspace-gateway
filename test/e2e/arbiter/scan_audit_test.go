//go:build e2e

package arbiter_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
)

// TestScanAuditLog proves InsertScanAudit / ListScanAudit end-to-end on the
// real Postgres engine over the migration-v16 scan_audit table: rows come back
// newest-first within the window, the limit caps the page, and a far-past
// window excludes everything. It inserts entries directly (the Arbiter worker
// writes them in production; the pipeline is exercised elsewhere).
func TestScanAuditLog(t *testing.T) {
	st := migratedStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// scan_audit carries no test-scoping column and occurred_at defaults to
	// now(), so a recent-window list would see other tests' rows. The package
	// runs tests sequentially (no t.Parallel) and each is self-contained, so a
	// truncate here is a safe deterministic slate.
	conn, err := pgx.Connect(ctx, sharedDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, "TRUNCATE scan_audit RESTART IDENTITY"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	// Insert three failures in order; occurred_at is DB-defaulted (now()), so
	// they land oldest-first and ListScanAudit must return them newest-first.
	entries := []store.ScanAuditEntry{
		{CorrelationID: "sa-1", UnitID: "in:0:0", CheckType: "injection", DetectorID: "inj",
			Reason: store.ScanReasonTimeout, Attempts: 5, Detail: "deadline exceeded"},
		{CorrelationID: "sa-2", UnitID: "in:0:1", CheckType: "pii",
			Reason: store.ScanReasonNoDetector},
		{CorrelationID: "sa-3", UnitID: "in:0:2", CheckType: "toxicity", DetectorID: "tox",
			Reason: store.ScanReasonDetectorError, Attempts: 2, Detail: "model oom"},
	}
	for i, e := range entries {
		if err := st.InsertScanAudit(ctx, e); err != nil {
			t.Fatalf("insert entry %d: %v", i, err)
		}
		time.Sleep(2 * time.Millisecond) // distinct occurred_at for a stable order
	}

	// Validation: an entry missing correlation_id / reason is rejected.
	if err := st.InsertScanAudit(ctx, store.ScanAuditEntry{Reason: store.ScanReasonTimeout}); err == nil {
		t.Error("InsertScanAudit with no correlation_id should error")
	}

	from := time.Now().Add(-time.Hour)
	to := time.Now().Add(time.Minute)
	got, err := st.ListScanAudit(ctx, from, to, 100)
	if err != nil {
		t.Fatalf("ListScanAudit: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("rows = %d, want 3", len(got))
	}
	// Newest-first: sa-3 inserted last comes first.
	if got[0].CorrelationID != "sa-3" || got[2].CorrelationID != "sa-1" {
		t.Fatalf("order not newest-first: %s … %s", got[0].CorrelationID, got[2].CorrelationID)
	}
	// Fields round-trip, including the DB-defaulted occurred_at.
	if got[0].CheckType != "toxicity" || got[0].DetectorID != "tox" ||
		got[0].Reason != store.ScanReasonDetectorError || got[0].Attempts != 2 || got[0].Detail != "model oom" {
		t.Fatalf("row[0] = %+v", got[0])
	}
	if got[0].OccurredAt.IsZero() {
		t.Error("occurred_at not populated on read")
	}

	// Limit caps the page (still newest-first).
	limited, err := st.ListScanAudit(ctx, from, to, 2)
	if err != nil {
		t.Fatalf("ListScanAudit (limit): %v", err)
	}
	if len(limited) != 2 || limited[0].CorrelationID != "sa-3" {
		t.Fatalf("limited = %+v, want 2 newest", limited)
	}

	// Window exclusion: a far-past window sees nothing.
	old, err := st.ListScanAudit(ctx, from.Add(-48*time.Hour), from.Add(-47*time.Hour), 100)
	if err != nil {
		t.Fatalf("ListScanAudit (old window): %v", err)
	}
	if len(old) != 0 {
		t.Fatalf("far-past window should be empty, got %d", len(old))
	}
}
