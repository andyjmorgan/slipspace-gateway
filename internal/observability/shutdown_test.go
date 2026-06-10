package observability_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// TestFlushThenShutdown_Ordering pins the graceful-termination contract: every
// flush callback runs before any shutdown callback, in registration order, and
// the composed callable is idempotent. This ordering is what guarantees
// buffered spans/metric points hit the wire before the exporters are torn
// down (the June 2026 binary-swap incident lost ~90 spans to the absence of
// exactly this).
func TestFlushThenShutdown_Ordering(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		flushes   []string
		shutdowns []string
		want      []string
	}{
		{
			name:      "flushes run before shutdowns in order",
			flushes:   []string{"flush_meter", "flush_tracer", "flush_logger"},
			shutdowns: []string{"stop_exporter", "stop_meter", "stop_tracer"},
			want:      []string{"flush_meter", "flush_tracer", "flush_logger", "stop_exporter", "stop_meter", "stop_tracer"},
		},
		{
			name:      "no flushes degrades to plain shutdown",
			shutdowns: []string{"stop_a", "stop_b"},
			want:      []string{"stop_a", "stop_b"},
		},
		{
			name: "no callbacks at all is a no-op",
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got []string
			mk := func(name string) func(context.Context) error {
				return func(context.Context) error {
					got = append(got, name)
					return nil
				}
			}
			var flushFns, shutdownFns []func(context.Context) error
			for _, n := range tc.flushes {
				flushFns = append(flushFns, mk(n))
			}
			for _, n := range tc.shutdowns {
				shutdownFns = append(shutdownFns, mk(n))
			}

			fn := observability.FlushThenShutdown(flushFns, shutdownFns)
			if err := fn(context.Background()); err != nil {
				t.Fatalf("first call: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("calls = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("call order = %v, want %v", got, tc.want)
				}
			}

			// Idempotent: a second invocation must not re-run anything.
			if err := fn(context.Background()); err != nil {
				t.Fatalf("second call: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("second call re-ran callbacks: %v", got)
			}
		})
	}
}

// TestFlushThenShutdown_ErrorsJoinedAllRun proves a failing flush neither
// aborts the remaining flushes nor skips the shutdowns, every error is
// joined into the result, and repeat invocations return the cached error.
func TestFlushThenShutdown_ErrorsJoinedAllRun(t *testing.T) {
	t.Parallel()

	flushErr := errors.New("flush boom")
	stopErr := errors.New("stop boom")
	var got []string
	mk := func(name string, err error) func(context.Context) error {
		return func(context.Context) error {
			got = append(got, name)
			return err
		}
	}

	fn := observability.FlushThenShutdown(
		[]func(context.Context) error{mk("flush_a", flushErr), mk("flush_b", nil)},
		[]func(context.Context) error{mk("stop_a", stopErr), mk("stop_b", nil)},
	)

	err := fn(context.Background())
	if !errors.Is(err, flushErr) || !errors.Is(err, stopErr) {
		t.Fatalf("joined error = %v, want both flushErr and stopErr", err)
	}
	if len(got) != 4 {
		t.Fatalf("ran %v, want all 4 callbacks despite errors", got)
	}

	if second := fn(context.Background()); !errors.Is(second, flushErr) {
		t.Fatalf("second call error = %v, want cached first result", second)
	}
	if len(got) != 4 {
		t.Fatalf("second call re-ran callbacks: %v", got)
	}
}

// TestExportErrorHandler_CountsAndLogs proves the global OTel error handler
// turns SDK-internal failures (in practice OTLP export errors) into the
// export-failures counter plus a warn log — the monitoring signal the
// incident lacked.
func TestExportErrorHandler_CountsAndLogs(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	meters, err := observability.NewMeters(mp.Meter(observability.MeterName))
	if err != nil {
		t.Fatalf("NewMeters: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := observability.ExportErrorHandler(meters.OTelExportFailuresTotal, logger)

	h.Handle(errors.New("otlp export boom"))
	h.Handle(errors.New("otlp export boom again"))

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != observability.MetricOTelExportFailuresTotal {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("metric %s is %T, want Sum[int64]", m.Name, m.Data)
			}
			for _, dp := range sum.DataPoints {
				total += dp.Value
			}
		}
	}
	if total != 2 {
		t.Fatalf("%s = %d, want 2", observability.MetricOTelExportFailuresTotal, total)
	}
	if !strings.Contains(buf.String(), "otlp export boom") {
		t.Fatalf("log output missing handler error: %q", buf.String())
	}
}
