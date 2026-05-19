package observability

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Sample captures the in-process Prometheus registry's state at a single
// point in time. Counters carry their cumulative-since-process-start
// value; histograms carry cumulative bucket counts + sum. The aggregator
// subtracts two Samples to derive windowed totals, rates, and quantiles.
type Sample struct {
	// At is the wall-clock time the snapshot was taken.
	At time.Time

	// Counters is metric name → label-key → cumulative count.
	Counters map[string]map[LabelKey]int64

	// Histograms is metric name → label-key → cumulative bucket state.
	Histograms map[string]map[LabelKey]HistogramSnapshot
}

// LabelKey is a deterministic encoding of an attribute set so map
// lookups across snapshots agree on equality. Lower-cardinality than
// the underlying *attribute.Set, which doesn't compare with ==.
type LabelKey string

// Get returns the value of the named attribute in this LabelKey, or
// "" if the attribute is absent. The encoding is "k1=v1,k2=v2" with
// keys sorted; this scan is linear in the number of attributes which
// is fine for the bounded sets our instruments emit.
func (k LabelKey) Get(name string) string {
	if k == "" {
		return ""
	}
	s := string(k)
	prefix := name + "="
	for {
		i := strings.Index(s, prefix)
		if i < 0 {
			return ""
		}
		// Confirm we matched a key boundary (start of string or after
		// a comma), not the tail of another key like "endpoint=foo"
		// when searching for "point".
		if i != 0 && s[i-1] != ',' {
			s = s[i+len(prefix):]
			continue
		}
		val := s[i+len(prefix):]
		if j := strings.Index(val, ","); j >= 0 {
			return val[:j]
		}
		return val
	}
}

// HistogramSnapshot mirrors metricdata.HistogramDataPoint's cumulative
// shape. Bounds are stored explicitly so the aggregator can compute
// quantiles via linear interpolation between bucket edges.
type HistogramSnapshot struct {
	Sum    float64
	Count  uint64
	Bounds []float64
	// Counts has len(Bounds)+1 entries: the i-th value is the cumulative
	// number of observations <= Bounds[i], with the final entry counting
	// the +Inf bucket. Matches OTel's representation.
	Counts []uint64
}

// EmptySample returns a Sample with the maps initialised so callers can
// add to it without nil-map panics.
func EmptySample(at time.Time) Sample {
	return Sample{
		At:         at,
		Counters:   map[string]map[LabelKey]int64{},
		Histograms: map[string]map[LabelKey]HistogramSnapshot{},
	}
}

// CounterValue returns the cumulative value of the named metric at the
// given label set, or 0 if absent. Useful in tests.
func (s Sample) CounterValue(metric string, key LabelKey) int64 {
	m, ok := s.Counters[metric]
	if !ok {
		return 0
	}
	return m[key]
}

// HistogramValue returns the cumulative histogram for (metric, key) or
// the zero value if absent.
func (s Sample) HistogramValue(metric string, key LabelKey) HistogramSnapshot {
	m, ok := s.Histograms[metric]
	if !ok {
		return HistogramSnapshot{}
	}
	return m[key]
}

// EncodeLabels deterministically serialises an attribute set into a
// LabelKey. Keys are sorted so attribute-order changes don't fragment
// the per-series map.
func EncodeLabels(attrs []attribute.KeyValue) LabelKey {
	if len(attrs) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(attrs))
	for _, kv := range attrs {
		pairs = append(pairs, fmt.Sprintf("%s=%s", kv.Key, kv.Value.Emit()))
	}
	sort.Strings(pairs)
	return LabelKey(strings.Join(pairs, ","))
}

// EncodeAttributeSet is EncodeLabels for an OTel attribute.Set
// (the form metricdata exposes).
func EncodeAttributeSet(set attribute.Set) LabelKey {
	if set.Len() == 0 {
		return ""
	}
	return EncodeLabels(set.ToSlice())
}

// Snapshotter periodically reads the in-process metric registry into a
// bounded ring of Samples. The ring is sized so the oldest sample
// covers at least the longest dashboard window (24h by default), with
// a sample cadence chosen to give the SPA enough chart resolution
// without burning memory.
//
// Snapshotter is goroutine-safe; Sample readers grab a read lock to
// copy out the slice they need.
type Snapshotter struct {
	reader   *sdkmetric.ManualReader
	interval time.Duration
	capacity int
	logger   *slog.Logger

	now func() time.Time // injectable for tests

	mu      sync.RWMutex
	samples []Sample // ordered oldest-first; len <= capacity
}

// SnapshotterOptions configures a new Snapshotter.
type SnapshotterOptions struct {
	// Reader is the SDK ManualReader the Snapshotter pulls from.
	// Required.
	Reader *sdkmetric.ManualReader

	// Interval between samples. Default 5m, which matches the SPA's
	// 288-point 24h chart cadence.
	Interval time.Duration

	// Capacity caps the number of stored samples. Default 290 — one
	// 5-minute window per chart point in a 24h view, plus a small
	// margin so a slow consumer doesn't lose the most recent point
	// to a snapshot writer mid-collect.
	Capacity int

	// Now overrides time.Now (test seam). Production should leave nil.
	Now func() time.Time

	// Logger receives any panic recovery in the snapshot goroutine.
	// Defaults to slog.Default() when nil.
	Logger *slog.Logger
}

// NewSnapshotter constructs a Snapshotter from opts. It does NOT start
// the collection loop; call Start with the lifetime context.
func NewSnapshotter(opts SnapshotterOptions) (*Snapshotter, error) {
	if opts.Reader == nil {
		return nil, fmt.Errorf("observability: snapshotter requires a ManualReader")
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	capacity := opts.Capacity
	if capacity <= 0 {
		capacity = 290
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Snapshotter{
		reader:   opts.Reader,
		interval: interval,
		capacity: capacity,
		logger:   logger,
		now:      now,
		samples:  make([]Sample, 0, capacity),
	}, nil
}

// Interval is the configured sampling cadence.
func (s *Snapshotter) Interval() time.Duration { return s.interval }

// Start kicks off the collection loop, returning immediately. The loop
// exits when ctx is cancelled.
func (s *Snapshotter) Start(ctx context.Context) {
	// Take an immediate snapshot so consumers don't have to wait an
	// entire interval before the dashboard renders anything.
	_ = s.Snapshot(ctx)
	go s.runLoop(ctx) //nolint:safego // safego lives in a downstream package; observability cannot import it without a dependency cycle. runLoop carries its own recover().
}

// runLoop is the goroutine entry point. It wraps loop() in a recover()
// so a panic during snapshot/collect lands as a logged error rather
// than tearing the process down.
func (s *Snapshotter) runLoop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.ErrorContext(ctx, "snapshotter goroutine panicked",
				"recover", fmt.Sprintf("%v", r),
				"stack", string(debug.Stack()),
			)
		}
	}()
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.Snapshot(ctx)
		}
	}
}

// Snapshot reads the current state of every instrument from the reader
// and appends it to the ring. Exported so tests (and the immediate
// post-Start call) can trigger a sample without waiting for the timer.
func (s *Snapshotter) Snapshot(ctx context.Context) error {
	var rm metricdata.ResourceMetrics
	if err := s.reader.Collect(ctx, &rm); err != nil {
		return fmt.Errorf("snapshot: collect: %w", err)
	}
	sample := EmptySample(s.now())
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch data := m.Data.(type) {
			case metricdata.Sum[int64]:
				for _, p := range data.DataPoints {
					key := EncodeAttributeSet(p.Attributes)
					if sample.Counters[m.Name] == nil {
						sample.Counters[m.Name] = map[LabelKey]int64{}
					}
					sample.Counters[m.Name][key] = p.Value
				}
			case metricdata.Sum[float64]:
				// Treat float counters as rounded int64 — none
				// in the current registry, but defensive in case
				// a future meter uses Float64Counter.
				for _, p := range data.DataPoints {
					key := EncodeAttributeSet(p.Attributes)
					if sample.Counters[m.Name] == nil {
						sample.Counters[m.Name] = map[LabelKey]int64{}
					}
					sample.Counters[m.Name][key] = int64(p.Value)
				}
			case metricdata.Histogram[float64]:
				for _, p := range data.DataPoints {
					key := EncodeAttributeSet(p.Attributes)
					if sample.Histograms[m.Name] == nil {
						sample.Histograms[m.Name] = map[LabelKey]HistogramSnapshot{}
					}
					sample.Histograms[m.Name][key] = HistogramSnapshot{
						Sum:    p.Sum,
						Count:  p.Count,
						Bounds: append([]float64(nil), p.Bounds...),
						Counts: append([]uint64(nil), p.BucketCounts...),
					}
				}
			}
		}
	}
	s.append(sample)
	return nil
}

func (s *Snapshotter) append(sample Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.samples) < s.capacity {
		s.samples = append(s.samples, sample)
		return
	}
	// Ring eviction: drop the oldest entry, shift left, append.
	copy(s.samples, s.samples[1:])
	s.samples[len(s.samples)-1] = sample
}

// Samples returns a copy of the ring, oldest first. Callers may mutate
// the returned slice without affecting the Snapshotter.
func (s *Snapshotter) Samples() []Sample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Sample, len(s.samples))
	copy(out, s.samples)
	return out
}

// Latest returns the most recent sample and true, or a zero Sample and
// false when the ring is empty.
func (s *Snapshotter) Latest() (Sample, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.samples) == 0 {
		return Sample{}, false
	}
	return s.samples[len(s.samples)-1], true
}

// WindowEnds returns the start and end Samples that cover at least the
// requested window, plus the realised duration between them. When the
// ring has fewer samples than needed, the earliest available sample is
// used (the realised window is shorter than requested — useful for
// "since startup" framing).
//
// Returns ok=false if the ring is empty or has only one sample (no
// delta can be computed).
func (s *Snapshotter) WindowEnds(window time.Duration) (start, end Sample, realised time.Duration, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := len(s.samples)
	if n < 2 {
		return Sample{}, Sample{}, 0, false
	}
	end = s.samples[n-1]
	cutoff := end.At.Add(-window)
	// Find the oldest sample at or after cutoff; if none, fall back to
	// the very first sample.
	startIdx := 0
	for i, sm := range s.samples {
		if !sm.At.Before(cutoff) {
			startIdx = i
			break
		}
		startIdx = i
	}
	start = s.samples[startIdx]
	realised = end.At.Sub(start.At)
	return start, end, realised, true
}
