package rules_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/andyjmorgan/sluice-gateway/internal/middleware/bodycapture"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	openaichat "github.com/andyjmorgan/sluice-gateway/providers/openai/chat"
)

// failMarshalBody implements json.Marshaler with a guaranteed error
// — used to exercise the body re-marshal failure branch without
// reaching for fault injection on the encoder.
type failMarshalBody struct{}

func (failMarshalBody) MarshalJSON() ([]byte, error) {
	return nil, io.ErrUnexpectedEOF
}

func TestBodyRemarshal_NilNextPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil next")
		}
	}()
	rules.BodyRemarshalHandler(nil, nil)
}

func TestBodyRemarshal_NoMutationIsPassthrough(t *testing.T) {
	t.Parallel()

	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		called = true
		if r.ContentLength != 0 {
			t.Errorf("ContentLength = %d, expected 0", r.ContentLength)
		}
	})
	h := rules.BodyRemarshalHandler(nil, next)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Error("next not invoked on passthrough")
	}
}

func TestBodyRemarshal_NoStatePassthrough(t *testing.T) {
	t.Parallel()
	// No MutableState on context — middleware should fall through.
	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { called = true })
	h := rules.BodyRemarshalHandler(nil, next)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Error("next not invoked when state is absent")
	}
}

func TestBodyRemarshal_MutationReencodesBody(t *testing.T) {
	t.Parallel()

	body := &openaichat.ChatCompletionRequest{Model: "gpt-4o"}
	state := rules.NewMutableState("openai", "chat_completions", nil, http.Header{})
	state.BodyMutated = true

	captured := bodycapture.Captured{Kind: bodycapture.KindChat, Body: body}

	ctx := context.Background()
	ctx = bodycapture.WithCaptured(ctx, captured)
	ctx = rules.WithMutableState(ctx, state)

	var sawBytes []byte
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		sawBytes = b
		if r.ContentLength != int64(len(b)) {
			t.Errorf("ContentLength = %d, want %d", r.ContentLength, len(b))
		}
		if got := r.Header.Get("Content-Length"); got == "" {
			t.Error("Content-Length header not set")
		}
	})
	h := rules.BodyRemarshalHandler(nil, next)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	h.ServeHTTP(httptest.NewRecorder(), req)

	var round openaichat.ChatCompletionRequest
	if err := json.Unmarshal(sawBytes, &round); err != nil {
		t.Fatalf("re-marshalled body should round-trip: %v\nbytes: %s", err, sawBytes)
	}
	if round.Model != "gpt-4o" {
		t.Errorf("round.Model = %q, want gpt-4o", round.Model)
	}
}

func TestBodyRemarshal_NilBodyPassthrough(t *testing.T) {
	t.Parallel()

	// BodyMutated=true but Captured.Body=nil — the action's PathParams
	// update did the work; nothing to re-marshal. Middleware should
	// fall through without erroring.
	state := rules.NewMutableState("openai", "chat_completions", nil, http.Header{})
	state.BodyMutated = true
	captured := bodycapture.Captured{Kind: bodycapture.KindPassthrough, Body: nil}

	ctx := context.Background()
	ctx = bodycapture.WithCaptured(ctx, captured)
	ctx = rules.WithMutableState(ctx, state)

	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	h := rules.BodyRemarshalHandler(nil, next)
	req := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Error("next not invoked with nil body")
	}
}

func TestBodyRemarshal_MarshalErrorReturns500(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	meters, err := observability.NewMeters(mp.Meter(observability.MeterName))
	if err != nil {
		t.Fatalf("NewMeters: %v", err)
	}

	state := rules.NewMutableState("openai", "chat_completions", nil, http.Header{})
	state.BodyMutated = true
	captured := bodycapture.Captured{Kind: bodycapture.KindChat, Body: failMarshalBody{}}

	ctx := context.Background()
	ctx = bodycapture.WithCaptured(ctx, captured)
	ctx = rules.WithMutableState(ctx, state)

	nextCalled := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nextCalled = true })
	h := rules.BodyRemarshalHandler(meters, next)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if nextCalled {
		t.Fatal("next should not be invoked on marshal error")
	}
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == observability.MetricRuleErrorsTotal {
				found = true
			}
		}
	}
	if !found {
		t.Error("rule_errors_total not incremented after remarshal failure")
	}
}
