package main

import (
	"bytes"
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
	f := newReporterFactory(nil, nil, logger, meters, ring, nil, nil, nil, false, testDefaultCaps())
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

func TestReporter_BuildRecordResponseTruncationFlag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		cap       int
		body      []byte
		wantTrunc bool
	}{
		{
			// Complete body within the cap: BodyBytes equals Total, no
			// truncation. The CP must not flag this even if the inline JSON
			// re-encodes to fewer bytes than BodyBytes.
			name:      "within cap",
			cap:       8 * 1024,
			body:      []byte(`{"choices":[{"message":{"content":"hello"}}]}`),
			wantTrunc: false,
		},
		{
			// Body exceeds the per-body cap: Append keeps a head prefix and
			// sets Truncated -> the Record must carry BodyTruncated.
			name:      "over cap",
			cap:       16,
			body:      []byte(`{"choices":[{"message":{"content":"a much longer body than the cap"}}]}`),
			wantTrunc: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTestReporter(t, nil)
			buf := livefeed.NewResponseBuffer(tt.cap)
			buf.Append(tt.body)
			ctx := livefeed.WithResponseBuffer(context.Background(), buf)

			rec := r.buildRecord(ctx, events.Request{StatusCode: 200}, nil)
			if rec.Response.BodyTruncated != tt.wantTrunc {
				t.Errorf("BodyTruncated = %v, want %v (BodyBytes=%d len(Body)=%d)",
					rec.Response.BodyTruncated, tt.wantTrunc, rec.Response.BodyBytes, len(rec.Response.Body))
			}
		})
	}
}

func TestReporter_BuildRecordStreamAssembled(t *testing.T) {
	t.Parallel()
	sse := []byte(
		`data: {"choices":[{"delta":{"content":"Hello"},"index":0}]}` + "\n\n" +
			`data: {"choices":[{"delta":{"content":" world"},"index":0}]}` + "\n\n" +
			"data: [DONE]\n\n")

	t.Run("streamed response populates Assembled, keeps raw Body", func(t *testing.T) {
		r := newTestReporter(t, nil)
		r.streaming = true
		buf := livefeed.NewResponseBuffer(8 * 1024)
		buf.Append(sse)
		ctx := livefeed.WithResponseBuffer(context.Background(), buf)

		ev := events.Request{StatusCode: 200, Streaming: true, Provider: "openai", Endpoint: "chat_completions"}
		rec := r.buildRecord(ctx, ev, nil)

		if !strings.Contains(rec.Response.Assembled, `"content":"Hello world"`) {
			t.Errorf("Assembled = %q, expected assembled chat content", rec.Response.Assembled)
		}
		if rec.Response.AssemblyPartial {
			t.Errorf("AssemblyPartial = true, want false for a clean stream")
		}
		// Raw SSE bytes survive on Body for the "raw stream" tab.
		if !bytes.Contains(rec.Response.Body, []byte("data: ")) {
			t.Errorf("Body = %q, expected to retain raw SSE bytes", rec.Response.Body)
		}
	})

	t.Run("non-streamed response leaves Assembled empty", func(t *testing.T) {
		r := newTestReporter(t, nil)
		buf := livefeed.NewResponseBuffer(8 * 1024)
		buf.Append([]byte(`{"id":"x","object":"chat.completion"}`))
		ctx := livefeed.WithResponseBuffer(context.Background(), buf)

		ev := events.Request{StatusCode: 200, Provider: "openai", Endpoint: "chat_completions"}
		rec := r.buildRecord(ctx, ev, nil)

		if rec.Response.Assembled != "" {
			t.Errorf("Assembled = %q, want empty for non-streamed response", rec.Response.Assembled)
		}
		if rec.Response.AssemblyPartial {
			t.Errorf("AssemblyPartial = true, want false")
		}
	})
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
	// ResponseAssembled is now the JSON-encoded ChatCompletionResponse,
	// not the bare text — assert the assembled message content survived
	// instead of the literal "Hello world".
	if !strings.Contains(env.ResponseAssembled, `"content":"Hello world"`) {
		t.Errorf("ResponseAssembled=%q (expected to contain assembled content)", env.ResponseAssembled)
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
