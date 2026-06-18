package admin

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
)

// newLiveSnapshotter wires a real ManualReader-backed snapshotter and
// pre-populates it with two timestamped samples by recording metrics
// between explicit Snapshot calls. Returns the snapshotter and the
// meter so callers can record their own data.
func newLiveSnapshotter(t *testing.T) (*observability.Snapshotter, metric.Meter) {
	t.Helper()
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
	return snap, mp.Meter(observability.MeterName)
}

func TestComputeSummary_WithLiveSnapshotter(t *testing.T) {
	snap, meter := newLiveSnapshotter(t)
	ctx := context.Background()

	ctr, _ := meter.Int64Counter(observability.MetricRequestsTotal)
	// Sample 1: empty.
	_ = snap.Snapshot(ctx)

	// Add traffic.
	ctr.Add(ctx, 50, metric.WithAttributes(
		attribute.String(observability.AttrGenAIProviderName, "openai"),
		attribute.String(observability.AttrHTTPResponseStatusCode, "200"),
	))
	ctr.Add(ctx, 5, metric.WithAttributes(
		attribute.String(observability.AttrGenAIProviderName, "openai"),
		attribute.String(observability.AttrHTTPResponseStatusCode, "500"),
	))
	time.Sleep(2 * time.Millisecond)
	_ = snap.Snapshot(ctx)

	got := computeSummary(snap, []string{"openai"}, nil, nil, time.Hour, 5*time.Minute)
	if got.Totals.Requests != 55 {
		t.Errorf("Totals.Requests = %d, want 55", got.Totals.Requests)
	}
	if got.Totals.RequestsSuccess != 50 {
		t.Errorf("Totals.RequestsSuccess = %d, want 50", got.Totals.RequestsSuccess)
	}
	if got.Totals.RequestsErrored != 5 {
		t.Errorf("Totals.RequestsErrored = %d, want 5", got.Totals.RequestsErrored)
	}
}

func TestComputeSummary_RingTooSmallReturnsEmpty(t *testing.T) {
	snap, _ := newLiveSnapshotter(t)
	got := computeSummary(snap, []string{"openai"}, nil, nil, time.Hour, 5*time.Minute)
	if got.Totals.Requests != 0 {
		t.Errorf("Totals.Requests = %d, want 0 (no samples yet)", got.Totals.Requests)
	}
	if len(got.ProviderHealth) != 1 {
		t.Errorf("ProviderHealth = %d, want 1 (empty body still seeds providers)", len(got.ProviderHealth))
	}
}

func TestFormatIntUnit_NegativeClampsToZero(t *testing.T) {
	if got := formatIntUnit(-3, "m"); got != "0m" {
		t.Errorf("formatIntUnit(-3, m) = %q, want 0m", got)
	}
}

func TestItoa_Zero(t *testing.T) {
	if got := itoa(0); got != "0" {
		t.Errorf("itoa(0) = %q, want 0", got)
	}
}

func TestParseWindow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw      string
		fallback time.Duration
		want     time.Duration
	}{
		{raw: "1h", fallback: 24 * time.Hour, want: time.Hour},
		{raw: "24h", fallback: time.Hour, want: 24 * time.Hour},
		{raw: "", fallback: 12 * time.Hour, want: 12 * time.Hour},
		{raw: "5m", fallback: time.Hour, want: time.Hour}, // unrecognised → fallback
		{raw: "garbage", fallback: 7 * time.Hour, want: 7 * time.Hour},
	}
	for _, tc := range cases {
		if got := parseWindow(tc.raw, tc.fallback); got != tc.want {
			t.Errorf("parseWindow(%q, %v) = %v, want %v", tc.raw, tc.fallback, got, tc.want)
		}
	}
}
