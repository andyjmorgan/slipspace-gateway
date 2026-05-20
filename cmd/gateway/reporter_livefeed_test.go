package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/andyjmorgan/sluice-gateway/contracts/events"
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
	f := newReporterFactory(nil, logger, meters, ring)
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
