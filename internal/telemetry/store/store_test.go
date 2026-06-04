package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// --- hand-rolled pgx fakes (the fake-the-boundary pattern, no gomock) ---

type fakeRow struct{ err error }

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	// Populate an *int (SchemaVersion's COALESCE(MAX(version))) so callers that
	// scan a single int get a deterministic value; ignore other shapes.
	if len(dest) == 1 {
		if p, ok := dest[0].(*int); ok {
			*p = 1
		}
	}
	return nil
}

// fakeRows yields scanErrs (one per row); a nil entry is a successful Scan.
type fakeRows struct {
	scanErrs []error
	idx      int
	closed   bool
	finalErr error
}

func (r *fakeRows) Next() bool {
	if r.idx >= len(r.scanErrs) {
		return false
	}
	r.idx++
	return true
}
func (r *fakeRows) Scan(_ ...any) error { return r.scanErrs[r.idx-1] }
func (r *fakeRows) Close()              { r.closed = true }
func (r *fakeRows) Err() error          { return r.finalErr }

type fakeTx struct {
	execErr    error
	failExecAt int // 1-based call index to fail at; 0 means use execErr for all
	execCalls  int
	commitErr  error
	rolledBack bool
}

func (t *fakeTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	t.execCalls++
	if t.failExecAt != 0 {
		if t.execCalls == t.failExecAt {
			return pgconn.CommandTag{}, errors.New("exec failed")
		}
		return pgconn.CommandTag{}, nil
	}
	return pgconn.CommandTag{}, t.execErr
}
func (t *fakeTx) Commit(context.Context) error   { return t.commitErr }
func (t *fakeTx) Rollback(context.Context) error { t.rolledBack = true; return nil }

type fakeQuerier struct {
	execErr error
	query   *fakeRows // when set, returned by every Query call (shared, advances)
	// freshRows, when >0 and query==nil, makes Query return a fresh rows of this
	// many on every call (so each sub-query scans real rows).
	freshRows   int
	queryErr    error
	queryFailAt int // fail the Nth Query call (1-based); 0 = never
	queryCalls  int
	row         rowScanner
	rowFailAt   int // QueryRow returns an erroring row on the Nth call; 0 = never
	rowCalls    int
	beginTx     txn
	beginErr    error
	pingErr     error
	closed      bool
	execCalls   int
	beginCalls  int
}

func (q *fakeQuerier) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	q.execCalls++
	return pgconn.CommandTag{}, q.execErr
}
func (q *fakeQuerier) Query(context.Context, string, ...any) (rows, error) {
	q.queryCalls++
	if q.queryFailAt != 0 && q.queryCalls == q.queryFailAt {
		return nil, errors.New("query failed")
	}
	if q.queryErr != nil {
		return nil, q.queryErr
	}
	if q.query != nil {
		return q.query, nil
	}
	if q.freshRows > 0 {
		return &fakeRows{scanErrs: make([]error, q.freshRows)}, nil
	}
	return &fakeRows{}, nil
}
func (q *fakeQuerier) QueryRow(context.Context, string, ...any) rowScanner {
	q.rowCalls++
	if q.rowFailAt != 0 && q.rowCalls == q.rowFailAt {
		return fakeRow{err: errors.New("row failed")}
	}
	if q.row != nil {
		return q.row
	}
	return fakeRow{}
}
func (q *fakeQuerier) Begin(context.Context) (txn, error) {
	q.beginCalls++
	return q.beginTx, q.beginErr
}
func (q *fakeQuerier) Ping(context.Context) error { return q.pingErr }
func (q *fakeQuerier) Close()                     { q.closed = true }

func ctx() context.Context { return context.Background() }

// --- lifecycle ---

func TestPing(t *testing.T) {
	if err := newStore(&fakeQuerier{}).Ping(ctx()); err != nil {
		t.Fatalf("Ping ok: %v", err)
	}
	if err := newStore(&fakeQuerier{pingErr: errors.New("down")}).Ping(ctx()); err == nil {
		t.Fatal("Ping should propagate error")
	}
}

func TestClose(t *testing.T) {
	q := &fakeQuerier{}
	newStore(q).Close()
	if !q.closed {
		t.Fatal("Close should close the querier")
	}
}

func TestOpen_BadDSN(t *testing.T) {
	if _, err := Open(ctx(), "://not a dsn"); err == nil {
		t.Fatal("Open with malformed DSN: want error")
	}
}

func TestOpen_Unreachable(t *testing.T) {
	// Parses fine, but nothing listens on port 1 -> Ping fails fast.
	if _, err := Open(ctx(), "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1"); err == nil {
		t.Fatal("Open against unreachable host: want error")
	}
}

// TestPoolQuerier_Delegations exercises the *pgxpool.Pool adapter. The pool is
// created lazily (no connect until first use), so each delegation runs and then
// errors against the unreachable host — covering the adapter lines without a
// live Postgres (the happy paths are in test/e2e/telemetry).
func TestPoolQuerier_Delegations(t *testing.T) {
	pool, err := pgxpool.New(ctx(), "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	q := poolQuerier{pool: pool}
	defer q.Close()

	c, cancel := context.WithTimeout(ctx(), 3*time.Second)
	defer cancel()
	_, _ = q.Exec(c, "SELECT 1")
	_, _ = q.Query(c, "SELECT 1")
	_ = q.QueryRow(c, "SELECT 1")
	if _, err := q.Begin(c); err == nil {
		t.Error("Begin against unreachable host should error")
	}
	if err := q.Ping(c); err == nil {
		t.Error("Ping against unreachable host should error")
	}
}

// --- migrations ---

func TestMigrate_Success(t *testing.T) {
	q := &fakeQuerier{row: fakeRow{}, beginTx: &fakeTx{}}
	if err := newStore(q).Migrate(ctx()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// fakeRow scans version=1, so every migration with version > 1 (re)applies.
	want := 0
	for _, m := range migrations {
		if m.version > 1 {
			want++
		}
	}
	if q.beginCalls != want {
		t.Errorf("begins = %d, want %d", q.beginCalls, want)
	}
}

func TestMigrate_AppliesFromZero(t *testing.T) {
	// version 0 -> migration 1 applied.
	q := &fakeQuerier{row: zeroRow{}, beginTx: &fakeTx{}}
	if err := newStore(q).Migrate(ctx()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if q.beginCalls != len(migrations) {
		t.Errorf("begins = %d, want %d", q.beginCalls, len(migrations))
	}
}

// zeroRow scans a 0 schema version so all migrations apply.
type zeroRow struct{}

func (zeroRow) Scan(dest ...any) error {
	if p, ok := dest[0].(*int); ok {
		*p = 0
	}
	return nil
}

func TestMigrate_ApplyError(t *testing.T) {
	// version 0 so migration 1 is attempted, but Begin fails -> Migrate errors.
	q := &fakeQuerier{row: zeroRow{}, beginErr: errors.New("begin")}
	if err := newStore(q).Migrate(ctx()); err == nil {
		t.Fatal("want error from failed migration apply")
	}
}

func TestMigrate_EnsureTableError(t *testing.T) {
	q := &fakeQuerier{execErr: errors.New("boom")}
	if err := newStore(q).Migrate(ctx()); err == nil {
		t.Fatal("want error when schema_migrations create fails")
	}
}

func TestMigrate_VersionReadError(t *testing.T) {
	q := &fakeQuerier{row: fakeRow{err: errors.New("read")}}
	if err := newStore(q).Migrate(ctx()); err == nil {
		t.Fatal("want error when version read fails")
	}
}

func TestApplyMigration_Errors(t *testing.T) {
	m := migrations[0]
	cases := map[string]*fakeQuerier{
		"begin error":         {row: zeroRow{}, beginErr: errors.New("begin")},
		"sql error":           {row: zeroRow{}, beginTx: &fakeTx{failExecAt: 1}},
		"record insert error": {row: zeroRow{}, beginTx: &fakeTx{failExecAt: 2}},
		"commit error":        {row: zeroRow{}, beginTx: &fakeTx{commitErr: errors.New("commit")}},
	}
	for name, q := range cases {
		t.Run(name, func(t *testing.T) {
			if err := newStore(q).applyMigration(ctx(), m); err == nil {
				t.Fatalf("%s: want error", name)
			}
		})
	}
}

func TestSchemaVersion_Errors(t *testing.T) {
	if _, err := newStore(&fakeQuerier{row: fakeRow{err: errors.New("x")}}).SchemaVersion(ctx()); err == nil {
		t.Fatal("want wrapped error")
	}
	if _, err := newStore(&fakeQuerier{row: fakeRow{err: context.Canceled}}).SchemaVersion(ctx()); !errors.Is(err, context.Canceled) {
		t.Fatal("context.Canceled should pass through")
	}
}

// --- events ---

func TestUpsertRequestEvent(t *testing.T) {
	if err := newStore(&fakeQuerier{}).UpsertRequestEvent(ctx(), RequestEvent{CorrelationID: "c"}); err != nil {
		t.Fatalf("ok: %v", err)
	}
	if err := newStore(&fakeQuerier{execErr: errors.New("x")}).UpsertRequestEvent(ctx(), RequestEvent{}); err == nil {
		t.Fatal("want exec error")
	}
}

func TestGetRequestEvent(t *testing.T) {
	if _, err := newStore(&fakeQuerier{row: fakeRow{}}).GetRequestEvent(ctx(), "c"); err != nil {
		t.Fatalf("ok: %v", err)
	}
	if _, err := newStore(&fakeQuerier{row: fakeRow{err: pgx.ErrNoRows}}).GetRequestEvent(ctx(), "c"); !errors.Is(err, ErrRequestEventNotFound) {
		t.Fatalf("want ErrRequestEventNotFound, got %v", err)
	}
	if _, err := newStore(&fakeQuerier{row: fakeRow{err: errors.New("db")}}).GetRequestEvent(ctx(), "c"); err == nil {
		t.Fatal("want scan error")
	}
}

func TestListRecentRequestEvents(t *testing.T) {
	q := &fakeQuerier{query: &fakeRows{scanErrs: []error{nil, nil}}}
	got, err := newStore(q).ListRecentRequestEvents(ctx(), 0) // 0 -> default limit
	if err != nil {
		t.Fatalf("ok: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d", len(got))
	}
	if !q.query.closed {
		t.Error("rows not closed")
	}
	// query error
	if _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).ListRecentRequestEvents(ctx(), 5); err == nil {
		t.Fatal("want query error")
	}
	// scan error
	if _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("scan")}}}).ListRecentRequestEvents(ctx(), 5); err == nil {
		t.Fatal("want scan error")
	}
}

// --- payloads ---

func TestUpsertPayload(t *testing.T) {
	if err := newStore(&fakeQuerier{}).UpsertPayload(ctx(), Payload{CorrelationID: "c", Kind: KindRequestBody}); err != nil {
		t.Fatalf("ok: %v", err)
	}
	if err := newStore(&fakeQuerier{execErr: errors.New("x")}).UpsertPayload(ctx(), Payload{}); err == nil {
		t.Fatal("want exec error")
	}
}

func TestListPayloads(t *testing.T) {
	q := &fakeQuerier{query: &fakeRows{scanErrs: []error{nil}}}
	got, err := newStore(q).ListPayloads(ctx(), "c")
	if err != nil {
		t.Fatalf("ok: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
	// empty -> ErrPayloadNotFound
	if _, err := newStore(&fakeQuerier{query: &fakeRows{}}).ListPayloads(ctx(), "c"); !errors.Is(err, ErrPayloadNotFound) {
		t.Fatalf("want ErrPayloadNotFound, got %v", err)
	}
	// query error
	if _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).ListPayloads(ctx(), "c"); err == nil {
		t.Fatal("want query error")
	}
	// scan error
	if _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("s")}}}).ListPayloads(ctx(), "c"); err == nil {
		t.Fatal("want scan error")
	}
	// rows.Err() error after iteration
	if _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{nil}, finalErr: errors.New("rows")}}).ListPayloads(ctx(), "c"); err == nil {
		t.Fatal("want rows.Err error")
	}
}

// --- metrics ---

func TestInsertMetricPoints(t *testing.T) {
	if err := newStore(&fakeQuerier{}).InsertMetricPoints(ctx(), nil); err != nil {
		t.Fatalf("empty: %v", err)
	}
	pts := []MetricPoint{{Name: "m", Value: 1}, {Name: "n", Labels: []byte(`{"a":"b"}`), Value: 2}}
	if err := newStore(&fakeQuerier{beginTx: &fakeTx{}}).InsertMetricPoints(ctx(), pts); err != nil {
		t.Fatalf("ok: %v", err)
	}
	if err := newStore(&fakeQuerier{beginErr: errors.New("b")}).InsertMetricPoints(ctx(), pts); err == nil {
		t.Fatal("want begin error")
	}
	if err := newStore(&fakeQuerier{beginTx: &fakeTx{execErr: errors.New("e")}}).InsertMetricPoints(ctx(), pts); err == nil {
		t.Fatal("want exec error")
	}
	if err := newStore(&fakeQuerier{beginTx: &fakeTx{commitErr: errors.New("c")}}).InsertMetricPoints(ctx(), pts); err == nil {
		t.Fatal("want commit error")
	}
}

func TestListRecentMetricPoints(t *testing.T) {
	q := &fakeQuerier{query: &fakeRows{scanErrs: []error{nil, nil}}}
	got, err := newStore(q).ListRecentMetricPoints(ctx(), 0)
	if err != nil {
		t.Fatalf("ok: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d", len(got))
	}
	if _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).ListRecentMetricPoints(ctx(), 5); err == nil {
		t.Fatal("want query error")
	}
}

// --- helpers ---

func TestNullHelpers(t *testing.T) {
	if nullJSON(nil) != nil || nullTime(time.Time{}) != nil {
		t.Error("empty -> nil")
	}
	if nullJSON([]byte("x")) == nil {
		t.Error("non-empty json -> value")
	}
	if nullTime(time.Unix(1, 0)) == nil {
		t.Error("non-zero time -> value")
	}
}
