package store

import (
	"context"
	"errors"
	"strings"
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

// --- ListSessions ---

func TestListSessions_Page(t *testing.T) {
	q := &fakeQuerier{query: rowsN(2)}
	out, next, err := newStore(q).ListSessions(ctx(), SessionListParams{Limit: 5})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) != 2 || next != "" {
		t.Fatalf("out=%d next=%q", len(out), next)
	}
}

func TestListSessions_NextCursor(t *testing.T) {
	// Limit 1 but 2 rows -> a further page exists, so next cursor is set.
	q := &fakeQuerier{query: rowsN(2)}
	out, next, err := newStore(q).ListSessions(ctx(), SessionListParams{Limit: 1})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out) != 1 || next == "" {
		t.Fatalf("expected 1 row + next cursor, got %d/%q", len(out), next)
	}
}

func TestListSessions_WindowAndFilterAndCap(t *testing.T) {
	// Window bounds + a tag + configuration filter + the limit cap branch all in
	// one call, so the args/placeholder numbering across the CTE and outer WHERE
	// is exercised together.
	q := &fakeQuerier{query: rowsN(1)}
	_, _, err := newStore(q).ListSessions(ctx(), SessionListParams{
		From:   time.Unix(1, 0),
		To:     time.Unix(2, 0),
		Filter: EventFilter{Configuration: "prod", Tags: []string{"agent:x"}},
		Limit:  10000, // exercises the cap branch
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestListSessions_ValidCursor(t *testing.T) {
	cur := encodeSessionCursor(sessionCursor{LastAt: time.Unix(5, 0), SessionID: "s"})
	q := &fakeQuerier{query: rowsN(1)}
	if _, _, err := newStore(q).ListSessions(ctx(), SessionListParams{Cursor: cur, Limit: 5}); err != nil {
		t.Fatalf("valid cursor: %v", err)
	}
}

func TestListSessions_Errors(t *testing.T) {
	if _, _, err := newStore(&fakeQuerier{}).ListSessions(ctx(), SessionListParams{Cursor: "!!!"}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("want ErrInvalidCursor, got %v", err)
	}
	if _, _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).ListSessions(ctx(), SessionListParams{}); err == nil {
		t.Fatal("want query error")
	}
	if _, _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("s")}}}).ListSessions(ctx(), SessionListParams{}); err == nil {
		t.Fatal("want scan error")
	}
	if _, _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{nil}, finalErr: errors.New("rows")}}).ListSessions(ctx(), SessionListParams{}); err == nil {
		t.Fatal("want rows.Err")
	}
}

func TestDecodeSessionCursor_BadJSON(t *testing.T) {
	// valid base64 but not JSON -> ErrInvalidCursor (covers the unmarshal branch)
	if _, err := decodeSessionCursor("YWJj"); !errors.Is(err, ErrInvalidCursor) { // "abc"
		t.Fatalf("want ErrInvalidCursor, got %v", err)
	}
}

// --- EventsBySessionRollup ---

func TestEventsBySessionRollup(t *testing.T) {
	if got, _ := newStore(&fakeQuerier{}).EventsBySessionRollup(ctx(), ""); got != nil {
		t.Error("empty session -> nil")
	}
	q := &fakeQuerier{query: rowsN(2)}
	got, err := newStore(q).EventsBySessionRollup(ctx(), "s")
	if err != nil || len(got) != 2 {
		t.Fatalf("got %d err %v", len(got), err)
	}
	if _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).EventsBySessionRollup(ctx(), "s"); err == nil {
		t.Fatal("want query error")
	}
	if _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("s")}}}).EventsBySessionRollup(ctx(), "s"); err == nil {
		t.Fatal("want scan error")
	}
}

// TestEventsBySessionRollup_InternalBatching proves the rollup pages the table
// in keyset batches: a first batch of exactly sessionScanBatch rows triggers a
// second query seeking past the last row, and the second (shorter) batch ends
// the scan.
func TestEventsBySessionRollup_InternalBatching(t *testing.T) {
	// Two full batches then an empty terminator: the rollup must issue three
	// queries (the keyset seek after each full batch) and return every row.
	cq := &countingQuerier{full: 2, batch: sessionScanBatch}
	got, err := newStore(cq).EventsBySessionRollup(ctx(), "s")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 2*sessionScanBatch {
		t.Fatalf("rows = %d, want %d (two full batches)", len(got), 2*sessionScanBatch)
	}
	if cq.calls != 3 {
		t.Errorf("queries = %d, want 3 (batch, batch, empty terminator)", cq.calls)
	}
	// Batches after the first must seek past the previous position.
	for i, sql := range cq.querySQL[1:] {
		if !strings.Contains(sql, "(observed_at, correlation_id) >") {
			t.Errorf("batch %d does not keyset-seek: %s", i+2, sql)
		}
	}
}

// countingQuerier returns `full` consecutive full batches of `batch` rows from
// Query, then empty row sets, recording each statement.
type countingQuerier struct {
	fakeQuerier
	full  int
	batch int
	calls int
}

func (c *countingQuerier) Query(_ context.Context, sql string, _ ...any) (rows, error) {
	c.calls++
	c.querySQL = append(c.querySQL, sql)
	if c.calls <= c.full {
		return &fakeRows{scanErrs: make([]error, c.batch)}, nil
	}
	return &fakeRows{}, nil
}

// TestSessionReads_BlobDiscipline is the OOM regression tripwire (2026-06-10
// prod: 688-message session x ~410 KB blobs OOM-killed the 512 Mi pod): the
// whole-session rollup scan must NEVER select the full span_event blob — only
// the gen_ai_content-stripped projection — and the only full-blob session read
// (EventsBySessionPage) must always carry a LIMIT.
func TestSessionReads_BlobDiscipline(t *testing.T) {
	if !strings.Contains(sessionRollupColumns, "span_event - 'gen_ai_content'") {
		t.Fatalf("sessionRollupColumns must strip gen_ai_content: %s", sessionRollupColumns)
	}

	// The SQL the rollup actually issues uses the stripped projection.
	q := &fakeQuerier{query: rowsN(1)}
	if _, err := newStore(q).EventsBySessionRollup(ctx(), "s"); err != nil {
		t.Fatalf("rollup: %v", err)
	}
	if len(q.querySQL) == 0 {
		t.Fatal("rollup issued no query")
	}
	for _, sql := range q.querySQL {
		if !strings.Contains(sql, "span_event - 'gen_ai_content'") {
			t.Errorf("rollup query selects the full blob: %s", sql)
		}
		if !strings.Contains(sql, "LIMIT") {
			t.Errorf("rollup batch is unbounded: %s", sql)
		}
	}

	// The spans page reads the full projection but is always LIMIT-bounded.
	qp := &fakeQuerier{query: rowsN(1)}
	if _, err := newStore(qp).EventsBySessionPage(ctx(), SessionPageParams{SessionID: "s"}, func(RequestEvent) error { return nil }); err != nil {
		t.Fatalf("page: %v", err)
	}
	for _, sql := range qp.querySQL {
		if !strings.Contains(sql, "LIMIT") {
			t.Errorf("session page is unbounded: %s", sql)
		}
	}
}

// --- EventsBySessionPage ---

func TestEventsBySessionPage(t *testing.T) {
	// Empty session id -> no query, no cursor.
	if next, err := newStore(&fakeQuerier{}).EventsBySessionPage(ctx(), SessionPageParams{}, nil); err != nil || next != "" {
		t.Fatalf("empty id: next=%q err=%v", next, err)
	}

	// 3 rows with limit 2 -> fn sees 2, next cursor set.
	q := &fakeQuerier{query: rowsN(3)}
	n := 0
	next, err := newStore(q).EventsBySessionPage(ctx(), SessionPageParams{SessionID: "s", Limit: 2}, func(RequestEvent) error {
		n++
		return nil
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if n != 2 || next == "" {
		t.Fatalf("fn calls = %d next = %q, want 2 rows + a next cursor", n, next)
	}

	// 2 rows with limit 5 -> all delivered, no next cursor.
	q2 := &fakeQuerier{query: rowsN(2)}
	n = 0
	next, err = newStore(q2).EventsBySessionPage(ctx(), SessionPageParams{SessionID: "s", Limit: 5}, func(RequestEvent) error {
		n++
		return nil
	})
	if err != nil || n != 2 || next != "" {
		t.Fatalf("last page: n=%d next=%q err=%v", n, next, err)
	}
}

func TestEventsBySessionPage_CursorAndCap(t *testing.T) {
	// A cursor minted by the package decodes and seeks; Limit above the max is
	// capped (exercises both branches in one call).
	cur := encodeCursor(eventCursor{ObservedAt: time.Unix(5, 0), Correlation: "c"})
	q := &fakeQuerier{query: rowsN(1)}
	if _, err := newStore(q).EventsBySessionPage(ctx(), SessionPageParams{SessionID: "s", Cursor: cur, Limit: 10000}, func(RequestEvent) error { return nil }); err != nil {
		t.Fatalf("valid cursor: %v", err)
	}
}

func TestEventsBySessionPage_Errors(t *testing.T) {
	nop := func(RequestEvent) error { return nil }
	if _, err := newStore(&fakeQuerier{}).EventsBySessionPage(ctx(), SessionPageParams{SessionID: "s", Cursor: "!!!"}, nop); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("want ErrInvalidCursor, got %v", err)
	}
	if _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).EventsBySessionPage(ctx(), SessionPageParams{SessionID: "s"}, nop); err == nil {
		t.Fatal("want query error")
	}
	if _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("s")}}}).EventsBySessionPage(ctx(), SessionPageParams{SessionID: "s"}, nop); err == nil {
		t.Fatal("want scan error")
	}
	if _, err := newStore(&fakeQuerier{query: &fakeRows{scanErrs: []error{nil}, finalErr: errors.New("rows")}}).EventsBySessionPage(ctx(), SessionPageParams{SessionID: "s"}, nop); err == nil {
		t.Fatal("want rows.Err")
	}
	boom := errors.New("fn")
	if _, err := newStore(&fakeQuerier{query: rowsN(1)}).EventsBySessionPage(ctx(), SessionPageParams{SessionID: "s"}, func(RequestEvent) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("fn error must propagate, got %v", err)
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

func TestDecodeSpanFields(t *testing.T) {
	// Empty blob -> zero fields.
	if f := (RequestEvent{}).DecodeSpanFields(); f.TokensIn != 0 || f.Tags != nil {
		t.Errorf("empty blob -> %+v", f)
	}
	// Malformed blob -> zero fields (no panic).
	if f := (RequestEvent{SpanEvent: []byte("not json")}).DecodeSpanFields(); f.LatencyMs != 0 {
		t.Errorf("malformed blob -> %+v", f)
	}
	// Valid blob projects the drill-down fields.
	e := RequestEvent{SpanEvent: []byte(`{"sluice.method":"POST","sluice.latency_ms":42,"gen_ai.usage.input_tokens":10,"gen_ai.request.stream":true,"tags":["a"],"rules_fired":["r1"]}`)}
	f := e.DecodeSpanFields()
	if f.Method != "POST" || f.LatencyMs != 42 || f.TokensIn != 10 || !f.Streaming {
		t.Errorf("fields = %+v", f)
	}
	if len(f.Tags) != 1 || f.Tags[0] != "a" || len(f.RulesFired) != 1 || f.RulesFired[0] != "r1" {
		t.Errorf("tags/rules = %+v", f)
	}
}

func TestAppendFilter_GatewayBlobPath(t *testing.T) {
	// gateway_id has no column — it filters via the span_event JSONB path.
	where, args := appendFilter(nil, nil, EventFilter{Gateway: "gw-9"})
	if len(args) != 1 || args[0] != "gw-9" {
		t.Fatalf("args = %v", args)
	}
	if len(where) != 1 || !contains(where[0], "span_event->>'gateway_id' =") {
		t.Errorf("where = %v", where)
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

// TestAppendCaggFilter covers the dashboard-scoped filter applier: every CAGG
// equality dimension binds a predicate, the dropped dimensions (tags/gateway/id
// boxes) are ignored, and each status class produces the right band (the open
// 5xx upper bound vs the bounded 2xx/4xx). It asserts on the rendered WHERE
// fragments + bound args rather than going through SQL.
func TestAppendCaggFilter(t *testing.T) {
	f := EventFilter{
		Configuration: "c", Model: "m", Provider: "p", Protocol: "pr",
		// All of these are message-browser-only and must NOT appear.
		Gateway: "g", SessionID: "s", CorrelationID: "cid", AgentID: "a", UserID: "u",
		Tags: []string{"t1"},
	}
	where, args := appendCaggFilter([]string{"bucket >= $1", "bucket < $2"}, []any{1, 2}, f)
	// 2 window + 4 equality predicates; 6 args (2 window + 4 equality), no tag/id.
	if len(where) != 6 || len(args) != 6 {
		t.Fatalf("where=%d args=%d, want 6/6 (no tag/gateway/id predicates)", len(where), len(args))
	}
	joined := strings.Join(where, " ")
	for _, banned := range []string{"tag", "gateway", "session", "correlation", "agent", "user"} {
		if strings.Contains(strings.ToLower(joined), banned) {
			t.Errorf("dashboard filter leaked a message-browser dimension %q: %s", banned, joined)
		}
	}

	// 5xx: open upper bound (>= 500), one extra arg.
	w5, a5 := appendCaggFilter(nil, nil, EventFilter{StatusClass: "5xx"})
	if len(w5) != 1 || len(a5) != 1 || !strings.Contains(w5[0], ">=") {
		t.Errorf("5xx band = %v args %v, want one open >= predicate", w5, a5)
	}
	// 2xx: bounded band (BETWEEN), two extra args.
	w2, a2 := appendCaggFilter(nil, nil, EventFilter{StatusClass: "2xx"})
	if len(w2) != 1 || len(a2) != 2 || !strings.Contains(w2[0], "BETWEEN") {
		t.Errorf("2xx band = %v args %v, want one BETWEEN predicate with two bounds", w2, a2)
	}
}

// TestQueryDashFired_ConfigurationFilter exercises the configuration-equality
// branch of queryDashFired (the only filter the rule/tag CAGGs honor).
func TestQueryDashFired_ConfigurationFilter(t *testing.T) {
	p := dashParams()
	p.Filter = EventFilter{Configuration: "prod"}
	q := &fakeQuerier{query: rowsN(1)}
	if _, err := newStore(q).queryDashFired(ctx(), p, "rules"); err != nil {
		t.Fatalf("fired with config filter: %v", err)
	}
}

// TestQueryDashboardSeries_Filtered drives QueryDashboardSeries with a fully
// populated filter so the request-side status band and the token-side
// (status-stripped) filter both render.
func TestQueryDashboardSeries_Filtered(t *testing.T) {
	q := &fakeQuerier{query: rowsN(1)}
	_, err := newStore(q).QueryDashboardSeries(ctx(), DashboardSeriesParams{
		From: time.Unix(0, 0), To: time.Unix(60, 0), BucketSeconds: 30,
		Filter: EventFilter{Configuration: "c", Provider: "p", StatusClass: "5xx"},
	})
	if err != nil {
		t.Fatalf("series filtered: %v", err)
	}
}

// TestQueryDashboardSummary_StageErrors fails one sub-query at a time to cover
// every error-propagation return in QueryDashboardSummary. Call order:
// QueryRow #1 totals (requests CAGG), #2 totals (tokens CAGG); Query #1
// ByProvider, #2 ByConfiguration, #3 ByProtocol, #4 ByModel, #5 RulesFired,
// #6 TagsFired, #7 ProviderHealth.
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

	// QueryRow-backed: totals issues two QueryRows (requests, then tokens); the
	// first erroring row fails it.
	if _, err := st(&fakeQuerier{row: fakeRow{err: errors.New("x")}}).queryDashTotals(ctx(), p); err == nil {
		t.Error("totals")
	}
	// Cover the token-totals QueryRow error path specifically: first row scans
	// clean, second errors.
	if _, err := st(&fakeQuerier{rowFailAt: 2}).queryDashTotals(ctx(), p); err == nil {
		t.Error("token totals")
	}

	// Query-backed: each has a query-error and a scan-error branch.
	queryBacked := map[string]func(*Store) error{
		"dimension": func(s *Store) error { _, e := s.queryDashDimension(ctx(), p, "provider"); return e },
		"protocol":  func(s *Store) error { _, e := s.queryDashProtocol(ctx(), p); return e },
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
