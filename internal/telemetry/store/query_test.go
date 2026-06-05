package store

import (
	"errors"
	"testing"
	"time"
)

// rowsN builds a fakeRows that yields n successful rows.
func rowsN(n int) *fakeRows {
	errs := make([]error, n)
	return &fakeRows{scanErrs: errs}
}

// --- ListEventsFiltered ---

func TestListEventsFiltered_Page(t *testing.T) {
	q := &fakeQuerier{query: rowsN(2)}
	out, next, err := newStore(q).ListEventsFiltered(ctx(), EventListParams{Limit: 5})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) != 2 || next != "" {
		t.Fatalf("out=%d next=%q", len(out), next)
	}
}

func TestListEventsFiltered_NextCursor(t *testing.T) {
	// Limit 1 but 2 rows -> a further page exists, so next cursor is set.
	q := &fakeQuerier{query: rowsN(2)}
	out, next, err := newStore(q).ListEventsFiltered(ctx(), EventListParams{Limit: 1})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) != 1 || next == "" {
		t.Fatalf("expected 1 row + next cursor, got %d/%q", len(out), next)
	}
}

func TestListEventsFiltered_WindowAndFilterAndCap(t *testing.T) {
	q := &fakeQuerier{query: rowsN(1)}
	_, _, err := newStore(q).ListEventsFiltered(ctx(), EventListParams{
		From:   time.Unix(1, 0),
		To:     time.Unix(2, 0),
		Filter: EventFilter{Configuration: "c", StatusClass: "5xx"},
		Limit:  10000, // exercises the cap branch
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestListEventsFiltered_ValidCursor(t *testing.T) {
	cur := encodeCursor(eventCursor{ObservedAt: time.Unix(5, 0), Correlation: "c"})
	q := &fakeQuerier{query: rowsN(1)}
	if _, _, err := newStore(q).ListEventsFiltered(ctx(), EventListParams{Cursor: cur, Limit: 5}); err != nil {
		t.Fatalf("valid cursor: %v", err)
	}
}

func TestListEventsFiltered_Errors(t *testing.T) {
	if _, _, err := newStore(&fakeQuerier{}).ListEventsFiltered(ctx(), EventListParams{Cursor: "!!!"}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("want ErrInvalidCursor, got %v", err)
	}
	if _, _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).ListEventsFiltered(ctx(), EventListParams{}); err == nil {
		t.Fatal("want query error")
	}
	if _, _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("s")}}}).ListEventsFiltered(ctx(), EventListParams{}); err == nil {
		t.Fatal("want scan error")
	}
	if _, _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{nil}, finalErr: errors.New("rows")}}).ListEventsFiltered(ctx(), EventListParams{}); err == nil {
		t.Fatal("want rows.Err")
	}
}

func TestDecodeCursor_BadJSON(t *testing.T) {
	// valid base64 but not JSON -> ErrInvalidCursor (covers the unmarshal branch)
	if _, err := decodeCursor("YWJj"); !errors.Is(err, ErrInvalidCursor) { // "abc"
		t.Fatalf("want ErrInvalidCursor, got %v", err)
	}
}

// --- EventsBySession ---

func TestEventsBySession(t *testing.T) {
	if got, _ := newStore(&fakeQuerier{}).EventsBySession(ctx(), ""); got != nil {
		t.Error("empty session -> nil")
	}
	q := &fakeQuerier{query: rowsN(2)}
	got, err := newStore(q).EventsBySession(ctx(), "s")
	if err != nil || len(got) != 2 {
		t.Fatalf("got %d err %v", len(got), err)
	}
	if _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).EventsBySession(ctx(), "s"); err == nil {
		t.Fatal("want query error")
	}
	if _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("s")}}}).EventsBySession(ctx(), "s"); err == nil {
		t.Fatal("want scan error")
	}
}

// --- filter helpers ---

func TestStatusClassBounds(t *testing.T) {
	cases := []struct {
		class  string
		lo, hi int
		ok     bool
	}{
		{"2xx", 200, 299, true},
		{"4xx", 400, 499, true},
		{"5xx", 500, 0, true},
		{"", 0, 0, false},
		{"bogus", 0, 0, false},
	}
	for _, c := range cases {
		lo, hi, ok := statusClassBounds(c.class)
		if lo != c.lo || hi != c.hi || ok != c.ok {
			t.Errorf("%q -> (%d,%d,%v), want (%d,%d,%v)", c.class, lo, hi, ok, c.lo, c.hi, c.ok)
		}
	}
}

func TestAppendFilter_BetweenAndEquality(t *testing.T) {
	// 4xx exercises the BETWEEN (bounded upper) branch; equality covers a col.
	where, args := appendFilter(nil, nil, EventFilter{Provider: "anthropic", StatusClass: "4xx"})
	if len(args) != 3 { // provider + lo + hi
		t.Fatalf("args = %v", args)
	}
	joined := ""
	for _, w := range where {
		joined += w + " "
	}
	if !contains(joined, "BETWEEN") || !contains(joined, "provider =") {
		t.Errorf("where = %q", joined)
	}
}

func TestRate(t *testing.T) {
	if rate(0, 0) != 0 {
		t.Error("den 0 -> 0")
	}
	if rate(1, 4) != 0.25 {
		t.Error("1/4")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// --- dashboard ---

func dashParams() DashboardParams {
	return DashboardParams{From: time.Unix(0, 0), To: time.Unix(3600, 0), RecentFrom: time.Unix(3300, 0)}
}

func TestQueryDashboardSummary_Success(t *testing.T) {
	// freshRows so every Query-backed sub-query scans a real row (covers the
	// per-row assignment lines), and a default success row for the QueryRow ones.
	q := &fakeQuerier{freshRows: 1}
	if _, err := newStore(q).QueryDashboardSummary(ctx(), dashParams()); err != nil {
		t.Fatalf("summary: %v", err)
	}
}

// TestQueryDashboardSummary_StageErrors fails one sub-query at a time to cover
// every error-propagation return in QueryDashboardSummary. Call order:
// QueryRow #1 totals, #2 latency; Query #1 ByProvider, #2 ByConfiguration,
// #3 ByEndpoint, #4 ByModel, #5 RulesFired, #6 TagsFired, #7 ProviderHealth.
func TestQueryDashboardSummary_StageErrors(t *testing.T) {
	rowStages := []int{1, 2}
	for _, n := range rowStages {
		if _, err := newStore(&fakeQuerier{rowFailAt: n}).QueryDashboardSummary(ctx(), dashParams()); err == nil {
			t.Errorf("rowFailAt %d: want error", n)
		}
	}
	for n := 1; n <= 7; n++ {
		if _, err := newStore(&fakeQuerier{queryFailAt: n}).QueryDashboardSummary(ctx(), dashParams()); err == nil {
			t.Errorf("queryFailAt %d: want error", n)
		}
	}
}

func TestDashSubqueries_Errors(t *testing.T) {
	p := dashParams()
	st := func(q *fakeQuerier) *Store { return newStore(q) }

	// QueryRow-backed: totals, latency.
	if _, err := st(&fakeQuerier{row: fakeRow{err: errors.New("x")}}).queryDashTotals(ctx(), p); err == nil {
		t.Error("totals")
	}
	if _, err := st(&fakeQuerier{row: fakeRow{err: errors.New("x")}}).queryDashLatency(ctx(), p); err == nil {
		t.Error("latency")
	}

	// Query-backed: each has a query-error and a scan-error branch.
	queryBacked := map[string]func(*Store) error{
		"dimension": func(s *Store) error { _, e := s.queryDashDimension(ctx(), p, "provider"); return e },
		"endpoint":  func(s *Store) error { _, e := s.queryDashEndpoint(ctx(), p); return e },
		"model":     func(s *Store) error { _, e := s.queryDashModel(ctx(), p); return e },
		"fired":     func(s *Store) error { _, e := s.queryDashFired(ctx(), p, "tags"); return e },
		"health":    func(s *Store) error { _, e := s.queryDashProviderHealth(ctx(), p); return e },
	}
	for name, fn := range queryBacked {
		if fn(st(&fakeQuerier{queryErr: errors.New("q")})) == nil {
			t.Errorf("%s: want query error", name)
		}
		if fn(st(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("s")}}})) == nil {
			t.Errorf("%s: want scan error", name)
		}
	}

	// Allowlist guards.
	if _, err := st(&fakeQuerier{}).queryDashDimension(ctx(), p, "bogus"); err == nil {
		t.Error("unknown dimension should error")
	}
	if _, err := st(&fakeQuerier{}).queryDashFired(ctx(), p, "bogus"); err == nil {
		t.Error("unknown fired field should error")
	}
}

func TestQueryDashboardSeries(t *testing.T) {
	if _, err := newStore(&fakeQuerier{}).QueryDashboardSeries(ctx(), DashboardSeriesParams{BucketSeconds: 0}); err == nil {
		t.Fatal("bucket 0 must error")
	}
	q := &fakeQuerier{query: rowsN(1)}
	if _, err := newStore(q).QueryDashboardSeries(ctx(), DashboardSeriesParams{From: time.Unix(0, 0), To: time.Unix(60, 0), BucketSeconds: 30}); err != nil {
		t.Fatalf("series: %v", err)
	}
	if _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).QueryDashboardSeries(ctx(), DashboardSeriesParams{BucketSeconds: 30}); err == nil {
		t.Fatal("want query error")
	}
	if _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("s")}}}).QueryDashboardSeries(ctx(), DashboardSeriesParams{BucketSeconds: 30}); err == nil {
		t.Fatal("want scan error")
	}
}
