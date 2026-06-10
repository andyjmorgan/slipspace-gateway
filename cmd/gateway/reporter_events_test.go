package main

import (
	"context"
	"strings"
	"testing"
	"time"

	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"

	"github.com/andyjmorgan/sluice-gateway/contracts/events"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/genaiattr"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// recordingEventLogger is an in-memory otellog.Logger for asserting the
// GenAI events emitted at completion. It embeds embedded.Logger to satisfy
// the log API's sealed interface.
type recordingEventLogger struct {
	embedded.Logger
	records []otellog.Record
	lastCtx context.Context
}

func (l *recordingEventLogger) Emit(ctx context.Context, r otellog.Record) {
	l.lastCtx = ctx
	l.records = append(l.records, r)
}

func (l *recordingEventLogger) Enabled(context.Context, otellog.EnabledParameters) bool { return true }

// flattenLogValue concatenates all string leaves of a structured log
// value, so a test can assert content text regardless of the surrounding
// message/parts structure.
func flattenLogValue(v otellog.Value) string {
	switch v.Kind() {
	case otellog.KindString:
		return v.AsString()
	case otellog.KindSlice:
		var b strings.Builder
		for _, e := range v.AsSlice() {
			b.WriteString(flattenLogValue(e))
		}
		return b.String()
	case otellog.KindMap:
		var b strings.Builder
		for _, kv := range v.AsMap() {
			b.WriteString(flattenLogValue(kv.Value))
		}
		return b.String()
	default:
		return ""
	}
}

func eventAttr(r otellog.Record, key string) (otellog.Value, bool) {
	var (
		val   otellog.Value
		found bool
	)
	r.WalkAttributes(func(kv otellog.KeyValue) bool {
		if kv.Key == key {
			val = kv.Value
			found = true
			return false
		}
		return true
	})
	return val, found
}

func TestEmitEvents_ExceptionOnError(t *testing.T) {
	rl := &recordingEventLogger{}
	r := &reporterRun{
		factory:       &reporterFactory{eventLogger: rl},
		provider:      "openai",
		protocol:      "chat_completions",
		configuration: "p",
		started:       time.Now(),
	}
	r.emitEvents(context.Background(), events.Request{
		Provider:   "openai",
		Protocol:   "chat_completions",
		StatusCode: 503,
	})

	if len(rl.records) != 1 {
		t.Fatalf("records = %d, want 1 (exception only, content off)", len(rl.records))
	}
	rec := rl.records[0]
	if rec.EventName() != observability.EventOperationException {
		t.Errorf("event name = %q, want %q", rec.EventName(), observability.EventOperationException)
	}
	if v, ok := eventAttr(rec, observability.AttrExceptionType); !ok || v.AsString() != "503" {
		t.Errorf("exception.type = %q (ok=%v), want 503", v.AsString(), ok)
	}
}

func TestEmitEvents_SuccessContentOffEmitsNothing(t *testing.T) {
	rl := &recordingEventLogger{}
	r := &reporterRun{
		factory:  &reporterFactory{eventLogger: rl},
		protocol: "chat_completions",
		started:  time.Now(),
	}
	r.emitEvents(context.Background(), events.Request{
		Provider:   "openai",
		Protocol:   "chat_completions",
		StatusCode: 200,
	})
	if len(rl.records) != 0 {
		t.Fatalf("records = %d, want 0 (success, content capture off)", len(rl.records))
	}
}

func TestEmitEvents_ContentRedacted(t *testing.T) {
	rl := &recordingEventLogger{}
	r := &reporterRun{
		factory:         &reporterFactory{eventLogger: rl, captureContent: true},
		provider:        "openai",
		protocol:        "chat_completions",
		configuration:   "p",
		started:         time.Now(),
		respOutputParts: []genaiattr.Part{{Type: "text", Content: "your key is sk_live_supersecret1234567890 keep it safe"}},
	}
	r.emitEvents(context.Background(), events.Request{
		Provider:   "openai",
		Protocol:   "chat_completions",
		StatusCode: 200,
	})

	rec := rl.records[0]
	v, ok := eventAttr(rec, observability.AttrGenAIOutputMessages)
	if !ok {
		t.Fatalf("output.messages absent")
	}
	got := flattenLogValue(v)
	if strings.Contains(got, "sk_live_supersecret1234567890") {
		t.Errorf("credential survived redaction in output content: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("expected [REDACTED] mask in output content, got %q", got)
	}
}

func TestEmitEvents_OperationDetailsWhenContentOn(t *testing.T) {
	rl := &recordingEventLogger{}
	r := &reporterRun{
		factory:         &reporterFactory{eventLogger: rl, captureContent: true},
		provider:        "anthropic",
		protocol:        "messages",
		model:           "claude-sonnet-4-6",
		configuration:   "p",
		started:         time.Now(),
		respModel:       "claude-sonnet-4-6",
		respOutputParts: []genaiattr.Part{{Type: "text", Content: "the model reply"}},
	}
	r.emitEvents(context.Background(), events.Request{
		Provider:   "anthropic",
		Protocol:   "messages",
		Model:      "claude-sonnet-4-6",
		StatusCode: 200,
	})

	if len(rl.records) != 1 {
		t.Fatalf("records = %d, want 1 (operation.details, success)", len(rl.records))
	}
	rec := rl.records[0]
	if rec.EventName() != observability.EventInferenceDetails {
		t.Errorf("event name = %q, want %q", rec.EventName(), observability.EventInferenceDetails)
	}
	if v, ok := eventAttr(rec, observability.AttrGenAIProviderName); !ok || v.AsString() != "anthropic" {
		t.Errorf("provider = %q (ok=%v)", v.AsString(), ok)
	}
	v, ok := eventAttr(rec, observability.AttrGenAIOutputMessages)
	if !ok {
		t.Fatalf("output.messages absent")
	}
	// Must be structured (a slice), not a JSON string, per the events spec.
	if v.Kind() != otellog.KindSlice {
		t.Errorf("output.messages kind = %v, want Slice (structured)", v.Kind())
	}
	if got := flattenLogValue(v); !strings.Contains(got, "the model reply") {
		t.Errorf("output.messages content = %q, want it to contain the reply", got)
	}
}

// TestEmitEvents_ServerToolUseAttrs mirrors the span projection on the
// operation-details event: one gen_ai.usage.server_tool_use.<counter>
// attribute per reported non-zero counter.
func TestEmitEvents_ServerToolUseAttrs(t *testing.T) {
	rl := &recordingEventLogger{}
	r := &reporterRun{
		factory:         &reporterFactory{eventLogger: rl, captureContent: true},
		provider:        "anthropic",
		protocol:        "messages",
		configuration:   "p",
		started:         time.Now(),
		serverToolUse:   map[string]int{"web_search_requests": 2, "web_fetch_requests": 0},
		respOutputParts: []genaiattr.Part{{Type: "text", Content: "ok"}},
	}
	r.emitEvents(context.Background(), events.Request{
		Provider:   "anthropic",
		Protocol:   "messages",
		StatusCode: 200,
	})
	if len(rl.records) != 1 {
		t.Fatalf("records = %d, want 1", len(rl.records))
	}
	rec := rl.records[0]
	if v, ok := eventAttr(rec, observability.AttrGenAIUsageServerToolUsePrefix+"web_search_requests"); !ok || v.AsInt64() != 2 {
		t.Errorf("event server_tool_use.web_search_requests = %d (ok=%v), want 2", v.AsInt64(), ok)
	}
	if _, ok := eventAttr(rec, observability.AttrGenAIUsageServerToolUsePrefix+"web_fetch_requests"); ok {
		t.Errorf("zero-valued counter projected on the event")
	}
}
