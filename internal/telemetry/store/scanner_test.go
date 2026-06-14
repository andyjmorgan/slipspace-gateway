package store

import (
	"errors"
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
