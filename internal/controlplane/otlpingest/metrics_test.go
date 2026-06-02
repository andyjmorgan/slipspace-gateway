package otlpingest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

func intPoint(v int64, attrs ...*commonpb.KeyValue) *metricspb.NumberDataPoint {
	return &metricspb.NumberDataPoint{
		TimeUnixNano: 1_700_000_000_000_000_000,
		Attributes:   attrs,
		Value:        &metricspb.NumberDataPoint_AsInt{AsInt: v},
	}
}

func doublePoint(v float64) *metricspb.NumberDataPoint {
	return &metricspb.NumberDataPoint{
		TimeUnixNano: 1_700_000_000_000_000_000,
		Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: v},
	}
}

func TestPointsFromMetric(t *testing.T) {
	res := []*commonpb.KeyValue{kvStr("sluice.gateway_id", "gw-1")}

	sum := &metricspb.Metric{
		Name: "sluice_requests_total",
		Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
			DataPoints: []*metricspb.NumberDataPoint{intPoint(5, kvStr("model", "gpt-4o"))},
		}},
	}
	pts := PointsFromMetric(res, sum)
	if len(pts) != 1 || pts[0].Name != "sluice_requests_total" || pts[0].Value != 5 {
		t.Fatalf("sum points = %+v", pts)
	}
	var labels map[string]string
	_ = json.Unmarshal(pts[0].Labels, &labels)
	if labels["model"] != "gpt-4o" || labels["sluice.gateway_id"] != "gw-1" {
		t.Errorf("labels = %v (resource + point merged)", labels)
	}
	if pts[0].ObservedAt.IsZero() {
		t.Error("observed_at not set from time_unix_nano")
	}

	gauge := &metricspb.Metric{
		Name: "g",
		Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{
			DataPoints: []*metricspb.NumberDataPoint{doublePoint(2.5)},
		}},
	}
	if pts := PointsFromMetric(res, gauge); len(pts) != 1 || pts[0].Value != 2.5 {
		t.Fatalf("gauge points = %+v", pts)
	}

	// Histograms and nil are skipped.
	hist := &metricspb.Metric{Name: "h", Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{}}}
	if pts := PointsFromMetric(res, hist); pts != nil {
		t.Errorf("histogram should be skipped, got %+v", pts)
	}
	if pts := PointsFromMetric(res, nil); pts != nil {
		t.Errorf("nil metric should be skipped, got %+v", pts)
	}
}

type fakeMetricStore struct {
	points []configdb.MetricPoint
	err    error
}

func (f *fakeMetricStore) InsertMetricPoints(_ context.Context, points []configdb.MetricPoint) error {
	if f.err != nil {
		return f.err
	}
	f.points = append(f.points, points...)
	return nil
}

func metricsReq(metrics ...*metricspb.Metric) *collectormetrics.ExportMetricsServiceRequest {
	return &collectormetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			Resource:     &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: metrics}},
		}},
	}
}

func newMetricsReceiver(store metricStore) *MetricsReceiver {
	return NewMetricsReceiver(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestMetricsReceiver_Export(t *testing.T) {
	store := &fakeMetricStore{}
	r := newMetricsReceiver(store)

	sum := &metricspb.Metric{Name: "m", Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
		DataPoints: []*metricspb.NumberDataPoint{intPoint(1), intPoint(2)},
	}}}
	if _, err := r.Export(context.Background(), metricsReq(sum)); err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(store.points) != 2 {
		t.Fatalf("stored %d points, want 2", len(store.points))
	}
}

func TestMetricsReceiver_Export_StoreErrorTolerated(t *testing.T) {
	r := newMetricsReceiver(&fakeMetricStore{err: errors.New("db down")})
	sum := &metricspb.Metric{Name: "m", Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
		DataPoints: []*metricspb.NumberDataPoint{intPoint(1)},
	}}}
	if _, err := r.Export(context.Background(), metricsReq(sum)); err != nil {
		t.Fatalf("a store error must not fail the export: %v", err)
	}
}
