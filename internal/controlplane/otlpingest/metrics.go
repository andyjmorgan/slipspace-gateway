package otlpingest

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// PointsFromMetric extracts samples from a single OTLP metric's number data
// points (gauge + sum), merging resource attributes under each point's own.
// Histogram and summary metrics are skipped — number points cover the
// rate/token/latency-sum counters the gateways export.
func PointsFromMetric(resourceAttrs []*commonpb.KeyValue, m *metricspb.Metric) []configdb.MetricPoint {
	if m == nil {
		return nil
	}
	var dps []*metricspb.NumberDataPoint
	switch {
	case m.GetGauge() != nil:
		dps = m.GetGauge().GetDataPoints()
	case m.GetSum() != nil:
		dps = m.GetSum().GetDataPoints()
	default:
		return nil
	}

	out := make([]configdb.MetricPoint, 0, len(dps))
	for _, dp := range dps {
		labels, _ := json.Marshal(attrsToLabels(resourceAttrs, dp.GetAttributes()))
		out = append(out, configdb.MetricPoint{
			Name:       m.GetName(),
			Labels:     labels,
			Value:      numberValue(dp),
			ObservedAt: time.Unix(0, int64(dp.GetTimeUnixNano())).UTC(), //nolint:gosec // a unix-nano timestamp never overflows int64
		})
	}
	return out
}

func attrsToLabels(resourceAttrs, pointAttrs []*commonpb.KeyValue) map[string]string {
	merged := mergeAttrs(resourceAttrs, pointAttrs)
	out := make(map[string]string, len(merged))
	for k := range merged {
		if s := strAttr(merged, k); s != "" {
			out[k] = s
		}
	}
	return out
}

func numberValue(dp *metricspb.NumberDataPoint) float64 {
	switch v := dp.GetValue().(type) {
	case *metricspb.NumberDataPoint_AsInt:
		return float64(v.AsInt)
	case *metricspb.NumberDataPoint_AsDouble:
		return v.AsDouble
	default:
		return 0
	}
}

// metricStore is the slice of the Postgres store the metrics receiver writes to.
type metricStore interface {
	InsertMetricPoints(ctx context.Context, points []configdb.MetricPoint) error
}

// MetricsReceiver is the CP's OTLP metrics ingest: gateway-exported counters and
// gauges land in metric_points for the console's aggregate panels. Registered on
// the CP gRPC server beside the trace receiver and fleet channel; CP-0 holds.
type MetricsReceiver struct {
	collectormetrics.UnimplementedMetricsServiceServer

	store  metricStore
	logger *slog.Logger
}

// NewMetricsReceiver builds the OTLP metrics receiver over store.
func NewMetricsReceiver(store metricStore, logger *slog.Logger) *MetricsReceiver {
	return &MetricsReceiver{store: store, logger: logger}
}

// Export ingests a batch of OTLP metrics. A store failure is logged, never
// returned — telemetry ingest does not reject the batch.
func (r *MetricsReceiver) Export(ctx context.Context, req *collectormetrics.ExportMetricsServiceRequest) (*collectormetrics.ExportMetricsServiceResponse, error) {
	var points []configdb.MetricPoint
	for _, rm := range req.GetResourceMetrics() {
		resourceAttrs := rm.GetResource().GetAttributes()
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				points = append(points, PointsFromMetric(resourceAttrs, m)...)
			}
		}
	}
	if len(points) > 0 {
		if err := r.store.InsertMetricPoints(ctx, points); err != nil {
			r.logger.Warn("otlp ingest: insert metric points", "count", len(points), "error", err)
		}
	}
	return &collectormetrics.ExportMetricsServiceResponse{}, nil
}
