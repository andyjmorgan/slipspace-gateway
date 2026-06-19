package store

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestUpsertToolCalls(t *testing.T) {
	// Empty slice is a no-op: no transaction is opened.
	q0 := &fakeQuerier{}
	if err := newStore(q0).UpsertToolCalls(ctx(), nil); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if q0.beginCalls != 0 {
		t.Errorf("begin calls = %d, want 0 for empty", q0.beginCalls)
	}

	obs := []ToolCallObservation{
		{Kind: ToolCallHalf, ToolCallID: "t1", ToolName: "Skill", Arguments: []byte(`{"a":1}`), ObservedAt: time.Unix(1, 0)},
		{Kind: ToolResultHalf, ToolCallID: "t1", Result: "ok"},
	}
	q := &fakeQuerier{beginTx: &fakeTx{}}
	if err := newStore(q).UpsertToolCalls(ctx(), obs); err != nil {
		t.Fatalf("ok: %v", err)
	}
	if tx := q.beginTx.(*fakeTx); tx.execCalls != 2 {
		t.Errorf("exec calls = %d, want 2 (call + result halves)", tx.execCalls)
	}

	// Error paths: begin, exec, commit.
	if err := newStore(&fakeQuerier{beginErr: errors.New("b")}).UpsertToolCalls(ctx(), obs); err == nil {
		t.Fatal("want begin error")
	}
	if err := newStore(&fakeQuerier{beginTx: &fakeTx{execErr: errors.New("e")}}).UpsertToolCalls(ctx(), obs); err == nil {
		t.Fatal("want exec error")
	}
	if err := newStore(&fakeQuerier{beginTx: &fakeTx{commitErr: errors.New("c")}}).UpsertToolCalls(ctx(), obs); err == nil {
		t.Fatal("want commit error")
	}
}

func TestListToolCalls(t *testing.T) {
	// Two rows with limit 1: the +1 sentinel triggers a next cursor.
	q := &fakeQuerier{query: &fakeRows{scanErrs: []error{nil, nil}}}
	got, next, err := newStore(q).ListToolCalls(ctx(), ToolCallListParams{Limit: 1})
	if err != nil {
		t.Fatalf("ok: %v", err)
	}
	if len(got) != 1 || next == "" {
		t.Fatalf("got %d rows, next %q", len(got), next)
	}
	if !q.query.closed {
		t.Error("rows not closed")
	}

	// All filters + a valid cursor exercise the WHERE/keyset build without error.
	cur := encodeToolCallCursor(toolCallCursor{ObservedAt: time.Unix(2, 0), ID: "z"})
	full := &fakeQuerier{query: &fakeRows{scanErrs: []error{nil}}}
	if _, _, err := newStore(full).ListToolCalls(ctx(), ToolCallListParams{
		From: time.Unix(1, 0), To: time.Unix(9, 0),
		Name: "Skill", SessionID: "s", Provider: "anthropic", Configuration: "dev",
		Protocol: "messages", Model: "claude-x", Status: "resolved", Cursor: cur, Limit: 50,
	}); err != nil {
		t.Fatalf("full filters: %v", err)
	}
	// pending status takes the other branch.
	if _, _, err := newStore(&fakeQuerier{query: &fakeRows{}}).ListToolCalls(ctx(), ToolCallListParams{Status: "pending"}); err != nil {
		t.Fatalf("pending: %v", err)
	}

	// Bad cursor, query error, scan error.
	if _, _, err := newStore(&fakeQuerier{}).ListToolCalls(ctx(), ToolCallListParams{Cursor: "!!!"}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("want ErrInvalidCursor, got %v", err)
	}
	if _, _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).ListToolCalls(ctx(), ToolCallListParams{}); err == nil {
		t.Fatal("want query error")
	}
	if _, _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("s")}}}).ListToolCalls(ctx(), ToolCallListParams{}); err == nil {
		t.Fatal("want scan error")
	}
	if _, _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{nil}, finalErr: errors.New("rows")}}).ListToolCalls(ctx(), ToolCallListParams{}); err == nil {
		t.Fatal("want rows.Err")
	}
}

func TestGetToolCall(t *testing.T) {
	if _, err := newStore(&fakeQuerier{row: fakeRow{}}).GetToolCall(ctx(), "t1"); err != nil {
		t.Fatalf("ok: %v", err)
	}
	if _, err := newStore(&fakeQuerier{row: fakeRow{err: pgx.ErrNoRows}}).GetToolCall(ctx(), "t1"); !errors.Is(err, ErrToolCallNotFound) {
		t.Fatalf("want ErrToolCallNotFound, got %v", err)
	}
	if _, err := newStore(&fakeQuerier{row: fakeRow{err: errors.New("db")}}).GetToolCall(ctx(), "t1"); err == nil {
		t.Fatal("want scan error")
	}
}

func TestToolNames(t *testing.T) {
	q := &fakeQuerier{query: &fakeRows{scanErrs: []error{nil, nil, nil}}}
	got, err := newStore(q).ToolNames(ctx())
	if err != nil {
		t.Fatalf("ok: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("names = %d, want 3", len(got))
	}
	if _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).ToolNames(ctx()); err == nil {
		t.Fatal("want query error")
	}
}

func TestToolCall_Resolved(t *testing.T) {
	at := time.Unix(1, 0)
	if (ToolCall{}).Resolved() {
		t.Error("no responded_at -> not resolved")
	}
	if !(ToolCall{RespondedAt: &at}).Resolved() {
		t.Error("responded_at set -> resolved")
	}
}
