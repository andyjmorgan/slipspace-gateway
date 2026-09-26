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

	contractsrules "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/httperr"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/bodycapture"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
)

// failingReader returns an error on every Read, driving the body-rewrite
// read-failure branch.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// errorWriterHarness builds an instrumented httperr.Writer over a manual
// reader so a test can assert the gateway error counter fired with the
// expected layer/code labels (issue #554).
type errorWriterHarness struct {
	writer *httperr.Writer
	reader *sdkmetric.ManualReader
}

func newErrorWriterHarness(t *testing.T) errorWriterHarness {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	meters, err := observability.NewMeters(mp.Meter(observability.MeterName))
	if err != nil {
		t.Fatalf("NewMeters: %v", err)
	}
	return errorWriterHarness{writer: httperr.New(meters.ErrorResponsesTotal, nil), reader: reader}
}

// errorCount returns the summed gateway error counter for the given
// layer/code labels.
func (h errorWriterHarness) errorCount(t *testing.T, layer, code string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := h.reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != observability.MetricErrorResponsesTotal {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				gotLayer, _ := dp.Attributes.Value("layer")
				gotCode, _ := dp.Attributes.Value("code")
				if gotLayer.AsString() == layer && gotCode.AsString() == code {
					total += dp.Value
				}
			}
		}
	}
	return total
}

// assertJSONError checks the documented httperr body shape
// (docs/pipeline.md, "Error responses") on a rejection.
func assertJSONError(t *testing.T, rec *httptest.ResponseRecorder, wantCode string) {
	const wantStatus = http.StatusInternalServerError
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, wantStatus, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json (body=%s)", ct, rec.Body.String())
	}
	var body httperr.Body
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not the httperr JSON shape: %v (%s)", err, rec.Body.String())
	}
	if body.Error != wantCode {
		t.Fatalf("error code = %q, want %q", body.Error, wantCode)
	}
}

func TestHTTPHandler_NoRoute_WritesJSONErrorAndCounts(t *testing.T) {
	t.Parallel()
	hw := newErrorWriterHarness(t)
	e := rules.NewEvaluator(nil, 8, nil)
	noMatch := func(context.Context) (string, string, string, map[string]string, bool) {
		return "", "", "", nil, false
	}
	h := rules.HTTPHandler(e, noMatch, nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next must not run")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(httperr.WithWriter(req.Context(), hw.writer))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, "internal")
	if got := hw.errorCount(t, "rules", "internal"); got != 1 {
		t.Fatalf("error counter rules/internal = %d, want 1", got)
	}
}

func TestHTTPHandler_NoStateNoMatchFrom_WritesJSONError(t *testing.T) {
	t.Parallel()
	e := rules.NewEvaluator(nil, 8, nil)
	h := rules.HTTPHandler(e, nil, nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next must not run")
	}))
	rec := httptest.NewRecorder()
	// No writer on context: the passive default must still emit JSON.
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assertJSONError(t, rec, "internal")
}

func TestBodyRemarshal_MarshalError_WritesJSONErrorAndCounts(t *testing.T) {
	t.Parallel()
	hw := newErrorWriterHarness(t)
	state := rules.NewMutableState("openai", "chat", "", nil, http.Header{})
	state.BodyMutated = true
	ctx := bodycapture.WithCaptured(context.Background(), bodycapture.Captured{Kind: bodycapture.KindChat, Body: failMarshalBody{}})
	ctx = rules.WithMutableState(ctx, state)
	ctx = httperr.WithWriter(ctx, hw.writer)

	h := rules.BodyRemarshalHandler(nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next must not run")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx))

	assertJSONError(t, rec, "body_remarshal_failed")
	if got := hw.errorCount(t, "rules", "body_remarshal_failed"); got != 1 {
		t.Fatalf("error counter rules/body_remarshal_failed = %d, want 1", got)
	}
}

func TestBodyRewrite_ReadError_WritesJSONErrorAndCounts(t *testing.T) {
	t.Parallel()
	hw := newErrorWriterHarness(t)
	state := rules.NewMutableState("openai", "chat", "", nil, http.Header{})
	// Any queued request-phase op is enough to reach the body read.
	if _, err := rules.ApplyAction(&contractsrules.RemoveFieldAction{Type: "removeField", Target: "request.body.metadata"}, state, nil); err != nil {
		t.Fatalf("queue op: %v", err)
	}
	ctx := rules.WithMutableState(context.Background(), state)
	ctx = httperr.WithWriter(ctx, hw.writer)

	h := rules.BodyRewriteHandler(nil, "", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next must not run")
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", io.NopCloser(failingReader{})).WithContext(ctx)
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, "body_rewrite_failed")
	if got := hw.errorCount(t, "rules", "body_rewrite_failed"); got != 1 {
		t.Fatalf("error counter rules/body_rewrite_failed = %d, want 1", got)
	}
}
