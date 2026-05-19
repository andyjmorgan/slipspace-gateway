package observability_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

func newSnapshotterWithReader(t *testing.T) (*observability.Snapshotter, metric.Meter) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	s, err := observability.NewSnapshotter(observability.SnapshotterOptions{
		Reader:   reader,
		Interval: 10 * time.Millisecond,
		Capacity: 4,
		Now:      time.Now,
	})
	if err != nil {
		t.Fatalf("NewSnapshotter: %v", err)
	}
	return s, mp.Meter("test")
}

func TestSnapshotter_RingEvictsOldest(t *testing.T) {
	s, meter := newSnapshotterWithReader(t)
	ctr, err := meter.Int64Counter("c.test")
	if err != nil {
		t.Fatalf("Int64Counter: %v", err)
	}

	ctx := context.Background()
	for i := 0; i < 6; i++ {
		ctr.Add(ctx, 1)
		if err := s.Snapshot(ctx); err != nil {
			t.Fatalf("Snapshot[%d]: %v", i, err)
		}
	}

	got := s.Samples()
	if len(got) != 4 {
		t.Fatalf("len(samples) = %d, want 4 (capacity)", len(got))
	}
	// The retained samples should be the last 4; their counter values
	// should be 3..6 since the counter increments before each.
	wantVals := []int64{3, 4, 5, 6}
	for i, sm := range got {
		if v := sm.CounterValue("c.test", ""); v != wantVals[i] {
			t.Errorf("sample[%d] counter = %d, want %d", i, v, wantVals[i])
		}
	}
}

func TestSnapshotter_LabelsAreEncoded(t *testing.T) {
	s, meter := newSnapshotterWithReader(t)
	ctr, _ := meter.Int64Counter("c.labelled")

	ctx := context.Background()
	ctr.Add(ctx, 5, metric.WithAttributes(attribute.String("provider", "openai")))
	ctr.Add(ctx, 7, metric.WithAttributes(attribute.String("provider", "anthropic")))
	if err := s.Snapshot(ctx); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	latest, ok := s.Latest()
	if !ok {
		t.Fatal("Latest: ring empty")
	}
	openaiKey := observability.EncodeLabels([]attribute.KeyValue{attribute.String("provider", "openai")})
	anthropicKey := observability.EncodeLabels([]attribute.KeyValue{attribute.String("provider", "anthropic")})
	if v := latest.CounterValue("c.labelled", openaiKey); v != 5 {
		t.Errorf("openai = %d, want 5", v)
	}
	if v := latest.CounterValue("c.labelled", anthropicKey); v != 7 {
		t.Errorf("anthropic = %d, want 7", v)
	}
}

func TestSnapshotter_HistogramSnapshot(t *testing.T) {
	s, meter := newSnapshotterWithReader(t)
	hist, _ := meter.Float64Histogram("h.test",
		metric.WithExplicitBucketBoundaries(0.1, 0.5, 1),
	)

	ctx := context.Background()
	hist.Record(ctx, 0.05) // bucket 0 (<=0.1)
	hist.Record(ctx, 0.3)  // bucket 1 (<=0.5)
	hist.Record(ctx, 0.8)  // bucket 2 (<=1)
	hist.Record(ctx, 2.0)  // bucket 3 (>1, +Inf)
	if err := s.Snapshot(ctx); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	latest, _ := s.Latest()
	hs := latest.HistogramValue("h.test", "")
	if hs.Count != 4 {
		t.Errorf("count = %d, want 4", hs.Count)
	}
	if len(hs.Counts) != 4 {
		t.Fatalf("len(Counts) = %d, want 4 (3 bounds + Inf)", len(hs.Counts))
	}
	wantBucketCounts := []uint64{1, 1, 1, 1}
	for i, c := range hs.Counts {
		if c != wantBucketCounts[i] {
			t.Errorf("bucket[%d] = %d, want %d", i, c, wantBucketCounts[i])
		}
	}
}

func TestSnapshotter_WindowEnds_RingTooSmall(t *testing.T) {
	s, meter := newSnapshotterWithReader(t)
	ctr, _ := meter.Int64Counter("c.x")
	ctx := context.Background()
	// Empty ring.
	if _, _, _, ok := s.WindowEnds(time.Hour); ok {
		t.Error("WindowEnds with empty ring should return ok=false")
	}
	// One sample.
	ctr.Add(ctx, 1)
	_ = s.Snapshot(ctx)
	if _, _, _, ok := s.WindowEnds(time.Hour); ok {
		t.Error("WindowEnds with one sample should return ok=false (no delta possible)")
	}
}

func TestSnapshotter_WindowEnds_FallsBackToOldest(t *testing.T) {
	// When the requested window exceeds the ring's coverage, WindowEnds
	// uses the earliest available sample — the realised duration is
	// shorter than requested.
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	currentTime := t0
	now := func() time.Time { return currentTime }

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	s, err := observability.NewSnapshotter(observability.SnapshotterOptions{
		Reader:   reader,
		Interval: time.Hour,
		Capacity: 10,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("NewSnapshotter: %v", err)
	}
	meter := mp.Meter("test")
	ctr, _ := meter.Int64Counter("c.win")
	ctx := context.Background()

	// Three samples 1 minute apart.
	for i := 0; i < 3; i++ {
		ctr.Add(ctx, 10)
		_ = s.Snapshot(ctx)
		currentTime = currentTime.Add(time.Minute)
	}

	// Ask for a 24h window — should fall back to the first sample.
	start, end, realised, ok := s.WindowEnds(24 * time.Hour)
	if !ok {
		t.Fatal("WindowEnds returned ok=false")
	}
	if !start.At.Equal(t0) {
		t.Errorf("start.At = %v, want %v", start.At, t0)
	}
	wantEndAt := t0.Add(2 * time.Minute)
	if !end.At.Equal(wantEndAt) {
		t.Errorf("end.At = %v, want %v", end.At, wantEndAt)
	}
	if realised != 2*time.Minute {
		t.Errorf("realised = %v, want 2m", realised)
	}
	if start.CounterValue("c.win", "") != 10 || end.CounterValue("c.win", "") != 30 {
		t.Errorf("counter delta: start=%d end=%d", start.CounterValue("c.win", ""), end.CounterValue("c.win", ""))
	}
}

func TestSnapshotter_ConcurrentAppendAndRead(t *testing.T) {
	s, meter := newSnapshotterWithReader(t)
	ctr, _ := meter.Int64Counter("c.race")
	ctx := context.Background()

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				ctr.Add(ctx, 1)
				_ = s.Snapshot(ctx)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = s.Samples()
				_, _ = s.Latest()
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func TestSnapshotter_StartTakesImmediateSnapshot(t *testing.T) {
	s, meter := newSnapshotterWithReader(t)
	ctr, _ := meter.Int64Counter("c.boot")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ctr.Add(ctx, 42)
	s.Start(ctx)

	// Start synchronously takes the first sample before returning, so
	// Latest must be available immediately.
	latest, ok := s.Latest()
	if !ok {
		t.Fatal("Latest unavailable after Start")
	}
	if v := latest.CounterValue("c.boot", ""); v != 42 {
		t.Errorf("counter = %d, want 42", v)
	}
}

func TestNewSnapshotter_RequiresReader(t *testing.T) {
	if _, err := observability.NewSnapshotter(observability.SnapshotterOptions{}); err == nil {
		t.Error("expected error for nil Reader")
	}
}

func TestLabelKey_Get(t *testing.T) {
	cases := []struct {
		key, name, want string
	}{
		{"", "anything", ""},
		{"a=1", "a", "1"},
		{"a=1,b=2", "a", "1"},
		{"a=1,b=2", "b", "2"},
		{"a=1,b=2,c=3", "c", "3"},
		{"endpoint=foo", "point", ""},           // partial match must not match
		{"a=1,endpoint=foo", "endpoint", "foo"}, // boundary check on later key
		{"a=1,b=2", "missing", ""},
	}
	for _, tc := range cases {
		if got := observability.LabelKey(tc.key).Get(tc.name); got != tc.want {
			t.Errorf("LabelKey(%q).Get(%q) = %q, want %q", tc.key, tc.name, got, tc.want)
		}
	}
}

func TestSnapshotter_Interval(t *testing.T) {
	s, _ := newSnapshotterWithReader(t)
	if got := s.Interval(); got != 10*time.Millisecond {
		t.Errorf("Interval() = %v, want 10ms", got)
	}
}

func TestSample_CounterValueMissingMetric(t *testing.T) {
	sample := observability.EmptySample(time.Now())
	if got := sample.CounterValue("nonexistent.metric", ""); got != 0 {
		t.Errorf("CounterValue on missing metric = %d, want 0", got)
	}
}

func TestSample_HistogramValueMissingMetric(t *testing.T) {
	sample := observability.EmptySample(time.Now())
	got := sample.HistogramValue("nonexistent.metric", "")
	if got.Count != 0 || len(got.Counts) != 0 {
		t.Errorf("HistogramValue on missing metric = %+v, want zero", got)
	}
}

func TestEncodeLabels_Empty(t *testing.T) {
	if got := observability.EncodeLabels(nil); got != "" {
		t.Errorf("EncodeLabels(nil) = %q, want empty", got)
	}
}

func TestSnapshotter_HandlesFloat64Counter(t *testing.T) {
	s, meter := newSnapshotterWithReader(t)
	ctr, err := meter.Float64Counter("c.float")
	if err != nil {
		t.Fatalf("Float64Counter: %v", err)
	}
	ctx := context.Background()
	ctr.Add(ctx, 3.7)
	if err := s.Snapshot(ctx); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	latest, _ := s.Latest()
	if got := latest.CounterValue("c.float", ""); got != 3 {
		t.Errorf("float counter snapshot = %d, want 3 (truncated)", got)
	}
}

func TestSnapshotter_LoopTakesPeriodicSnapshots(t *testing.T) {
	s, meter := newSnapshotterWithReader(t)
	ctr, _ := meter.Int64Counter("c.loop")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctr.Add(ctx, 1)
	s.Start(ctx)
	time.Sleep(60 * time.Millisecond)
	cancel()
	if len(s.Samples()) < 2 {
		t.Errorf("len(Samples) = %d, want >= 2 after the loop ran", len(s.Samples()))
	}
}
