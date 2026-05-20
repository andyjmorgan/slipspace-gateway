package admin_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/andyjmorgan/sluice-gateway/internal/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

func newMeters(t *testing.T) (*observability.Meters, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	m, err := observability.NewMeters(mp.Meter(observability.MeterName))
	if err != nil {
		t.Fatalf("NewMeters: %v", err)
	}
	return m, reader
}

func TestInstrumentRoute_IncrementsCounter(t *testing.T) {
	meters, reader := newMeters(t)
	h := admin.InstrumentRoute(meters, "/api/v1/auth/me",
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	h.ServeHTTP(rec, req)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	var foundStatus, foundRoute bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != observability.MetricAdminRequestsTotal {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("metric data type = %T, want Sum[int64]", m.Data)
			}
			for _, p := range sum.DataPoints {
				for _, kv := range p.Attributes.ToSlice() {
					if kv.Key == "route" && kv.Value.AsString() == "/api/v1/auth/me" {
						foundRoute = true
					}
					if kv.Key == "status" && kv.Value.AsString() == "200" {
						foundStatus = true
					}
				}
			}
		}
	}
	if !foundRoute {
		t.Error("counter did not carry route attribute")
	}
	if !foundStatus {
		t.Error("counter did not carry status attribute")
	}
}

func TestInstrumentRoute_StatusDefaultsTo200(t *testing.T) {
	meters, reader := newMeters(t)
	h := admin.InstrumentRoute(meters, "static",
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			// no WriteHeader, just Write — should default to 200
			_, _ = w.Write([]byte("hello"))
		}),
	)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	var found bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != observability.MetricAdminRequestsTotal {
				continue
			}
			sum := m.Data.(metricdata.Sum[int64])
			for _, p := range sum.DataPoints {
				for _, kv := range p.Attributes.ToSlice() {
					if kv.Key == "status" && kv.Value.AsString() == "200" {
						found = true
					}
				}
			}
		}
	}
	if !found {
		t.Error("status attribute did not default to 200")
	}
}

// TestInstrumentRoute_ForwardsFlusher pins the SSE-friendliness of the
// statusRecorder wrapper. The /api/v1/messages/stream route depends on
// the underlying http.ResponseWriter still satisfying http.Flusher
// after instrumentation; if the wrapper drops the capability, the SSE
// handler bails with "streaming unsupported" (500) — which is exactly
// the bug that shipped in v1.1.7.
func TestInstrumentRoute_ForwardsFlusher(t *testing.T) {
	meters, _ := newMeters(t)
	var sawFlusher bool
	h := admin.InstrumentRoute(meters, "/probe",
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if _, ok := w.(http.Flusher); ok {
				sawFlusher = true
			}
			w.WriteHeader(http.StatusOK)
		}),
	)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/probe")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if !sawFlusher {
		t.Fatalf("handler did not observe http.Flusher through the wrapper")
	}
}

func TestInstrumentRoute_NilMetersIsPassthrough(t *testing.T) {
	called := false
	h := admin.InstrumentRoute(nil, "noop", http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)
	if !called {
		t.Error("nil meters should still call next handler")
	}
}
