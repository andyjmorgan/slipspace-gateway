package store

import (
	"errors"
	"testing"
	"time"
)

func TestInsertScanAudit(t *testing.T) {
	// Happy path: a well-formed entry issues one Exec.
	q := &fakeQuerier{}
	e := ScanAuditEntry{CorrelationID: "c", UnitID: "in:0:0", CheckType: "injection",
		DetectorID: "det", Reason: ScanReasonTimeout, Attempts: 5, Detail: "deadline"}
	if err := newStore(q).InsertScanAudit(ctx(), e); err != nil {
		t.Fatalf("InsertScanAudit: %v", err)
	}
	if q.execCalls != 1 {
		t.Fatalf("exec calls = %d, want 1", q.execCalls)
	}

	// Validation: missing correlation_id or reason is rejected before any Exec.
	for _, bad := range []ScanAuditEntry{
		{Reason: ScanReasonTimeout}, // no correlation id
		{CorrelationID: "c"},        // no reason
	} {
		q := &fakeQuerier{}
		if err := newStore(q).InsertScanAudit(ctx(), bad); err == nil {
			t.Errorf("InsertScanAudit(%+v): want validation error", bad)
		}
		if q.execCalls != 0 {
			t.Errorf("validation should short-circuit before Exec, got %d calls", q.execCalls)
		}
	}

	// Exec error is wrapped.
	if err := newStore(&fakeQuerier{execErr: errors.New("boom")}).InsertScanAudit(ctx(), e); err == nil {
		t.Fatal("want exec error")
	}
}

func TestListScanAudit(t *testing.T) {
	now := time.Now()

	// OK: two rows drained and closed.
	q := &fakeQuerier{query: &fakeRows{scanErrs: []error{nil, nil}}}
	got, err := newStore(q).ListScanAudit(ctx(), now.Add(-time.Hour), now, 100)
	if err != nil {
		t.Fatalf("ListScanAudit: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2", len(got))
	}
	if !q.query.closed {
		t.Error("rows not closed")
	}

	// Query error.
	if _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).ListScanAudit(ctx(), now.Add(-time.Hour), now, 100); err == nil {
		t.Fatal("want query error")
	}

	// Scan error.
	if _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("scan")}}}).ListScanAudit(ctx(), now.Add(-time.Hour), now, 100); err == nil {
		t.Fatal("want scan error")
	}

	// rows.Err after a clean drain.
	if _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{nil}, finalErr: errors.New("rows")}}).ListScanAudit(ctx(), now.Add(-time.Hour), now, 100); err == nil {
		t.Fatal("want rows.Err")
	}
}
