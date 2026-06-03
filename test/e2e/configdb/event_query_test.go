//go:build e2e

package configdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// seedEvents inserts a fixed set of request events at controlled observed_at
// times so the stats + pagination assertions are deterministic.
func seedEvents(t *testing.T, db *configdb.DB, events []configdb.RequestEvent) {
	t.Helper()
	for _, e := range events {
		if err := db.UpsertRequestEvent(context.Background(), e); err != nil {
			t.Fatalf("seed %s: %v", e.CorrelationID, err)
		}
	}
}

// TestConfigDB_QueryEventStats_BucketingAndTotals exercises the stats rollup
// against real Postgres: time bucketing with zero-fill, headline totals, p95,
// error counting, and the status-class breakdown per bucket.
func TestConfigDB_QueryEventStats_BucketingAndTotals(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	// Bucket 0 [12:00,12:05): three 200s with latencies 10/20/30.
	// Bucket 1 [12:05,12:10): empty (must zero-fill).
	// Bucket 2 [12:10,12:15): a 200, a 404, a 500.
	seedEvents(t, db, []configdb.RequestEvent{
		{CorrelationID: "b0-1", Configuration: "prod", Backend: "openai", Model: "gpt-4o", Protocol: "chat", StatusCode: 200, LatencyMs: 10, TokensIn: 1, TokensOut: 2, ObservedAt: base.Add(1 * time.Minute)},
		{CorrelationID: "b0-2", Configuration: "prod", Backend: "openai", Model: "gpt-4o", Protocol: "chat", StatusCode: 200, LatencyMs: 20, TokensIn: 3, TokensOut: 4, ObservedAt: base.Add(2 * time.Minute)},
		{CorrelationID: "b0-3", Configuration: "prod", Backend: "openai", Model: "gpt-4o", Protocol: "chat", StatusCode: 200, LatencyMs: 30, TokensIn: 5, TokensOut: 6, ObservedAt: base.Add(3 * time.Minute)},
		{CorrelationID: "b2-1", Configuration: "prod", Backend: "openai", Model: "gpt-4o", Protocol: "chat", StatusCode: 200, LatencyMs: 40, ObservedAt: base.Add(11 * time.Minute)},
		{CorrelationID: "b2-2", Configuration: "prod", Backend: "openai", Model: "gpt-4o", Protocol: "chat", StatusCode: 404, LatencyMs: 50, ObservedAt: base.Add(12 * time.Minute)},
		{CorrelationID: "b2-3", Configuration: "prod", Backend: "openai", Model: "gpt-4o", Protocol: "chat", StatusCode: 500, LatencyMs: 60, ObservedAt: base.Add(13 * time.Minute)},
	})

	stats, err := db.QueryEventStats(ctx, configdb.EventStatsParams{
		From:          base,
		To:            base.Add(15 * time.Minute),
		BucketSeconds: 300,
	})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}

	// Totals across all six events.
	if stats.Totals.Requests != 6 {
		t.Errorf("total requests = %d, want 6", stats.Totals.Requests)
	}
	if stats.Totals.Errors != 2 {
		t.Errorf("total errors = %d, want 2 (404 + 500)", stats.Totals.Errors)
	}
	if stats.Totals.TokensIn != 9 || stats.Totals.TokensOut != 12 {
		t.Errorf("tokens = in %d / out %d, want 9 / 12", stats.Totals.TokensIn, stats.Totals.TokensOut)
	}
	wantRate := 2.0 / 6.0
	if stats.Totals.ErrorRate < wantRate-0.001 || stats.Totals.ErrorRate > wantRate+0.001 {
		t.Errorf("error_rate = %v, want ~%v", stats.Totals.ErrorRate, wantRate)
	}
	// p95 over [10,20,30,40,50,60] is 57.5 -> rounds to 58.
	if stats.Totals.P95LatencyMs != 58 {
		t.Errorf("p95 = %d, want 58", stats.Totals.P95LatencyMs)
	}

	// Three buckets across the 15-minute window at 5-minute width.
	if len(stats.Series) != 3 {
		t.Fatalf("series len = %d, want 3 buckets", len(stats.Series))
	}
	if stats.Series[0].Requests != 3 || stats.Series[0].Status2xx != 3 {
		t.Errorf("bucket 0 = %+v, want 3 requests / 3 2xx", stats.Series[0])
	}
	if !stats.Series[0].Ts.Equal(base) {
		t.Errorf("bucket 0 ts = %v, want %v", stats.Series[0].Ts, base)
	}
	// p95 of bucket 0 [10,20,30] is 29 -> rounds to 29.
	if stats.Series[0].P95LatencyMs != 29 {
		t.Errorf("bucket 0 p95 = %d, want 29", stats.Series[0].P95LatencyMs)
	}
	if stats.Series[1].Requests != 0 {
		t.Errorf("bucket 1 (empty) = %d requests, want 0 (zero-fill)", stats.Series[1].Requests)
	}
	if stats.Series[2].Requests != 3 || stats.Series[2].Status2xx != 1 || stats.Series[2].Status4xx != 1 || stats.Series[2].Status5xx != 1 {
		t.Errorf("bucket 2 = %+v, want 3 req / 1 each class", stats.Series[2])
	}
}

// TestConfigDB_QueryEventStats_Filters exercises every equality filter and the
// status-class mapping, confirming each narrows the result set.
func TestConfigDB_QueryEventStats_Filters(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	seedEvents(t, db, []configdb.RequestEvent{
		{CorrelationID: "a", Configuration: "prod", GatewayID: "gw-a", Backend: "openai", Model: "gpt-4o", Protocol: "chat", StatusCode: 200, ObservedAt: base.Add(1 * time.Minute)},
		{CorrelationID: "b", Configuration: "dev", GatewayID: "gw-b", Backend: "anthropic", Model: "claude", Protocol: "messages", StatusCode: 404, ObservedAt: base.Add(2 * time.Minute)},
		{CorrelationID: "c", Configuration: "prod", GatewayID: "gw-a", Backend: "openai", Model: "gpt-4o", Protocol: "chat", StatusCode: 503, ObservedAt: base.Add(3 * time.Minute)},
	})

	params := configdb.EventStatsParams{From: base, To: base.Add(10 * time.Minute), BucketSeconds: 600}

	cases := []struct {
		name   string
		filter configdb.EventFilter
		want   int64
	}{
		{"configuration", configdb.EventFilter{Configuration: "prod"}, 2},
		{"backend", configdb.EventFilter{Backend: "anthropic"}, 1},
		{"model", configdb.EventFilter{Model: "gpt-4o"}, 2},
		{"gateway", configdb.EventFilter{Gateway: "gw-b"}, 1},
		{"protocol", configdb.EventFilter{Protocol: "messages"}, 1},
		{"status 2xx", configdb.EventFilter{StatusClass: "2xx"}, 1},
		{"status 4xx", configdb.EventFilter{StatusClass: "4xx"}, 1},
		{"status 5xx", configdb.EventFilter{StatusClass: "5xx"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := params
			p.Filter = tc.filter
			s, qerr := db.QueryEventStats(ctx, p)
			if qerr != nil {
				t.Fatalf("stats: %v", qerr)
			}
			if s.Totals.Requests != tc.want {
				t.Errorf("requests = %d, want %d", s.Totals.Requests, tc.want)
			}
		})
	}
}

// TestConfigDB_QueryEventStats_GroupByTop exercises the top-10 breakdown and
// its DESC-by-count ordering.
func TestConfigDB_QueryEventStats_GroupByTop(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	var seed []configdb.RequestEvent
	// gpt-4o x3, claude x2, gemini x1.
	for i, m := range []string{"gpt-4o", "gpt-4o", "gpt-4o", "claude", "claude", "gemini"} {
		status := 200
		if i == 5 {
			status = 500
		}
		seed = append(seed, configdb.RequestEvent{
			CorrelationID: m + "-" + time.Duration(i).String(),
			Model:         m,
			StatusCode:    status,
			ObservedAt:    base.Add(time.Duration(i) * time.Minute),
		})
	}
	seedEvents(t, db, seed)

	stats, err := db.QueryEventStats(ctx, configdb.EventStatsParams{
		From:          base,
		To:            base.Add(10 * time.Minute),
		BucketSeconds: 600,
		GroupBy:       "model",
	})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(stats.Top) != 3 {
		t.Fatalf("top len = %d, want 3", len(stats.Top))
	}
	if stats.Top[0].Key != "gpt-4o" || stats.Top[0].Requests != 3 {
		t.Errorf("top[0] = %+v, want gpt-4o x3", stats.Top[0])
	}
	if stats.Top[1].Key != "claude" || stats.Top[1].Requests != 2 {
		t.Errorf("top[1] = %+v, want claude x2", stats.Top[1])
	}
	if stats.Top[2].Key != "gemini" || stats.Top[2].Requests != 1 || stats.Top[2].Errors != 1 || stats.Top[2].ErrorRate != 1.0 {
		t.Errorf("top[2] = %+v, want gemini x1 1 error", stats.Top[2])
	}
}

// TestConfigDB_QueryEventStats_EmptyRange confirms an empty window yields zeroed
// totals (not an error) and still zero-fills the series buckets.
func TestConfigDB_QueryEventStats_EmptyRange(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	stats, err := db.QueryEventStats(ctx, configdb.EventStatsParams{
		From:          base,
		To:            base.Add(10 * time.Minute),
		BucketSeconds: 300,
		GroupBy:       "model",
	})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Totals.Requests != 0 || stats.Totals.ErrorRate != 0 || stats.Totals.P95LatencyMs != 0 {
		t.Errorf("empty totals = %+v, want all zero", stats.Totals)
	}
	if len(stats.Series) != 2 {
		t.Errorf("series len = %d, want 2 zero-filled buckets", len(stats.Series))
	}
	if len(stats.Top) != 0 {
		t.Errorf("top = %+v, want empty", stats.Top)
	}
}

// TestConfigDB_QueryEventStats_BadBucket rejects a non-positive bucket width.
func TestConfigDB_QueryEventStats_BadBucket(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	if _, err := db.QueryEventStats(ctx, configdb.EventStatsParams{
		From:          time.Now(),
		To:            time.Now().Add(time.Hour),
		BucketSeconds: 0,
	}); err == nil {
		t.Fatal("want error for zero bucket width")
	}
}

// TestConfigDB_ListEventsFiltered_Keyset exercises keyset pagination: a stable
// (observed_at DESC, correlation_id DESC) order, the cursor round-trip across
// pages, and an empty next_cursor on the final page.
func TestConfigDB_ListEventsFiltered_Keyset(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	var seed []configdb.RequestEvent
	for i := 0; i < 5; i++ {
		seed = append(seed, configdb.RequestEvent{
			CorrelationID: string(rune('a' + i)),
			Model:         "gpt-4o",
			StatusCode:    200,
			ObservedAt:    base.Add(time.Duration(i) * time.Minute),
		})
	}
	seedEvents(t, db, seed)

	// Page 1: limit 2 -> newest two (e at 12:04, d at 12:03), a further page.
	page1, cur1, err := db.ListEventsFiltered(ctx, configdb.EventListParams{Limit: 2})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 || page1[0].CorrelationID != "e" || page1[1].CorrelationID != "d" {
		t.Fatalf("page1 = %v", ids(page1))
	}
	if cur1 == "" {
		t.Fatal("page1 cursor empty, want more")
	}

	// Page 2 via the cursor: c, b.
	page2, cur2, err := db.ListEventsFiltered(ctx, configdb.EventListParams{Limit: 2, Cursor: cur1})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 || page2[0].CorrelationID != "c" || page2[1].CorrelationID != "b" {
		t.Fatalf("page2 = %v", ids(page2))
	}
	if cur2 == "" {
		t.Fatal("page2 cursor empty, want one more")
	}

	// Page 3: the last row a, and an empty cursor (last page).
	page3, cur3, err := db.ListEventsFiltered(ctx, configdb.EventListParams{Limit: 2, Cursor: cur2})
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3) != 1 || page3[0].CorrelationID != "a" {
		t.Fatalf("page3 = %v", ids(page3))
	}
	if cur3 != "" {
		t.Fatalf("page3 cursor = %q, want empty (last page)", cur3)
	}
}

// TestConfigDB_ListEventsFiltered_FilterAndCap exercises the filter pass-through,
// the time-window bound, the default page size, and the 500 cap.
func TestConfigDB_ListEventsFiltered_FilterAndCap(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	seedEvents(t, db, []configdb.RequestEvent{
		{CorrelationID: "in-1", Configuration: "prod", Model: "gpt-4o", StatusCode: 200, ObservedAt: base.Add(1 * time.Minute)},
		{CorrelationID: "in-2", Configuration: "prod", Model: "gpt-4o", StatusCode: 500, ObservedAt: base.Add(2 * time.Minute)},
		{CorrelationID: "out-cfg", Configuration: "dev", Model: "gpt-4o", StatusCode: 200, ObservedAt: base.Add(3 * time.Minute)},
		{CorrelationID: "out-time", Configuration: "prod", Model: "gpt-4o", StatusCode: 200, ObservedAt: base.Add(1 * time.Hour)},
	})

	got, next, err := db.ListEventsFiltered(ctx, configdb.EventListParams{
		From:   base,
		To:     base.Add(10 * time.Minute),
		Filter: configdb.EventFilter{Configuration: "prod", StatusClass: "5xx"},
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].CorrelationID != "in-2" {
		t.Fatalf("filtered = %v, want only in-2", ids(got))
	}
	if next != "" {
		t.Errorf("next = %q, want empty", next)
	}
}

// TestConfigDB_ListEventsFiltered_InvalidCursor rejects a malformed cursor.
func TestConfigDB_ListEventsFiltered_InvalidCursor(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	if _, _, err := db.ListEventsFiltered(ctx, configdb.EventListParams{Cursor: "!!!not-base64!!!"}); err != configdb.ErrInvalidCursor {
		t.Fatalf("err = %v, want ErrInvalidCursor", err)
	}
}

func ids(events []configdb.RequestEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.CorrelationID
	}
	return out
}
