package observability_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
)

// stubSpoolSource satisfies SpoolStatsSource with canned rows.
type stubSpoolSource struct {
	rows []observability.SpoolTrackSnapshot
}

func (s stubSpoolSource) Snapshot() []observability.SpoolTrackSnapshot { return s.rows }

// point is one collected data point flattened for assertions.
type point struct {
	value int64
	attrs map[string]string
}

func collectSpoolPoints(t *testing.T, reader *sdkmetric.ManualReader) map[string][]point {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := map[string][]point{}
	flatten := func(name string, attrs []metricdata.DataPoint[int64]) {
		for _, dp := range attrs {
			p := point{value: dp.Value, attrs: map[string]string{}}
			for _, kv := range dp.Attributes.ToSlice() {
				p.attrs[string(kv.Key)] = kv.Value.AsString()
			}
			out[name] = append(out[name], p)
		}
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch data := m.Data.(type) {
			case metricdata.Sum[int64]:
				flatten(m.Name, data.DataPoints)
			case metricdata.Gauge[int64]:
				flatten(m.Name, data.DataPoints)
			}
		}
	}
	return out
}

func findPoint(points []point, want map[string]string) (int64, bool) {
outer:
	for _, p := range points {
		for k, v := range want {
			if p.attrs[k] != v {
				continue outer
			}
		}
		return p.value, true
	}
	return 0, false
}

// TestRegisterSpoolInstruments_ReportsRows proves every gateway.spool.*
// instrument is registered under the meter and populated from the
// source snapshot with the documented labels — the /metrics surface #560
// asked for.
func TestRegisterSpoolInstruments_ReportsRows(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	src := stubSpoolSource{rows: []observability.SpoolTrackSnapshot{
		{
			Connector: "archive", Registered: true,
			Enqueued: 10, DroppedRing: 2, Written: 8, WriteErrors: 1, SegmentsSealed: 3,
			UploadsOK: 2, UploadsRetried: 4, UploadsDLQ: 1,
			BreakerState: 2, BreakerStateName: "open", PendingSegments: 5,
		},
		// A name records were bound to that never had a track: only the
		// no_track drop counter is meaningful.
		{Connector: "ghost", Registered: false, DroppedNoTrack: 7},
	}}

	if err := observability.RegisterSpoolInstruments(mp.Meter(observability.MeterName), src, "pod-a"); err != nil {
		t.Fatalf("RegisterSpoolInstruments: %v", err)
	}
	got := collectSpoolPoints(t, reader)

	for _, name := range []string{
		observability.MetricSpoolEnqueuedTotal,
		observability.MetricSpoolDroppedTotal,
		observability.MetricSpoolWrittenTotal,
		observability.MetricSpoolWriteErrorsTotal,
		observability.MetricSpoolSegmentsSealedTotal,
		observability.MetricSpoolUploadsTotal,
		observability.MetricSpoolBreakerState,
		observability.MetricSpoolPendingSegments,
	} {
		if len(got[name]) == 0 {
			t.Errorf("missing instrument %q in collected output", name)
		}
	}

	cases := []struct {
		metric string
		attrs  map[string]string
		want   int64
	}{
		{observability.MetricSpoolEnqueuedTotal, map[string]string{"connector": "archive"}, 10},
		{observability.MetricSpoolDroppedTotal, map[string]string{"connector": "archive", "reason": "ring_full"}, 2},
		{observability.MetricSpoolDroppedTotal, map[string]string{"connector": "ghost", "reason": "no_track"}, 7},
		{observability.MetricSpoolWrittenTotal, map[string]string{"connector": "archive"}, 8},
		{observability.MetricSpoolWriteErrorsTotal, map[string]string{"connector": "archive"}, 1},
		{observability.MetricSpoolSegmentsSealedTotal, map[string]string{"connector": "archive"}, 3},
		{observability.MetricSpoolUploadsTotal, map[string]string{"connector": "archive", "outcome": "ok"}, 2},
		{observability.MetricSpoolUploadsTotal, map[string]string{"connector": "archive", "outcome": "retried"}, 4},
		{observability.MetricSpoolUploadsTotal, map[string]string{"connector": "archive", "outcome": "dlq"}, 1},
		{observability.MetricSpoolBreakerState, map[string]string{"connector": "archive", "pod": "pod-a", "state_name": "open"}, 2},
		{observability.MetricSpoolPendingSegments, map[string]string{"connector": "archive", "pod": "pod-a"}, 5},
	}
	for _, c := range cases {
		v, ok := findPoint(got[c.metric], c.attrs)
		if !ok {
			t.Errorf("%s %v: no data point", c.metric, c.attrs)
			continue
		}
		if v != c.want {
			t.Errorf("%s %v = %d, want %d", c.metric, c.attrs, v, c.want)
		}
	}

	// The unregistered name must not fabricate a track's worth of zeros.
	if _, ok := findPoint(got[observability.MetricSpoolEnqueuedTotal], map[string]string{"connector": "ghost"}); ok {
		t.Errorf("unregistered connector emitted %s", observability.MetricSpoolEnqueuedTotal)
	}
	if _, ok := findPoint(got[observability.MetricSpoolBreakerState], map[string]string{"connector": "ghost"}); ok {
		t.Errorf("unregistered connector emitted %s", observability.MetricSpoolBreakerState)
	}
	// A registered track with no no_track drops emits no no_track series.
	if _, ok := findPoint(got[observability.MetricSpoolDroppedTotal], map[string]string{"connector": "archive", "reason": "no_track"}); ok {
		t.Errorf("registered track with zero no_track drops emitted a no_track series")
	}
}

func TestRegisterSpoolInstruments_NilSourceNoOp(t *testing.T) {
	t.Parallel()
	mp := sdkmetric.NewMeterProvider()
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	if err := observability.RegisterSpoolInstruments(mp.Meter(observability.MeterName), nil, "pod"); err != nil {
		t.Errorf("nil source should be a no-op, got error: %v", err)
	}
}

func TestRegisterSpoolInstruments_NilMeterErrors(t *testing.T) {
	t.Parallel()
	if err := observability.RegisterSpoolInstruments(nil, stubSpoolSource{}, "pod"); err == nil {
		t.Errorf("expected error for nil meter")
	}
}

// failingCounterMeter fails Int64ObservableCounter so the counter wrap
// branch is exercised; failingMeter (meters_test.go) covers the gauge one.
type failingCounterMeter struct {
	noop.Meter
}

func (failingCounterMeter) Int64ObservableCounter(_ string, _ ...metric.Int64ObservableCounterOption) (metric.Int64ObservableCounter, error) {
	return nil, errors.New("synthetic counter failure")
}

// failingCallbackMeter fails RegisterCallback so the callback wrap
// branch is exercised.
type failingCallbackMeter struct {
	noop.Meter
}

func (failingCallbackMeter) RegisterCallback(_ metric.Callback, _ ...metric.Observable) (metric.Registration, error) {
	return nil, errors.New("synthetic callback failure")
}

func TestRegisterSpoolInstruments_RegisterErrorsAreWrapped(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		meter   metric.Meter
		mention string
	}{
		{"counter", failingCounterMeter{}, observability.MetricSpoolEnqueuedTotal},
		{"gauge", failingMeter{}, observability.MetricSpoolBreakerState},
		{"callback", failingCallbackMeter{}, "spool callback"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := observability.RegisterSpoolInstruments(c.meter, stubSpoolSource{}, "pod")
			if err == nil {
				t.Fatalf("expected error from %s meter", c.name)
			}
			if !strings.Contains(err.Error(), c.mention) {
				t.Errorf("error %q should mention %q", err.Error(), c.mention)
			}
		})
	}
}
