//go:build e2e

package telemetry_test

import (
	"context"
	"encoding/json"
	"errors"
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
	e := store.RequestEvent{
		CorrelationID: "evt-rt-1",
		GatewayID:     "gw-a",
		Provider:      "anthropic",
		Model:         "claude-x",
		StatusCode:    200,
		TokensIn:      10,
		TokensOut:     20,
		SessionID:     "sess-1",
		GenAIContent:  []byte(`{"input_messages":[]}`),
		Detail:        []byte(`{"tags":["a"]}`),
	}
	if err := st.UpsertRequestEvent(ctx, e); err != nil {
		t.Fatalf("UpsertRequestEvent: %v", err)
	}
	got, err := st.GetRequestEvent(ctx, "evt-rt-1")
	if err != nil {
		t.Fatalf("GetRequestEvent: %v", err)
	}
	if got.Provider != "anthropic" || got.TokensIn != 10 || got.SessionID != "sess-1" {
		t.Errorf("got = %+v", got)
	}
	if got.ObservedAt.IsZero() {
		t.Error("ObservedAt should default to now()")
	}
}

func TestRequestEvent_UpsertPreservesContent(t *testing.T) {
	st := migratedStore(t)
	ctx := context.Background()
	if err := st.UpsertRequestEvent(ctx, store.RequestEvent{CorrelationID: "evt-up", GenAIContent: []byte(`{"x":1}`)}); err != nil {
		t.Fatal(err)
	}
	// A response-phase update omits content; it must be preserved.
	if err := st.UpsertRequestEvent(ctx, store.RequestEvent{CorrelationID: "evt-up", StatusCode: 200}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetRequestEvent(ctx, "evt-up")
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusCode != 200 {
		t.Errorf("status not updated: %d", got.StatusCode)
	}
	if len(got.GenAIContent) == 0 {
		t.Error("gen_ai_content should be preserved across an update that omits it")
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

func TestPayloads_OrderedByCompositeKey(t *testing.T) {
	st := migratedStore(t)
	ctx := context.Background()
	// Insert out of order; expect (ts_ns, instance_id, seq) ordering on read.
	items := []store.Payload{
		{CorrelationID: "p1", Kind: store.KindResponseBody, InstanceID: "b", Seq: 1, TsNs: 200, Body: []byte("z")},
		{CorrelationID: "p1", Kind: store.KindRequestBody, InstanceID: "a", Seq: 1, TsNs: 100, Body: []byte("x")},
		{CorrelationID: "p1", Kind: store.KindSSERollup, InstanceID: "a", Seq: 2, TsNs: 150, Body: []byte("y")},
	}
	for _, it := range items {
		if err := st.UpsertPayload(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.ListPayloads(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d payloads", len(got))
	}
	wantOrder := []int64{100, 150, 200}
	for i, p := range got {
		if p.TsNs != wantOrder[i] {
			t.Errorf("payload[%d].TsNs = %d, want %d", i, p.TsNs, wantOrder[i])
		}
	}
}

func TestPayloads_UpsertIdempotent(t *testing.T) {
	st := migratedStore(t)
	ctx := context.Background()
	p := store.Payload{CorrelationID: "p2", Kind: store.KindRequestBody, InstanceID: "a", Seq: 1, Body: []byte("v1")}
	if err := st.UpsertPayload(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Body = []byte("v2")
	if err := st.UpsertPayload(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := st.ListPayloads(ctx, "p2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0].Body) != "v2" {
		t.Fatalf("expected one upserted row v2, got %+v", got)
	}
}

func TestPayloads_NotFound(t *testing.T) {
	st := migratedStore(t)
	if _, err := st.ListPayloads(context.Background(), "none"); !errors.Is(err, store.ErrPayloadNotFound) {
		t.Fatalf("err = %v, want ErrPayloadNotFound", err)
	}
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
