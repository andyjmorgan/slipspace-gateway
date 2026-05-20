package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/andyjmorgan/sluice-gateway/contracts/events"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/bodycapture"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/observability/livefeed"
)

// newTestReporter builds a reporterRun against a fresh ring and meter
// set, with the (provider, endpoint, model, configuration) labels
// pre-populated so OnComplete has something concrete to publish.
func newTestReporter(t *testing.T, ring *livefeed.Ring) *reporterRun {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	meters, err := observability.NewMeters(noop.NewMeterProvider().Meter("test"))
	if err != nil {
		t.Fatalf("NewMeters: %v", err)
	}
	f := newReporterFactory(nil, logger, meters, ring, nil)
	return &reporterRun{
		factory:       f,
		provider:      "openai",
		endpoint:      "chat_completions",
		model:         "gpt-4o-mini",
		configuration: "production",
	}
}

func TestReporter_AppendsLiveFeedEntryOnComplete(t *testing.T) {
	t.Parallel()
	ring, err := livefeed.NewRing(8)
	if err != nil {
		t.Fatal(err)
	}
	r := newTestReporter(t, ring)

	ctx := observability.WithCorrelationID(context.Background(), "corr-123")
	r.OnComplete(ctx, 200, 42)

	got := ring.Recent(0)
	if len(got) != 1 {
		t.Fatalf("ring len=%d want 1", len(got))
	}
	e := got[0]
	if e.CorrelationID != "corr-123" {
		t.Errorf("CorrelationID = %q want corr-123", e.CorrelationID)
	}
	if e.Provider != "openai" || e.Endpoint != "chat_completions" {
		t.Errorf("provider/endpoint = %q/%q", e.Provider, e.Endpoint)
	}
	if e.Model != "gpt-4o-mini" {
		t.Errorf("Model = %q", e.Model)
	}
	if e.Configuration != "production" {
		t.Errorf("Configuration = %q", e.Configuration)
	}
	if e.StatusCode != 200 || e.DurationMs != 42 {
		t.Errorf("status/duration = %d/%d", e.StatusCode, e.DurationMs)
	}
	if e.EventID == "" {
		t.Errorf("EventID empty")
	}
	if e.At.IsZero() {
		t.Errorf("At not set")
	}
}

func TestReporter_AppendsRuleHitsFromContextBuffer(t *testing.T) {
	t.Parallel()
	ring, _ := livefeed.NewRing(4)
	r := newTestReporter(t, ring)

	ctx, buf := rules.WithMatchBuffer(context.Background())
	buf.Record(events.RuleMatched{
		RuleName:       "claude-haiku-redirect",
		ActionsApplied: []string{"changeProvider"},
	})
	buf.Record(events.RuleMatched{
		RuleName:       "block-banned-model",
		ActionsApplied: []string{"returnStatusCode"},
		Terminated:     true,
	})
	ctx = observability.WithCorrelationID(ctx, "corr-rules")

	r.OnComplete(ctx, 200, 10)

	got := ring.Recent(0)
	if len(got) != 1 {
		t.Fatalf("ring len=%d want 1", len(got))
	}
	hits := got[0].RulesMatched
	if len(hits) != 2 {
		t.Fatalf("hits=%d want 2: %+v", len(hits), hits)
	}
	if hits[0].RuleName != "claude-haiku-redirect" || hits[0].ActionsApplied[0] != "changeProvider" {
		t.Errorf("hit[0] = %+v", hits[0])
	}
	if !hits[1].Terminated {
		t.Errorf("hit[1] not Terminated: %+v", hits[1])
	}
}

func TestReporter_LiveFeedNilIsNoop(t *testing.T) {
	t.Parallel()
	r := newTestReporter(t, nil) // nil ring
	r.OnComplete(observability.WithCorrelationID(context.Background(), "x"), 200, 1)
	// No panic, no assertion beyond completing — the nil ring path is
	// the disabled-feature codepath the production wiring relies on.
}

func TestReporter_CapturesRequestAndResponseBodies(t *testing.T) {
	t.Parallel()
	ring, _ := livefeed.NewRing(4)
	store, err := livefeed.NewBodyStore(64 * 1024)
	if err != nil {
		t.Fatal(err)
	}
	r := newTestReporter(t, ring)
	r.factory.bodyStore = store

	requestBody := []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`)
	ctx := bodycapture.WithCaptured(context.Background(), bodycapture.Captured{
		Kind: bodycapture.KindChat,
		Raw:  requestBody,
	})
	buf := livefeed.NewResponseBuffer(8 * 1024)
	buf.Append([]byte(`{"choices":[{"message":{"content":"hello"}}]}`))
	ctx = livefeed.WithResponseBuffer(ctx, buf)
	ctx = observability.WithCorrelationID(ctx, "corr-body")

	r.OnComplete(ctx, 200, 5)

	entries := ring.Recent(0)
	if len(entries) != 1 {
		t.Fatalf("ring len=%d want 1", len(entries))
	}
	env, ok := store.Get(entries[0].EventID)
	if !ok {
		t.Fatalf("body store missing entry for %q", entries[0].EventID)
	}
	if string(env.Request) != string(requestBody) {
		t.Errorf("Request=%q", env.Request)
	}
	if !strings.Contains(string(env.Response), `"content":"hello"`) {
		t.Errorf("Response=%q", env.Response)
	}
	if env.ResponseAssembled != "" {
		t.Errorf("non-streaming response should not run the accumulator; ResponseAssembled=%q", env.ResponseAssembled)
	}
}

func TestReporter_StreamingResponseAccumulated(t *testing.T) {
	t.Parallel()
	ring, _ := livefeed.NewRing(4)
	store, _ := livefeed.NewBodyStore(64 * 1024)
	r := newTestReporter(t, ring)
	r.factory.bodyStore = store
	// Endpoint maps to OpenAI chat accumulator.
	r.endpoint = "chat_completions"

	buf := livefeed.NewResponseBuffer(8 * 1024)
	buf.Append([]byte(
		`data: {"choices":[{"delta":{"content":"Hello"},"index":0}]}` + "\n\n" +
			`data: {"choices":[{"delta":{"content":" world"},"index":0}]}` + "\n\n" +
			"data: [DONE]\n\n"))
	ctx := livefeed.WithResponseBuffer(context.Background(), buf)
	ctx = observability.WithCorrelationID(ctx, "corr-stream")

	// Mark response as streaming so the reporter dispatches accumulator.
	r.streaming = true
	r.statusCode = 200

	r.OnComplete(ctx, 200, 10)

	entries := ring.Recent(0)
	if len(entries) != 1 {
		t.Fatalf("ring len=%d", len(entries))
	}
	env, ok := store.Get(entries[0].EventID)
	if !ok {
		t.Fatal("body store missing entry")
	}
	if env.ResponseAssembled != "Hello world" {
		t.Errorf("ResponseAssembled=%q want 'Hello world'", env.ResponseAssembled)
	}
}

func TestReporter_BodyStoreNilIsNoop(t *testing.T) {
	t.Parallel()
	ring, _ := livefeed.NewRing(4)
	r := newTestReporter(t, ring)
	// bodyStore stays nil; appending a body buffer to ctx should not
	// trigger a write or panic.
	buf := livefeed.NewResponseBuffer(64)
	buf.Append([]byte("x"))
	ctx := livefeed.WithResponseBuffer(context.Background(), buf)
	ctx = observability.WithCorrelationID(ctx, "corr-nobody")
	r.OnComplete(ctx, 200, 1)
	// Pass when OnComplete doesn't panic and the ring entry exists.
	if len(ring.Recent(0)) != 1 {
		t.Fatal("ring missing entry")
	}
}
