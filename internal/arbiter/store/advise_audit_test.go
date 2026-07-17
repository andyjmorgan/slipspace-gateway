package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestInsertAdviseAudit(t *testing.T) {
	// Happy path: a well-formed entry issues one Exec.
	q := &fakeQuerier{}
	e := AdviseAuditEntry{GatewayID: "gw", ConversationID: "conv-1",
		RequestedModel: "big-model", VerdictSwitch: true, VerdictModel: "small-model",
		ToolNames: []string{"Read"}, JudgeLatencyMs: 1200}
	if err := newStore(q).InsertAdviseAudit(ctx(), e); err != nil {
		t.Fatalf("InsertAdviseAudit: %v", err)
	}
	if q.execCalls != 1 {
		t.Fatalf("exec calls = %d, want 1", q.execCalls)
	}

	// Nil tool slice must not reach pgx as NULL (column is NOT NULL).
	e.ToolNames = nil
	if err := newStore(&fakeQuerier{}).InsertAdviseAudit(ctx(), e); err != nil {
		t.Fatalf("InsertAdviseAudit(nil tools): %v", err)
	}

	// Validation: missing gateway_id or conversation_id is rejected before Exec.
	for _, bad := range []AdviseAuditEntry{
		{ConversationID: "conv-1"}, // no gateway id
		{GatewayID: "gw"},          // no conversation id
	} {
		q := &fakeQuerier{}
		if err := newStore(q).InsertAdviseAudit(ctx(), bad); err == nil {
			t.Errorf("InsertAdviseAudit(%+v): want validation error", bad)
		}
		if q.execCalls != 0 {
			t.Errorf("validation should short-circuit before Exec, got %d calls", q.execCalls)
		}
	}

	// Exec error is wrapped.
	if err := newStore(&fakeQuerier{execErr: errors.New("boom")}).InsertAdviseAudit(ctx(), e); err == nil {
		t.Fatal("want exec error")
	}
}

func TestListAdviseAudit(t *testing.T) {
	// OK: two rows drained and closed; a zero `before` and an oversized limit
	// are both normalized rather than rejected.
	q := &fakeQuerier{query: &fakeRows{scanErrs: []error{nil, nil}}}
	got, err := newStore(q).ListAdviseAudit(ctx(), 10_000, time.Time{})
	if err != nil {
		t.Fatalf("ListAdviseAudit: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2", len(got))
	}
	if !q.query.closed {
		t.Error("rows not closed")
	}

	// Query error.
	if _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).ListAdviseAudit(ctx(), 10, time.Now()); err == nil {
		t.Fatal("want query error")
	}

	// Scan error.
	if _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("scan")}}}).ListAdviseAudit(ctx(), 10, time.Now()); err == nil {
		t.Fatal("want scan error")
	}

	// rows.Err after a clean drain.
	if _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{nil}, finalErr: errors.New("rows")}}).ListAdviseAudit(ctx(), 10, time.Now()); err == nil {
		t.Fatal("want rows.Err")
	}
}

func TestAdviseSavings(t *testing.T) {
	since := time.Now().Add(-24 * time.Hour)

	// OK: one savings row drained, then the judge-cost scalar.
	q := &fakeQuerier{query: &fakeRows{scanErrs: []error{nil}}}
	rowsOut, judge, err := newStore(q).AdviseSavings(ctx(), since, "advise-judge")
	if err != nil {
		t.Fatalf("AdviseSavings: %v", err)
	}
	if len(rowsOut) != 1 {
		t.Fatalf("rows = %d, want 1", len(rowsOut))
	}
	if judge != 0 { // fakeRow scans nothing; the zero value is the assertion
		t.Fatalf("judge cost = %v, want 0", judge)
	}
	if q.rowCalls != 1 {
		t.Fatalf("QueryRow calls = %d, want 1", q.rowCalls)
	}
	// The join must key on the agent-route tag — the tripwire against a
	// regression to an unscoped conversation join that would count unpinned
	// requests as savings.
	if len(q.querySQL) != 1 || !strings.Contains(q.querySQL[0], "'agent-route:' || p.verdict_model") {
		t.Fatalf("savings query lost the agent-route tag join:\n%s", strings.Join(q.querySQL, "\n"))
	}

	// Query error.
	if _, _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).AdviseSavings(ctx(), since, "j"); err == nil {
		t.Fatal("want query error")
	}

	// Scan error.
	if _, _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("scan")}}}).AdviseSavings(ctx(), since, "j"); err == nil {
		t.Fatal("want scan error")
	}

	// rows.Err after a clean drain.
	if _, _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{nil}, finalErr: errors.New("rows")}}).AdviseSavings(ctx(), since, "j"); err == nil {
		t.Fatal("want rows.Err")
	}

	// Judge-cost QueryRow error.
	if _, _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{nil}}, rowFailAt: 1}).AdviseSavings(ctx(), since, "j"); err == nil {
		t.Fatal("want judge-cost error")
	}
}
