package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

func TestTimeseriesHandler_RPSWithLiveSnapshotter(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	snap, err := observability.NewSnapshotter(observability.SnapshotterOptions{
		Reader:   reader,
		Interval: time.Millisecond,
		Capacity: 8,
	})
	if err != nil {
		t.Fatalf("NewSnapshotter: %v", err)
	}
	ctr, _ := mp.Meter(observability.MeterName).Int64Counter(observability.MetricRequestsTotal)
	ctx := context.Background()

	_ = snap.Snapshot(ctx)
	ctr.Add(ctx, 100, metric.WithAttributes(
		attribute.String(observability.AttrGenAIProviderName, "openai"),
		attribute.String(observability.AttrHTTPResponseStatusCode, "200"),
	))
	time.Sleep(2 * time.Millisecond)
	_ = snap.Snapshot(ctx)

	h := admin.TimeseriesHandler(snap)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/timeseries?series=rps", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got adminc.DashboardTimeseries
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Series) != 1 {
		t.Fatalf("len(series) = %d, want 1", len(got.Series))
	}
	if len(got.Series[0].Points) == 0 {
		t.Error("series[0].Points empty")
	}
	if got.Series[0].Points[0].Value <= 0 {
		t.Errorf("first point value = %f, want > 0", got.Series[0].Points[0].Value)
	}
}

func TestTimeseriesHandler_NilSnapshotterReturnsEmpty(t *testing.T) {
	h := admin.TimeseriesHandler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/timeseries?series=rps", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got adminc.DashboardTimeseries
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Series) != 0 {
		t.Errorf("len(series) = %d, want 0 with nil snapshotter", len(got.Series))
	}
}

func TestTimeseriesHandler_UnknownSeriesReturnsEmpty(t *testing.T) {
	// Build a snapshotter with at least one sample so the handler
	// reaches the switch on series name.
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	snap, err := observability.NewSnapshotter(observability.SnapshotterOptions{Reader: reader, Interval: time.Millisecond, Capacity: 4})
	if err != nil {
		t.Fatalf("NewSnapshotter: %v", err)
	}
	_ = snap.Snapshot(context.Background())

	h := admin.TimeseriesHandler(snap)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/timeseries?series=not_a_real_series", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got adminc.DashboardTimeseries
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Series) != 0 {
		t.Errorf("len(series) = %d, want 0 for unknown series name", len(got.Series))
	}
}
