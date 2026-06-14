package ingest

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"

	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

// eventWriter is the slice of the store the trace receiver writes through.
// UpsertRequestEventWithChecks is used only when an Exploder is attached (the
// Arbiter scanner is enabled), to write the span and its check tasks atomically.
type eventWriter interface {
	UpsertRequestEvent(ctx context.Context, e store.RequestEvent) error
	UpsertRequestEventWithChecks(ctx context.Context, e store.RequestEvent, checks []store.CheckTask) error
}

// Exploder builds the Arbiter check tasks for a span (ADR-004). nil when the
// scanner is disabled, in which case ingest is a plain upsert and untouched.
// Implemented by *arbiter.Scanner; kept as a consumer-side interface so this
// package does not import the scanner.
type Exploder interface {
	Explode(e store.RequestEvent) []store.CheckTask
}

// metricWriter is the slice of the store the metrics receiver writes through.
type metricWriter interface {
	InsertMetricPoints(ctx context.Context, points []store.MetricPoint) error
}

// TraceReceiver is the telemetry service's OTLP trace ingest: for every gen_ai
// span a gateway pushes (directly or via a collector), it stores a
// request_events row. Unlike the scrapped control plane it signs no receipts
// and never sits on a request path — it only ever consumes telemetry.
type TraceReceiver struct {
	collectortrace.UnimplementedTraceServiceServer

	store  eventWriter
	logger *slog.Logger
	// contentMaxBytes bounds the gen_ai content kept per request; <= 0 keeps it
	// whole. Resolved from the telemetry config's content_max_bytes.
	contentMaxBytes int
	// exploder, when non-nil, builds Arbiter check tasks written atomically with
	// the span. nil leaves ingest a plain upsert (scanner disabled).
	exploder Exploder
}

// NewTraceReceiver builds the OTLP trace receiver over store. contentMaxBytes
// bounds the gen_ai content captured per span (<= 0 disables the cap).
// exploder may be nil (scanner disabled); when set, each span is written with
// its check tasks in one transaction.
func NewTraceReceiver(store eventWriter, logger *slog.Logger, contentMaxBytes int, exploder Exploder) *TraceReceiver {
	return &TraceReceiver{store: store, logger: logger, contentMaxBytes: contentMaxBytes, exploder: exploder}
}

// Export ingests a batch of OTLP spans. A per-span failure is logged and
// skipped — telemetry ingest must never reject the whole batch — so the
// response is always success.
func (r *TraceReceiver) Export(ctx context.Context, req *collectortrace.ExportTraceServiceRequest) (*collectortrace.ExportTraceServiceResponse, error) {
	for _, rs := range req.GetResourceSpans() {
		resourceAttrs := rs.GetResource().GetAttributes()
		for _, ss := range rs.GetScopeSpans() {
			for _, span := range ss.GetSpans() {
				e, ok := EventFromSpan(resourceAttrs, span, r.contentMaxBytes)
				if !ok {
					continue
				}
				if err := r.ingest(ctx, e); err != nil {
					r.logger.Warn("otlp ingest: upsert request event", "correlation_id", e.CorrelationID, "error", err)
				}
			}
		}
	}
	return &collectortrace.ExportTraceServiceResponse{}, nil
}

// ingest writes one span. With no exploder (scanner off) it is a plain upsert.
// With an exploder, the span's check tasks are built and — when there are any —
// written atomically with the span (ADR-004); a span that explodes to nothing
// (no scannable content / no configured check) still takes the plain path.
func (r *TraceReceiver) ingest(ctx context.Context, e store.RequestEvent) error {
	if r.exploder == nil {
		return r.store.UpsertRequestEvent(ctx, e)
	}
	checks := r.exploder.Explode(e)
	if len(checks) == 0 {
		return r.store.UpsertRequestEvent(ctx, e)
	}
	return r.store.UpsertRequestEventWithChecks(ctx, e, checks)
}

// MetricsReceiver is the telemetry service's OTLP metrics ingest: gateway-
// exported counters and gauges land in metric_points for the dashboard's
// aggregate panels.
type MetricsReceiver struct {
	collectormetrics.UnimplementedMetricsServiceServer

	store  metricWriter
	logger *slog.Logger
}

// NewMetricsReceiver builds the OTLP metrics receiver over store.
func NewMetricsReceiver(store metricWriter, logger *slog.Logger) *MetricsReceiver {
	return &MetricsReceiver{store: store, logger: logger}
}

// Export ingests a batch of OTLP metrics. A store failure is logged, never
// returned — telemetry ingest does not reject the batch.
func (r *MetricsReceiver) Export(ctx context.Context, req *collectormetrics.ExportMetricsServiceRequest) (*collectormetrics.ExportMetricsServiceResponse, error) {
	var points []store.MetricPoint
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

// PointsFromMetric extracts samples from a single OTLP metric's number data
// points (gauge + sum), merging resource attributes under each point's own.
// Histogram and summary metrics are skipped — number points cover the
// rate/token/latency-sum counters the gateways export.
func PointsFromMetric(resourceAttrs []*commonpb.KeyValue, m *metricspb.Metric) []store.MetricPoint {
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

	out := make([]store.MetricPoint, 0, len(dps))
	for _, dp := range dps {
		labels, _ := json.Marshal(attrsToLabels(resourceAttrs, dp.GetAttributes()))
		out = append(out, store.MetricPoint{
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
