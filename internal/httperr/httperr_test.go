package httperr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestWriter_WriteHappyPath(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	counter, err := mp.Meter("test").Int64Counter("gateway.error_responses.total")
	if err != nil {
		t.Fatalf("counter: %v", err)
	}

	wr := New(counter, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	rec := httptest.NewRecorder()

	wr.Write(context.Background(), rec, 404, "routing", "no_route", "not found")

	if got := rec.Code; got != 404 {
		t.Errorf("status = %d, want 404", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	var body Body
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body.Error != "no_route" {
		t.Errorf("body.Error = %q, want no_route", body.Error)
	}
	if body.Message != "not found" {
		t.Errorf("body.Message = %q, want %q", body.Message, "not found")
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	if !counterIncremented(rm, "gateway.error_responses.total") {
		t.Errorf("expected counter increment for gateway.error_responses.total")
	}
}

func TestWriter_OmitsMessageWhenEmpty(t *testing.T) {
	t.Parallel()

	wr := New(nil, nil)
	rec := httptest.NewRecorder()

	wr.Write(context.Background(), rec, 500, "handler", "internal", "")

	if bytes.Contains(rec.Body.Bytes(), []byte(`"message"`)) {
		t.Errorf("empty message should be omitted; body = %s", rec.Body.String())
	}
}

func TestWriter_NilCounter(t *testing.T) {
	t.Parallel()

	wr := New(nil, nil)
	rec := httptest.NewRecorder()

	wr.Write(context.Background(), rec, 500, "handler", "internal", "internal error")

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	var body Body
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error != "internal" {
		t.Errorf("body.Error = %q, want internal", body.Error)
	}
}

func TestWriter_FallbackOnMarshalFailure(t *testing.T) {
	t.Parallel()

	failMarshal := func(any) ([]byte, error) {
		return nil, errors.New("synthetic marshal failure")
	}

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	wr := newWithMarshaller(nil, logger, failMarshal)
	rec := httptest.NewRecorder()

	wr.Write(context.Background(), rec, 500, "routing", "no_route", "not found")

	if got := rec.Body.String(); got != fallbackBody {
		t.Errorf("body = %q, want %q", got, fallbackBody)
	}
	if !bytes.Contains(buf.Bytes(), []byte("marshal error body")) {
		t.Errorf("expected marshal warning in logger output; got %s", buf.String())
	}
}

func TestWriter_FallbackWithNilLogger(t *testing.T) {
	t.Parallel()

	failMarshal := func(any) ([]byte, error) {
		return nil, errors.New("synthetic marshal failure")
	}

	wr := newWithMarshaller(nil, nil, failMarshal)
	rec := httptest.NewRecorder()

	wr.Write(context.Background(), rec, 500, "routing", "no_route", "not found")

	if got := rec.Body.String(); got != fallbackBody {
		t.Errorf("body = %q, want %q", got, fallbackBody)
	}
}

func counterIncremented(rm metricdata.ResourceMetrics, name string) bool {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				return false
			}
			for _, dp := range sum.DataPoints {
				if dp.Value > 0 {
					return true
				}
			}
		}
	}
	return false
}
