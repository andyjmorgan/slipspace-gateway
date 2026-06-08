//go:build e2e

package telemetry_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

func TestOpenAndPing(t *testing.T) {
	st := openStore(t)
	if err := st.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestOpen_BadDSN(t *testing.T) {
	if startErr != nil {
		t.Skipf("postgres container unavailable: %v", startErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := store.Open(ctx, "postgres://nope:nope@127.0.0.1:1/none?sslmode=disable"); err == nil {
		t.Fatal("Open with unreachable DSN: want error")
	}
}

func TestMigrate_AppliesAndIsIdempotent(t *testing.T) {
	st := migratedStore(t)
	ctx := context.Background()
	v, err := st.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v < 1 {
		t.Fatalf("schema version = %d, want >= 1", v)
	}
	// Second run is a no-op; version unchanged.
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate (second): %v", err)
	}
	v2, err := st.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion (second): %v", err)
	}
	if v2 != v {
		t.Fatalf("schema version changed on re-run: %d -> %d", v, v2)
	}
}

func TestRequestEvent_RoundTrip(t *testing.T) {
	st := migratedStore(t)
	ctx := context.Background()
	span := []byte(`{"sluice.method":"POST","gen_ai.usage.input_tokens":10,"gen_ai.usage.output_tokens":20,"tags":["a"],"gen_ai_content":{"input_messages":[]}}`)
	e := store.RequestEvent{
		CorrelationID: "evt-rt-1",
		Provider:      "anthropic",
		Model:         "claude-x",
		StatusCode:    200,
		SessionID:     "sess-1",
		AgentID:       "agt-1",
		UserID:        "usr-1",
		SpanEvent:     span,
	}
	if err := st.UpsertRequestEvent(ctx, e); err != nil {
		t.Fatalf("UpsertRequestEvent: %v", err)
	}
	got, err := st.GetRequestEvent(ctx, "evt-rt-1")
	if err != nil {
		t.Fatalf("GetRequestEvent: %v", err)
	}
	if got.Provider != "anthropic" || got.SessionID != "sess-1" {
		t.Errorf("got = %+v", got)
	}
	if got.AgentID != "agt-1" || got.UserID != "usr-1" {
		t.Errorf("identity round-trip = agent %q user %q", got.AgentID, got.UserID)
	}
	// Measurement values live in the blob; decode them out.
	f := got.DecodeSpanFields()
	if f.TokensIn != 10 || f.TokensOut != 20 || f.Method != "POST" {
		t.Errorf("span fields = %+v", f)
	}
	if got.ObservedAt.IsZero() {
		t.Error("ObservedAt should default to now()")
	}
}

// TestRequestEvent_SingleWriterReplace proves the single-writer entity replaces
// the row wholesale on conflict (no cross-feed merge): a second span for the
// same correlation_id overwrites the columns and the blob.
func TestRequestEvent_SingleWriterReplace(t *testing.T) {
	st := migratedStore(t)
	ctx := context.Background()

	if err := st.UpsertRequestEvent(ctx, store.RequestEvent{
		CorrelationID: "evt-up", Provider: "anthropic", StatusCode: 200,
		SpanEvent: []byte(`{"gen_ai.usage.input_tokens":7}`),
	}); err != nil {
		t.Fatal(err)
	}
	// Re-export of the same span (refined) replaces the row.
	if err := st.UpsertRequestEvent(ctx, store.RequestEvent{
		CorrelationID: "evt-up", Provider: "anthropic", Configuration: "dev", StatusCode: 200,
		SpanEvent: []byte(`{"gen_ai.usage.input_tokens":9,"tags":["a"]}`),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetRequestEvent(ctx, "evt-up")
	if err != nil {
		t.Fatal(err)
	}
	if got.Configuration != "dev" || got.StatusCode != 200 {
		t.Errorf("columns not replaced: %+v", got)
	}
	f := got.DecodeSpanFields()
	if f.TokensIn != 9 || len(f.Tags) != 1 || f.Tags[0] != "a" {
		t.Errorf("blob not replaced: %+v", f)
	}
}

// TestRecord_RoundTrip proves the lazy record blob stores + reads back verbatim,
// keyed by correlation_id, idempotent on re-push.
func TestRecord_RoundTrip(t *testing.T) {
	st := migratedStore(t)
	ctx := context.Background()
	body := []byte(`{"correlation_id":"rec-1","provider":"anthropic"}`)
	if err := st.UpsertRecord(ctx, "rec-1", time.Now(), body); err != nil {
		t.Fatalf("UpsertRecord: %v", err)
	}
	got, err := st.GetRecordBody(ctx, "rec-1")
	if err != nil {
		t.Fatalf("GetRecordBody: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("body = %s, want %s", got, body)
	}
	// Re-push overwrites in place.
	body2 := []byte(`{"correlation_id":"rec-1","provider":"openai"}`)
	if err := st.UpsertRecord(ctx, "rec-1", time.Now(), body2); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetRecordBody(ctx, "rec-1")
	if string(got) != string(body2) {
		t.Errorf("re-push body = %s, want %s", got, body2)
	}
	if _, err := st.GetRecordBody(ctx, "absent"); !errors.Is(err, store.ErrRecordNotFound) {
		t.Errorf("absent record = %v, want ErrRecordNotFound", err)
	}
}

func TestRequestEvent_NotFound(t *testing.T) {
	st := migratedStore(t)
	if _, err := st.GetRequestEvent(context.Background(), "nope"); !errors.Is(err, store.ErrRequestEventNotFound) {
		t.Fatalf("err = %v, want ErrRequestEventNotFound", err)
	}
}

func TestRequestEvent_ListRecent(t *testing.T) {
	st := migratedStore(t)
	ctx := context.Background()
	for _, id := range []string{"lr-1", "lr-2", "lr-3"} {
		if err := st.UpsertRequestEvent(ctx, store.RequestEvent{CorrelationID: id, ObservedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.ListRecentRequestEvents(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("limit not honored: got %d", len(got))
	}
}

// TestListSessions exercises the session-discovery query against real Postgres:
// window overlap (a session active in the window appears even if it began
// earlier; one entirely outside is excluded), keyset ordering by last activity,
// the per-session rollup (count / tokens / distinct models / real bounds), and
// the tag + configuration filters applied before aggregation.
//
// migratedStore shares the schema across tests, so the seed uses unique session
// ids and a historical window (2023) that no "now"-stamped event from a sibling
// test can fall into.
func TestListSessions(t *testing.T) {
	st := migratedStore(t)
	ctx := context.Background()

	base := time.Date(2023, 3, 15, 12, 0, 0, 0, time.UTC)
	seed := func(id, sess, cfg, model, tag string, in, out int64, at time.Time) {
		span := fmt.Sprintf(
			`{"gen_ai.usage.input_tokens":%d,"gen_ai.usage.output_tokens":%d,"tags":["%s"]}`,
			in, out, tag)
		if err := st.UpsertRequestEvent(ctx, store.RequestEvent{
			CorrelationID: id, SessionID: sess, Configuration: cfg, Model: model,
			StatusCode: 200, ObservedAt: at, SpanEvent: []byte(span),
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	// Session A: two requests in-window (config prod). Session B: one, more recent
	// (config dev). Session old: one entirely before the window.
	seed("lsq-a1", "lsq-A", "lsq-prod", "lsq-m1", "lsq-x", 100, 10, base.Add(-2*time.Hour))
	seed("lsq-a2", "lsq-A", "lsq-prod", "lsq-m1", "lsq-x", 200, 20, base.Add(-1*time.Hour))
	seed("lsq-b1", "lsq-B", "lsq-dev", "lsq-m2", "lsq-y", 50, 5, base.Add(-30*time.Minute))
	seed("lsq-o1", "lsq-old", "lsq-prod", "lsq-m3", "lsq-z", 1, 1, base.Add(-10*24*time.Hour))

	from, to := base.Add(-3*time.Hour), base

	// pick returns the summary for a session id from a result page, or fails.
	pick := func(t *testing.T, list []store.SessionSummary, id string) store.SessionSummary {
		t.Helper()
		for _, s := range list {
			if s.SessionID == id {
				return s
			}
		}
		t.Fatalf("session %q not in result %+v", id, list)
		return store.SessionSummary{}
	}

	t.Run("window overlap + rollup + order", func(t *testing.T) {
		got, _, err := st.ListSessions(ctx, store.SessionListParams{From: from, To: to})
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		a := pick(t, got, "lsq-A")
		b := pick(t, got, "lsq-B")
		for _, s := range got {
			if s.SessionID == "lsq-old" {
				t.Fatalf("out-of-window session returned: %+v", got)
			}
		}
		if a.Messages != 2 || a.TotalTokens != 330 {
			t.Errorf("A rollup = %+v, want messages 2 tokens 330", a)
		}
		if len(a.Models) != 1 || a.Models[0] != "lsq-m1" {
			t.Errorf("A models = %v, want [lsq-m1]", a.Models)
		}
		if !a.Started.Equal(base.Add(-2*time.Hour)) || !a.LastAt.Equal(base.Add(-1*time.Hour)) {
			t.Errorf("A bounds = %v..%v", a.Started, a.LastAt)
		}
		// Ordered by last activity desc: B (-30m) precedes A (-1h).
		var ai, bi = -1, -1
		for i, s := range got {
			if s.SessionID == "lsq-A" {
				ai = i
			}
			if s.SessionID == "lsq-B" {
				bi = i
			}
		}
		if bi > ai {
			t.Errorf("order: B(%d) should precede A(%d): %+v", bi, ai, got)
		}
		_ = b
	})

	t.Run("configuration filter excludes non-matching + out-of-window", func(t *testing.T) {
		got, _, err := st.ListSessions(ctx, store.SessionListParams{
			From: from, To: to, Filter: store.EventFilter{Configuration: "lsq-prod"},
		})
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		// Only A matches config prod within the window (old is prod but out of window).
		pick(t, got, "lsq-A")
		for _, s := range got {
			if s.SessionID == "lsq-B" || s.SessionID == "lsq-old" {
				t.Fatalf("config filter leaked %q: %+v", s.SessionID, got)
			}
		}
	})

	t.Run("tag filter", func(t *testing.T) {
		got, _, err := st.ListSessions(ctx, store.SessionListParams{
			From: from, To: to, Filter: store.EventFilter{Tags: []string{"lsq-x"}},
		})
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		pick(t, got, "lsq-A")
		for _, s := range got {
			if s.SessionID == "lsq-B" {
				t.Fatalf("tag filter leaked B: %+v", got)
			}
		}
	})

	t.Run("keyset paging", func(t *testing.T) {
		// Limit 1 over the two in-window sessions yields a cursor; the next page
		// returns the other session, and no session repeats.
		p1, cur, err := st.ListSessions(ctx, store.SessionListParams{From: from, To: to, Limit: 1})
		if err != nil {
			t.Fatalf("page 1: %v", err)
		}
		if len(p1) != 1 || cur == "" {
			t.Fatalf("page 1 = %d rows, cursor %q", len(p1), cur)
		}
		p2, _, err := st.ListSessions(ctx, store.SessionListParams{From: from, To: to, Limit: 1, Cursor: cur})
		if err != nil {
			t.Fatalf("page 2: %v", err)
		}
		if len(p2) != 1 || p2[0].SessionID == p1[0].SessionID {
			t.Fatalf("page 2 = %+v (page 1 was %+v)", p2, p1)
		}
	})
}

func TestMetricPoints_InsertAndList(t *testing.T) {
	st := migratedStore(t)
	ctx := context.Background()
	if err := st.InsertMetricPoints(ctx, nil); err != nil {
		t.Fatalf("empty insert: %v", err)
	}
	labels, _ := json.Marshal(map[string]string{"provider": "anthropic"})
	pts := []store.MetricPoint{
		{Name: "sluice.requests", Labels: labels, Value: 1, ObservedAt: time.Now()},
		{Name: "sluice.tokens", Value: 42, ObservedAt: time.Now()}, // nil labels -> {}
	}
	if err := st.InsertMetricPoints(ctx, pts); err != nil {
		t.Fatalf("InsertMetricPoints: %v", err)
	}
	got, err := st.ListRecentMetricPoints(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 {
		t.Fatalf("got %d points, want >= 2", len(got))
	}
}
