package safego_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
	"github.com/andyjmorgan/sluice-gateway/internal/safego"
)

func newMeters(t *testing.T) (*observability.Meters, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	meters, err := observability.NewMeters(provider.Meter(observability.MeterName))
	if err != nil {
		t.Fatalf("NewMeters: %v", err)
	}
	return meters, reader
}

func newLog() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

func waitForCounter(t *testing.T, reader *sdkmetric.ManualReader, name, siteLabel string) int64 { //nolint:unparam // name kept as a parameter so the helper generalises to RequestPanicsTotal etc. without a signature change
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var rm metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &rm); err != nil {
			t.Fatalf("collect: %v", err)
		}
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name != name {
					continue
				}
				sum, ok := m.Data.(metricdata.Sum[int64])
				if !ok {
					continue
				}
				for _, dp := range sum.DataPoints {
					iter := dp.Attributes.Iter()
					for iter.Next() {
						attr := iter.Attribute()
						if string(attr.Key) == "site" && attr.Value.AsString() == siteLabel {
							return dp.Value
						}
					}
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("counter %q with site=%q never recorded", name, siteLabel)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGo_PanicRecovered_ProcessKeptAlive(t *testing.T) {
	t.Parallel()
	meters, reader := newMeters(t)
	log, logs := newLog()

	done := make(chan struct{})
	safego.Go(context.Background(), "test.panic_site", log, meters, func() {
		defer close(done)
		panic("boom")
	})

	<-done

	// Counter incremented exactly once for this site.
	if got := waitForCounter(t, reader, observability.MetricGoroutinePanicsTotal, "test.panic_site"); got != 1 {
		t.Errorf("counter = %d, want 1", got)
	}

	// Log entry carries site + panic message + stack.
	entry := logs.String()
	if !strings.Contains(entry, `"site":"test.panic_site"`) {
		t.Errorf("log missing site label: %s", entry)
	}
	if !strings.Contains(entry, "boom") {
		t.Errorf("log missing panic message: %s", entry)
	}
	if !strings.Contains(entry, `"stack"`) {
		t.Errorf("log missing stack field: %s", entry)
	}
	if !strings.Contains(entry, `"level":"ERROR"`) {
		t.Errorf("log entry not at ERROR level: %s", entry)
	}
}

func TestGo_NoPanic_FnRunsAndReturns(t *testing.T) {
	t.Parallel()
	meters, reader := newMeters(t)
	log, logs := newLog()

	done := make(chan struct{})
	var ran bool
	safego.Go(context.Background(), "test.normal_site", log, meters, func() {
		ran = true
		close(done)
	})
	<-done

	if !ran {
		t.Fatal("fn did not run")
	}

	// Counter must NOT increment on the normal path.
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != observability.MetricGoroutinePanicsTotal {
				continue
			}
			sum := m.Data.(metricdata.Sum[int64])
			for _, dp := range sum.DataPoints {
				iter := dp.Attributes.Iter()
				for iter.Next() {
					attr := iter.Attribute()
					if string(attr.Key) == "site" && attr.Value.AsString() == "test.normal_site" {
						t.Errorf("counter incremented on the non-panic path: %d", dp.Value)
					}
				}
			}
		}
	}
	if logs.Len() != 0 {
		t.Errorf("non-panic path should not log anything; got %s", logs.String())
	}
}

func TestRun_PanicRecovered_Synchronous(t *testing.T) {
	t.Parallel()
	meters, reader := newMeters(t)
	log, _ := newLog()

	safego.Run(context.Background(), "test.sync_site", log, meters, func() { panic("sync-boom") })

	// We reached this line — Run did NOT propagate the panic.
	if got := waitForCounter(t, reader, observability.MetricGoroutinePanicsTotal, "test.sync_site"); got != 1 {
		t.Errorf("counter = %d, want 1", got)
	}
}

func TestGo_NilLogAndMeters_StillRecovers(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	safego.Go(context.Background(), "test.nil_obs", nil, nil, func() {
		defer close(done)
		panic("silent boom")
	})
	<-done
	// If we reach here, the panic was recovered even without log/meters.
}

func TestGo_DistinctSites_TrackedIndependently(t *testing.T) {
	t.Parallel()
	meters, reader := newMeters(t)

	wg := sync.WaitGroup{}
	wg.Add(2)
	safego.Go(context.Background(), "test.site_a", nil, meters, func() { defer wg.Done(); panic("a") })
	safego.Go(context.Background(), "test.site_b", nil, meters, func() { defer wg.Done(); panic("b") })
	wg.Wait()

	if got := waitForCounter(t, reader, observability.MetricGoroutinePanicsTotal, "test.site_a"); got != 1 {
		t.Errorf("site_a counter = %d, want 1", got)
	}
	if got := waitForCounter(t, reader, observability.MetricGoroutinePanicsTotal, "test.site_b"); got != 1 {
		t.Errorf("site_b counter = %d, want 1", got)
	}
}
