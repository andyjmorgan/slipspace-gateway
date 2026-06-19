package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestUpsertRequestEventWithChecks(t *testing.T) {
	checks := []CheckTask{{CorrelationID: "c", UnitID: "u", CheckType: "injection"}} // nil locator => normalized to {}

	// success — also covers nil span/tags/locator normalization.
	if err := newStore(&fakeQuerier{beginTx: &fakeTx{}}).
		UpsertRequestEventWithChecks(ctx(), RequestEvent{CorrelationID: "c"}, checks); err != nil {
		t.Fatalf("success: %v", err)
	}
	// begin error
	if err := newStore(&fakeQuerier{beginErr: errors.New("x")}).
		UpsertRequestEventWithChecks(ctx(), RequestEvent{}, nil); err == nil {
		t.Error("want begin error")
	}
	// event exec error (1st Exec)
	if err := newStore(&fakeQuerier{beginTx: &fakeTx{failExecAt: 1}}).
		UpsertRequestEventWithChecks(ctx(), RequestEvent{}, checks); err == nil {
		t.Error("want event exec error")
	}
	// check exec error (2nd Exec)
	if err := newStore(&fakeQuerier{beginTx: &fakeTx{failExecAt: 2}}).
		UpsertRequestEventWithChecks(ctx(), RequestEvent{}, checks); err == nil {
		t.Error("want check exec error")
	}
	// commit error
	if err := newStore(&fakeQuerier{beginTx: &fakeTx{commitErr: errors.New("c")}}).
		UpsertRequestEventWithChecks(ctx(), RequestEvent{SpanEvent: []byte(`{"a":1}`), Tags: []string{"t"}}, nil); err == nil {
		t.Error("want commit error")
	}
}

func TestClaimCheckTasks(t *testing.T) {
	tasks, err := newStore(&fakeQuerier{freshRows: 2}).ClaimCheckTasks(ctx(), 10, 120)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("claim = %d tasks, err %v", len(tasks), err)
	}
	if _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).ClaimCheckTasks(ctx(), 10, 120); err == nil {
		t.Error("want query error")
	}
	if _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("s")}}}).ClaimCheckTasks(ctx(), 10, 120); err == nil {
		t.Error("want scan error")
	}
}

func TestCheckTaskUpdates(t *testing.T) {
	ok := newStore(&fakeQuerier{})
	if err := ok.CompleteCheckTask(ctx(), "c", "u", "k", "completed", nil); err != nil {
		t.Errorf("complete: %v", err)
	}
	if err := ok.RetryCheckTask(ctx(), "c", "u", "k", time.Now()); err != nil {
		t.Errorf("retry: %v", err)
	}
	bad := newStore(&fakeQuerier{execErr: errors.New("e")})
	if err := bad.CompleteCheckTask(ctx(), "c", "u", "k", "failed", []byte(`{}`)); err == nil {
		t.Error("want complete error")
	}
	if err := bad.RetryCheckTask(ctx(), "c", "u", "k", time.Now()); err == nil {
		t.Error("want retry error")
	}
}

func TestInsertFindingAndEvidence(t *testing.T) {
	ok := newStore(&fakeQuerier{})
	if err := ok.InsertFinding(ctx(), Finding{CorrelationID: "c"}); err != nil {
		t.Errorf("finding: %v", err)
	}
	exp := time.Now()
	if err := ok.InsertEvidence(ctx(), Evidence{CorrelationID: "c", Ciphertext: []byte{1}, Nonce: []byte{2}, ExpiresAt: &exp}); err != nil {
		t.Errorf("evidence: %v", err)
	}
	bad := newStore(&fakeQuerier{execErr: errors.New("e")})
	if err := bad.InsertFinding(ctx(), Finding{}); err == nil {
		t.Error("want finding error")
	}
	if err := bad.InsertEvidence(ctx(), Evidence{}); err == nil {
		t.Error("want evidence error")
	}
}

func TestListFindingsAndInconclusive(t *testing.T) {
	fs, err := newStore(&fakeQuerier{freshRows: 3}).ListFindings(ctx(), "c")
	if err != nil || len(fs) != 3 {
		t.Fatalf("findings = %d, err %v", len(fs), err)
	}
	if _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).ListFindings(ctx(), "c"); err == nil {
		t.Error("want list query error")
	}
	if _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("s")}}}).ListFindings(ctx(), "c"); err == nil {
		t.Error("want finding scan error")
	}

	ic, err := newStore(&fakeQuerier{freshRows: 1}).InconclusiveCheckTypes(ctx(), "c")
	if err != nil || len(ic) != 1 {
		t.Fatalf("inconclusive = %v, err %v", ic, err)
	}
	if _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).InconclusiveCheckTypes(ctx(), "c"); err == nil {
		t.Error("want inconclusive query error")
	}
	if _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("s")}}}).InconclusiveCheckTypes(ctx(), "c"); err == nil {
		t.Error("want inconclusive scan error")
	}
}

func TestCorrelationsReadyForVerdict(t *testing.T) {
	ids, err := newStore(&fakeQuerier{freshRows: 2}).CorrelationsReadyForVerdict(ctx(), 10)
	if err != nil || len(ids) != 2 {
		t.Fatalf("ready = %d, err %v", len(ids), err)
	}
	if _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).CorrelationsReadyForVerdict(ctx(), 10); err == nil {
		t.Error("want query error")
	}
	if _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("s")}}}).CorrelationsReadyForVerdict(ctx(), 10); err == nil {
		t.Error("want scan error")
	}
}

func TestListRecentFindings(t *testing.T) {
	// success — unbounded window (zero from/to) + default limit (limit <= 0).
	q := &fakeQuerier{freshRows: 2}
	items, err := newStore(q).ListRecentFindings(ctx(), time.Time{}, time.Time{}, 0)
	if err != nil || len(items) != 2 {
		t.Fatalf("findings = %d, err %v", len(items), err)
	}
	if len(q.querySQL) != 1 {
		t.Fatalf("query calls = %d, want 1", len(q.querySQL))
	}
	// projects the unit kind/role from the check task locator and joins the
	// request facts, newest-first.
	if !strings.Contains(q.querySQL[0], "unit_locator->>'kind'") || !strings.Contains(q.querySQL[0], "request_events") {
		t.Errorf("query missing locator/event join: %q", q.querySQL[0])
	}
	if !strings.Contains(q.querySQL[0], "ORDER BY e.observed_at DESC") {
		t.Errorf("query is not newest-first: %q", q.querySQL[0])
	}
	// an unbounded window emits no observed_at predicate.
	if strings.Contains(q.querySQL[0], "WHERE") {
		t.Errorf("unbounded query should have no WHERE: %q", q.querySQL[0])
	}

	// from-only bound -> a lower-bound predicate, no upper bound.
	now := time.Now()
	qf := &fakeQuerier{freshRows: 1}
	if _, err := newStore(qf).ListRecentFindings(ctx(), now.Add(-time.Hour), time.Time{}, 5); err != nil {
		t.Errorf("from-only: %v", err)
	}
	if !strings.Contains(qf.querySQL[0], "WHERE e.observed_at >= $1") || strings.Contains(qf.querySQL[0], "<=") {
		t.Errorf("from-only window predicate wrong: %q", qf.querySQL[0])
	}

	// to-only bound -> an upper-bound predicate only.
	qt := &fakeQuerier{freshRows: 1}
	if _, err := newStore(qt).ListRecentFindings(ctx(), time.Time{}, now, 0); err != nil {
		t.Errorf("to-only: %v", err)
	}
	if !strings.Contains(qt.querySQL[0], "WHERE e.observed_at <= $1") || strings.Contains(qt.querySQL[0], ">=") {
		t.Errorf("to-only window predicate wrong: %q", qt.querySQL[0])
	}

	// both bounds -> ANDed predicate, limit at $3.
	qb := &fakeQuerier{freshRows: 1}
	if _, err := newStore(qb).ListRecentFindings(ctx(), now.Add(-time.Hour), now, 5); err != nil {
		t.Errorf("both bounds: %v", err)
	}
	if !strings.Contains(qb.querySQL[0], "e.observed_at >= $1 AND e.observed_at <= $2") || !strings.Contains(qb.querySQL[0], "LIMIT $3") {
		t.Errorf("both-bound window predicate wrong: %q", qb.querySQL[0])
	}

	// query error
	if _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).ListRecentFindings(ctx(), time.Time{}, time.Time{}, 0); err == nil {
		t.Error("want query error")
	}
	// scan error
	if _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("s")}}}).ListRecentFindings(ctx(), time.Time{}, time.Time{}, 0); err == nil {
		t.Error("want scan error")
	}
}

func TestListFindingsBySession(t *testing.T) {
	// success — scoped by session_id, newest-first.
	q := &fakeQuerier{freshRows: 3}
	items, err := newStore(q).ListFindingsBySession(ctx(), "sess-1")
	if err != nil || len(items) != 3 {
		t.Fatalf("findings = %d, err %v", len(items), err)
	}
	if len(q.querySQL) != 1 || !strings.Contains(q.querySQL[0], "e.session_id = $1") {
		t.Errorf("query not session-scoped: %q", q.querySQL)
	}
	// query error
	if _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).ListFindingsBySession(ctx(), "s"); err == nil {
		t.Error("want query error")
	}
	// scan error
	if _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("s")}}}).ListFindingsBySession(ctx(), "s"); err == nil {
		t.Error("want scan error")
	}
}

func TestUpsertAndGetVerdict(t *testing.T) {
	if err := newStore(&fakeQuerier{}).UpsertVerdict(ctx(), Verdict{CorrelationID: "c", State: "flagged"}); err != nil {
		t.Errorf("upsert: %v", err)
	}
	if err := newStore(&fakeQuerier{execErr: errors.New("e")}).UpsertVerdict(ctx(), Verdict{Inconclusive: []string{"x"}, Provenance: []byte(`{}`)}); err == nil {
		t.Error("want upsert error")
	}
	// get: success (fakeRow ignores non-int dest)
	if _, err := newStore(&fakeQuerier{}).GetVerdict(ctx(), "c"); err != nil {
		t.Errorf("get: %v", err)
	}
	// not found
	if _, err := newStore(&fakeQuerier{row: fakeRow{err: pgx.ErrNoRows}}).GetVerdict(ctx(), "c"); !errors.Is(err, ErrVerdictNotFound) {
		t.Errorf("want ErrVerdictNotFound, got %v", err)
	}
	// generic error
	if _, err := newStore(&fakeQuerier{row: fakeRow{err: errors.New("boom")}}).GetVerdict(ctx(), "c"); err == nil {
		t.Error("want get error")
	}
}
