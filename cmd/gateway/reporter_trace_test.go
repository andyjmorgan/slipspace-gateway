package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/andyjmorgan/sluice-gateway/contracts/events"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/bodycapture"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/genaiattr"
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
	}, nil)

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
	}, nil)
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
	}, nil)

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

func TestEmitTrace_ConversationID(t *testing.T) {
	r, sr := traceHarness(t)
	r.sessionID = "bundle-1"
	r.emitTrace(context.Background(), events.Request{
		Provider:   "openai",
		Endpoint:   "chat_completions",
		Model:      "gpt-4o-mini",
		StatusCode: 200,
		DurationMs: 10,
	}, nil)
	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("len(spans) = %d, want 1", len(spans))
	}
	if v, ok := attrValue(spans[0].Attributes(), observability.AttrGenAIConversationID); !ok || v.AsString() != "bundle-1" {
		t.Errorf("gen_ai.conversation.id = %q (ok=%v), want bundle-1", v.AsString(), ok)
	}
}

func TestEmitTrace_NoConversationIDWhenNoSession(t *testing.T) {
	r, sr := traceHarness(t)
	r.emitTrace(context.Background(), events.Request{Endpoint: "chat_completions", StatusCode: 200, DurationMs: 1}, nil)
	if _, ok := attrValue(sr.Ended()[0].Attributes(), observability.AttrGenAIConversationID); ok {
		t.Errorf("gen_ai.conversation.id should be absent when no session resolved")
	}
}

func TestEmitTrace_SluiceFleetAttrs(t *testing.T) {
	r, sr := traceHarness(t)
	r.apiKeyName = "internal-svc"
	r.sessionIDSource = "X-Agentling-Task-Id"
	r.statusCode = 502 // upstream-reported, distinct from the client status below
	matches := []events.RuleMatched{
		{RuleName: "redirect-claude"},
		{RuleName: "add-team-tag"},
	}
	r.emitTrace(context.Background(), events.Request{
		Provider:   "openai",
		Endpoint:   "chat_completions",
		Model:      "gpt-4o-mini",
		Method:     "POST",
		StatusCode: 200,
		DurationMs: 10,
		Tags:       []string{"team:platform", "env:prod"},
		PolicyRef:  "failover-pool",
	}, matches)

	attrs := sr.Ended()[0].Attributes()
	str := map[string]string{
		observability.AttrSluiceTags:            "team:platform,env:prod",
		observability.AttrSluiceRulesFired:      "redirect-claude,add-team-tag",
		observability.AttrSluicePolicyRef:       "failover-pool",
		observability.AttrSluiceAPIKeyName:      "internal-svc",
		observability.AttrSluiceSessionIDSource: "X-Agentling-Task-Id",
		observability.AttrSluiceMethod:          "POST",
	}
	for k, want := range str {
		got, ok := attrValue(attrs, k)
		if !ok || got.AsString() != want {
			t.Errorf("attr %s = %q (ok=%v), want %q", k, got.AsString(), ok, want)
		}
	}
	if v, ok := attrValue(attrs, observability.AttrSluiceUpstreamStatus); !ok || v.AsInt64() != 502 {
		t.Errorf("sluice.upstream_status = %d (ok=%v), want 502", v.AsInt64(), ok)
	}
}

func TestEmitTrace_SluiceFleetAttrsOmittedWhenEmpty(t *testing.T) {
	r, sr := traceHarness(t)
	r.emitTrace(context.Background(), events.Request{
		Provider:   "openai",
		Endpoint:   "chat_completions",
		Model:      "gpt-4o-mini",
		StatusCode: 200,
		DurationMs: 1,
	}, nil)

	attrs := sr.Ended()[0].Attributes()
	for _, k := range []string{
		observability.AttrSluiceTags,
		observability.AttrSluiceRulesFired,
		observability.AttrSluicePolicyRef,
		observability.AttrSluiceAPIKeyName,
		observability.AttrSluiceUpstreamStatus,
		observability.AttrSluiceSessionIDSource,
		observability.AttrSluiceMethod,
	} {
		if _, ok := attrValue(attrs, k); ok {
			t.Errorf("attr %s should be absent on a lean single-shot request", k)
		}
	}
}

func TestEmitTrace_ClientConformanceAttrs(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reasoning := 33
	r := &reporterRun{
		factory:           &reporterFactory{tracer: tp.Tracer("test")},
		provider:          "gemini",
		endpoint:          "generate_content",
		model:             "gemini-2.0-flash",
		configuration:     "prod",
		serverAddress:     "generativelanguage.googleapis.com",
		serverPort:        443,
		streaming:         true,
		started:           start,
		firstByte:         start.Add(50 * time.Millisecond),
		respID:            "resp-xyz",
		respModel:         "gemini-2.0-flash-001",
		respFinishReasons: []string{"STOP"},
		respReasoning:     &reasoning,
	}
	r.emitTrace(context.Background(), events.Request{
		Provider:            "gemini",
		Endpoint:            "generate_content",
		Model:               "gemini-2.0-flash",
		StatusCode:          200,
		DurationMs:          300,
		Streaming:           true,
		TokensIn:            120,
		TokensOut:           40,
		TokensCached:        2000,
		TokensCacheCreation: 50,
	}, nil)

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("len(spans) = %d, want 1", len(spans))
	}
	attrs := spans[0].Attributes()

	// gemini maps to the gcp.gemini spec enum value.
	if v, ok := attrValue(attrs, observability.AttrGenAIProviderName); !ok || v.AsString() != "gcp.gemini" {
		t.Errorf("gen_ai.provider.name = %q (ok=%v), want gcp.gemini", v.AsString(), ok)
	}
	// generate_content is its own operation, not collapsed to chat.
	if v, ok := attrValue(attrs, observability.AttrGenAIOperationName); !ok || v.AsString() != observability.OperationGenerateContent {
		t.Errorf("gen_ai.operation.name = %q, want generate_content", v.AsString())
	}
	if spans[0].Name() != "generate_content gemini-2.0-flash" {
		t.Errorf("span name = %q, want %q", spans[0].Name(), "generate_content gemini-2.0-flash")
	}
	if v, ok := attrValue(attrs, observability.AttrServerAddress); !ok || v.AsString() != "generativelanguage.googleapis.com" {
		t.Errorf("server.address = %q (ok=%v)", v.AsString(), ok)
	}
	if v, ok := attrValue(attrs, observability.AttrServerPort); !ok || v.AsInt64() != 443 {
		t.Errorf("server.port = %d (ok=%v), want 443", v.AsInt64(), ok)
	}
	if v, ok := attrValue(attrs, observability.AttrGenAIRequestStream); !ok || !v.AsBool() {
		t.Errorf("gen_ai.request.stream = %v (ok=%v), want true", v.AsBool(), ok)
	}
	if v, ok := attrValue(attrs, observability.AttrGenAIResponseTimeToFirstChunk); !ok || v.AsFloat64() <= 0 {
		t.Errorf("gen_ai.response.time_to_first_chunk = %v (ok=%v), want >0", v.AsFloat64(), ok)
	}
	if v, ok := attrValue(attrs, observability.AttrGenAIUsageCacheReadInputTokens); !ok || v.AsInt64() != 2000 {
		t.Errorf("gen_ai.usage.cache_read.input_tokens = %d (ok=%v), want 2000", v.AsInt64(), ok)
	}
	if v, ok := attrValue(attrs, observability.AttrGenAIUsageCacheCreationInputTokens); !ok || v.AsInt64() != 50 {
		t.Errorf("gen_ai.usage.cache_creation.input_tokens = %d (ok=%v), want 50", v.AsInt64(), ok)
	}
	if v, ok := attrValue(attrs, observability.AttrGenAIResponseID); !ok || v.AsString() != "resp-xyz" {
		t.Errorf("gen_ai.response.id = %q (ok=%v), want resp-xyz", v.AsString(), ok)
	}
	if v, ok := attrValue(attrs, observability.AttrGenAIResponseModel); !ok || v.AsString() != "gemini-2.0-flash-001" {
		t.Errorf("gen_ai.response.model = %q (ok=%v)", v.AsString(), ok)
	}
	if v, ok := attrValue(attrs, observability.AttrGenAIResponseFinishReasons); !ok || len(v.AsStringSlice()) != 1 || v.AsStringSlice()[0] != "STOP" {
		t.Errorf("gen_ai.response.finish_reasons = %v (ok=%v), want [STOP]", v.AsStringSlice(), ok)
	}
	if v, ok := attrValue(attrs, observability.AttrGenAIUsageReasoningOutputTokens); !ok || v.AsInt64() != 33 {
		t.Errorf("gen_ai.usage.reasoning.output_tokens = %d (ok=%v), want 33", v.AsInt64(), ok)
	}
}

func TestEmitTrace_OpenAIProviderAttrs(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := &reporterRun{
		factory:               &reporterFactory{tracer: tp.Tracer("test")},
		provider:              "openai",
		endpoint:              "chat",
		model:                 "gpt-4o",
		configuration:         "prod",
		started:               start,
		respServiceTier:       "default",
		respSystemFingerprint: "fp_44709d6fcb",
	}
	r.emitTrace(context.Background(), events.Request{
		Provider:   "openai",
		Endpoint:   "chat",
		Model:      "gpt-4o",
		StatusCode: 200,
		DurationMs: 50,
	}, nil)

	attrs := sr.Ended()[0].Attributes()
	if v, ok := attrValue(attrs, observability.AttrOpenAIResponseServiceTier); !ok || v.AsString() != "default" {
		t.Errorf("openai.response.service_tier = %q (ok=%v)", v.AsString(), ok)
	}
	if v, ok := attrValue(attrs, observability.AttrOpenAIResponseSystemFingerprint); !ok || v.AsString() != "fp_44709d6fcb" {
		t.Errorf("openai.response.system_fingerprint = %q (ok=%v)", v.AsString(), ok)
	}
	if v, ok := attrValue(attrs, observability.AttrOpenAIAPIType); !ok || v.AsString() != "chat_completions" {
		t.Errorf("openai.api.type = %q (ok=%v), want chat_completions", v.AsString(), ok)
	}
	// openai provider passes through unmapped.
	if v, ok := attrValue(attrs, observability.AttrGenAIProviderName); !ok || v.AsString() != "openai" {
		t.Errorf("gen_ai.provider.name = %q, want openai", v.AsString())
	}
}

func TestEmitTrace_ContentOnSpan(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := &reporterRun{
		factory:         &reporterFactory{tracer: tp.Tracer("test"), captureContent: true},
		provider:        "openai",
		endpoint:        "chat_completions",
		model:           "gpt-4o",
		configuration:   "p",
		started:         start,
		respOutputParts: []genaiattr.Part{{Type: "text", Content: "the answer"}},
	}
	ctx := bodycapture.WithCaptured(context.Background(), bodycapture.Captured{
		Raw: []byte(`{"messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hi there"}],"tools":[{"type":"function"}]}`),
	})
	r.emitTrace(ctx, events.Request{
		Provider:   "openai",
		Endpoint:   "chat_completions",
		Model:      "gpt-4o",
		StatusCode: 200,
		DurationMs: 10,
	}, nil)

	attrs := sr.Ended()[0].Attributes()
	// On a span, content rides as JSON-string attributes. system_instructions
	// is a parts array; input/output messages are [{role,parts}].
	if v, ok := attrValue(attrs, observability.AttrGenAISystemInstructions); !ok || !strings.Contains(v.AsString(), `"content":"be brief"`) {
		t.Errorf("system_instructions on span = %q (ok=%v)", v.AsString(), ok)
	}
	if v, ok := attrValue(attrs, observability.AttrGenAIInputMessages); !ok || !strings.Contains(v.AsString(), "hi there") {
		t.Errorf("input.messages on span = %q (ok=%v)", v.AsString(), ok)
	}
	if v, ok := attrValue(attrs, observability.AttrGenAIOutputMessages); !ok || !strings.Contains(v.AsString(), "the answer") {
		t.Errorf("output.messages on span = %q (ok=%v)", v.AsString(), ok)
	}
	if _, ok := attrValue(attrs, observability.AttrGenAIToolDefinitions); !ok {
		t.Errorf("tool.definitions absent on span")
	}
}

func TestEmitTrace_SpanContextReachesEvents(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	rl := &recordingEventLogger{}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := &reporterRun{
		factory:       &reporterFactory{tracer: tp.Tracer("t"), eventLogger: rl, captureContent: true},
		provider:      "openai",
		endpoint:      "chat_completions",
		model:         "gpt-4o",
		configuration: "p",
		started:       start,
	}
	ev := events.Request{Provider: "openai", Endpoint: "chat_completions", Model: "gpt-4o", StatusCode: 200, DurationMs: 5}
	traceCtx := r.emitTrace(context.Background(), ev, nil)
	r.emitEvents(traceCtx, ev)

	if sc := trace.SpanContextFromContext(traceCtx); !sc.IsValid() {
		t.Fatal("emitTrace returned a ctx with no span context")
	}
	if len(rl.records) == 0 {
		t.Fatal("no event emitted")
	}
	// The log record's ctx carries the same trace as the recorded span, so a
	// backend correlates the event to the span natively.
	logTID := trace.SpanContextFromContext(rl.lastCtx).TraceID()
	if logTID != sr.Ended()[0].SpanContext().TraceID() {
		t.Errorf("event trace id %v != span trace id %v", logTID, sr.Ended()[0].SpanContext().TraceID())
	}
}

func TestEmitTrace_NilTracerReturnsOriginalCtx(t *testing.T) {
	r := &reporterRun{factory: &reporterFactory{}, started: time.Now()}
	ctx := context.Background()
	if got := r.emitTrace(ctx, events.Request{}, nil); got != ctx {
		t.Error("nil tracer should return the original ctx unchanged")
	}
}

func TestEmitTrace_NonStreamingOmitsStreamAttrs(t *testing.T) {
	r, sr := traceHarness(t) // openai, non-streaming, no upstream host captured
	r.emitTrace(context.Background(), events.Request{
		Provider:   "openai",
		Endpoint:   "chat_completions",
		Model:      "gpt-4o-mini",
		StatusCode: 200,
		DurationMs: 10,
	}, nil)
	attrs := sr.Ended()[0].Attributes()
	if _, ok := attrValue(attrs, observability.AttrGenAIRequestStream); ok {
		t.Error("gen_ai.request.stream should be absent on a non-streaming request")
	}
	if _, ok := attrValue(attrs, observability.AttrGenAIResponseTimeToFirstChunk); ok {
		t.Error("gen_ai.response.time_to_first_chunk should be absent on a non-streaming request")
	}
	if _, ok := attrValue(attrs, observability.AttrServerAddress); ok {
		t.Error("server.address should be absent when no upstream host was captured")
	}
}

func TestEmitTrace_NoTracerOrNoStartNoOp(t *testing.T) {
	// nil tracer.
	r := &reporterRun{factory: &reporterFactory{}, started: time.Now()}
	r.emitTrace(context.Background(), events.Request{StatusCode: 200}, nil) // must not panic

	// zero start time.
	r2, sr := traceHarness(t)
	r2.started = time.Time{}
	r2.emitTrace(context.Background(), events.Request{StatusCode: 200}, nil)
	if got := len(sr.Ended()); got != 0 {
		t.Errorf("spans = %d, want 0 when start time is zero", got)
	}
}
