package main

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/andyjmorgan/sluice-gateway/contracts/events"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// traceHarness wires a reporterRun to an in-memory SpanRecorder so a
// single emitTrace call can be inspected without an exporter.
func traceHarness(t *testing.T) (*reporterRun, *tracetest.SpanRecorder) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	r := &reporterRun{
		factory:       &reporterFactory{tracer: tp.Tracer("test")},
		provider:      "openai",
		endpoint:      "chat_completions",
		model:         "gpt-4o-mini",
		configuration: "dev",
		started:       time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	return r, sr
}

func attrValue(kvs []attribute.KeyValue, key string) (attribute.Value, bool) {
	for _, kv := range kvs {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestEmitTrace_SingleRequestSpan(t *testing.T) {
	r, sr := traceHarness(t)
	r.emitTrace(context.Background(), events.Request{
		Provider:      "openai",
		Endpoint:      "chat_completions",
		Model:         "gpt-4o-mini",
		StatusCode:    200,
		DurationMs:    120,
		CorrelationID: "corr-1",
		TokensIn:      10,
		TokensOut:     5,
	})

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("len(spans) = %d, want 1", len(spans))
	}
	span := spans[0]
	if span.Name() != "chat gpt-4o-mini" {
		t.Errorf("span name = %q, want %q", span.Name(), "chat gpt-4o-mini")
	}
	attrs := span.Attributes()
	checks := map[string]string{
		observability.AttrGenAIOperationName:  observability.OperationChat,
		observability.AttrGenAIProviderName:   "openai",
		observability.AttrGenAIRequestModel:   "gpt-4o-mini",
		observability.AttrSluiceEndpoint:      "chat_completions",
		observability.AttrSluiceConfiguration: "dev",
		observability.AttrSluiceCorrelationID: "corr-1",
	}
	for k, want := range checks {
		got, ok := attrValue(attrs, k)
		if !ok || got.AsString() != want {
			t.Errorf("attr %s = %q (ok=%v), want %q", k, got.AsString(), ok, want)
		}
	}
	if v, ok := attrValue(attrs, observability.AttrGenAIUsageInputTokens); !ok || v.AsInt64() != 10 {
		t.Errorf("input tokens = %d (ok=%v), want 10", v.AsInt64(), ok)
	}
	if v, ok := attrValue(attrs, observability.AttrHTTPResponseStatusCode); !ok || v.AsInt64() != 200 {
		t.Errorf("status = %d (ok=%v), want 200", v.AsInt64(), ok)
	}
	if _, ok := attrValue(attrs, observability.AttrErrorType); ok {
		t.Errorf("error.type should be absent on a 200")
	}
	// Backdated start + duration-derived end.
	if !span.StartTime().Equal(r.started) {
		t.Errorf("start = %v, want %v", span.StartTime(), r.started)
	}
	if want := r.started.Add(120 * time.Millisecond); !span.EndTime().Equal(want) {
		t.Errorf("end = %v, want %v", span.EndTime(), want)
	}
}

func TestEmitTrace_ErrorStatusSetsErrorType(t *testing.T) {
	r, sr := traceHarness(t)
	r.emitTrace(context.Background(), events.Request{
		Provider:   "openai",
		Endpoint:   "chat_completions",
		Model:      "gpt-4o-mini",
		StatusCode: 503,
		DurationMs: 10,
	})
	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("len(spans) = %d, want 1", len(spans))
	}
	if v, ok := attrValue(spans[0].Attributes(), observability.AttrErrorType); !ok || v.AsString() != "503" {
		t.Errorf("error.type = %q (ok=%v), want 503", v.AsString(), ok)
	}
	if spans[0].Status().Code != codes.Error {
		t.Errorf("status code = %v, want Error", spans[0].Status().Code)
	}
}

func TestEmitTrace_AttemptsBecomeChildSpans(t *testing.T) {
	r, sr := traceHarness(t)
	base := r.started
	r.emitTrace(context.Background(), events.Request{
		Provider:   "openai",
		Endpoint:   "chat_completions",
		Model:      "gpt-4o-mini",
		StatusCode: 200,
		DurationMs: 200,
		Attempts: []events.AttemptRecord{
			{Target: "primary", StartedAt: base, DurationMs: 80, StatusCode: 503, Outcome: "failure_status"},
			{Target: "backup", StartedAt: base.Add(80 * time.Millisecond), DurationMs: 100, StatusCode: 200, Outcome: "success"},
		},
	})

	spans := sr.Ended()
	// Two children end before the parent.
	if len(spans) != 3 {
		t.Fatalf("len(spans) = %d, want 3 (parent + 2 attempts)", len(spans))
	}
	var parent sdktrace.ReadOnlySpan
	children := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range spans {
		if v, ok := attrValue(s.Attributes(), observability.AttrSluiceResilienceTarget); ok {
			children[v.AsString()] = s
			continue
		}
		parent = s
	}
	if parent == nil {
		t.Fatal("no parent request span")
	}
	if len(children) != 2 {
		t.Fatalf("children = %d, want 2", len(children))
	}
	// Children parent onto the request span.
	for target, c := range children {
		if c.Parent().SpanID() != parent.SpanContext().SpanID() {
			t.Errorf("child %q not parented to the request span", target)
		}
	}
	if children["primary"].Status().Code != codes.Error {
		t.Errorf("primary attempt should be Error (503)")
	}
	if children["backup"].Status().Code == codes.Error {
		t.Errorf("backup attempt (200) should not be Error")
	}
}

func TestEmitTrace_NoTracerOrNoStartNoOp(t *testing.T) {
	// nil tracer.
	r := &reporterRun{factory: &reporterFactory{}, started: time.Now()}
	r.emitTrace(context.Background(), events.Request{StatusCode: 200}) // must not panic

	// zero start time.
	r2, sr := traceHarness(t)
	r2.started = time.Time{}
	r2.emitTrace(context.Background(), events.Request{StatusCode: 200})
	if got := len(sr.Ended()); got != 0 {
		t.Errorf("spans = %d, want 0 when start time is zero", got)
	}
}
