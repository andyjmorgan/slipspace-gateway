package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Connector-spool instruments. All are observable: one callback per
// collection reads the spool's per-track counter snapshot and emits a
// point per track, so the spool's hot path (Enqueue, drain, upload)
// touches no meter and the request path pays nothing for observability
// (invariant #2). Cardinality is bounded by the operator's connector
// list; reason / outcome / state_name are fixed vocabularies.
//
// These exist because the loss they expose was previously invisible: the
// per-track counters incremented into an unexported struct nobody read
// (#560), so a ring overflow or a deadlettering destination looked
// exactly like a healthy spool.
const (
	// MetricSpoolEnqueuedTotal counts records accepted onto a track's
	// ring by Spool.Enqueue.
	MetricSpoolEnqueuedTotal = "gateway.spool.enqueued.total"

	// MetricSpoolDroppedTotal counts records the spool lost before they
	// reached disk, by connector + reason: ring_full (the per-track ring
	// was at capacity — drain is slower than ingest) or no_track (the
	// binding named a connector with no registered track — a live-added
	// connector whose track failed to build, or a stale binding). Any
	// non-zero rate is audit-record loss.
	MetricSpoolDroppedTotal = "gateway.spool.dropped.total"

	// MetricSpoolWrittenTotal counts records the drain goroutine wrote
	// into a segment.
	MetricSpoolWrittenTotal = "gateway.spool.written.total"

	// MetricSpoolWriteErrorsTotal counts records lost because the
	// segment write failed (disk full, unwritable spool root).
	MetricSpoolWriteErrorsTotal = "gateway.spool.write_errors.total"

	// MetricSpoolSegmentsSealedTotal counts non-empty segments moved to
	// sealed/ and handed to the uploader.
	MetricSpoolSegmentsSealedTotal = "gateway.spool.segments_sealed.total"

	// MetricSpoolUploadsTotal counts upload outcomes by connector +
	// outcome: ok (segment delivered), retried (a retryable attempt
	// failure — degradation, not yet loss), dlq (segment moved to
	// deadletter/ — loss until an operator replays it).
	MetricSpoolUploadsTotal = "gateway.spool.uploads.total"

	// MetricSpoolBreakerState is the per-destination circuit breaker's
	// current state: 0=closed, 1=half_open, 2=open. Distinct from
	// gateway.cb.state (the resilience middleware's upstream breaker).
	MetricSpoolBreakerState = "gateway.spool.breaker.state"

	// MetricSpoolPendingSegments is the number of sealed segments
	// awaiting upload — the on-disk backlog. Grows while the breaker is
	// open or the destination is slow; a steadily climbing value is the
	// early warning before disk-full write errors.
	MetricSpoolPendingSegments = "gateway.spool.pending_segments"
)

// Spool metric attribute values. Fixed vocabularies so dashboards can
// match on them.
const (
	// SpoolDropReasonRingFull labels a drop at Enqueue because the
	// track's ring was at capacity.
	SpoolDropReasonRingFull = "ring_full"
	// SpoolDropReasonNoTrack labels a drop at Enqueue because no track
	// of that name was registered.
	SpoolDropReasonNoTrack = "no_track"
	// SpoolUploadOutcomeOK labels a delivered segment.
	SpoolUploadOutcomeOK = "ok"
	// SpoolUploadOutcomeRetried labels a retryable attempt failure.
	SpoolUploadOutcomeRetried = "retried"
	// SpoolUploadOutcomeDLQ labels a segment moved to deadletter/.
	SpoolUploadOutcomeDLQ = "dlq"
)

// SpoolTrackSnapshot is one connector's spool counters at collection
// time, mirrored at the observability boundary so the callback can emit
// them without importing internal/spool (which imports safego, which
// imports this package). cmd/gateway adapts spool.Stats into it.
type SpoolTrackSnapshot struct {
	// Connector is the track / connector name.
	Connector string
	// Registered reports whether a track of this name exists. False
	// rows carry only DroppedNoTrack — records bound to a name with no
	// sink — and emit nothing else.
	Registered bool
	// Enqueued is the ring-accept count.
	Enqueued int64
	// DroppedRing is the ring-full drop count.
	DroppedRing int64
	// DroppedNoTrack is the no-registered-track drop count.
	DroppedNoTrack int64
	// Written is the records-written-to-segment count.
	Written int64
	// WriteErrors is the segment-write-failure count.
	WriteErrors int64
	// SegmentsSealed is the sealed-segment count.
	SegmentsSealed int64
	// UploadsOK is the delivered-segment count.
	UploadsOK int64
	// UploadsRetried is the retryable-attempt-failure count.
	UploadsRetried int64
	// UploadsDLQ is the deadlettered-segment count.
	UploadsDLQ int64
	// BreakerState is the numeric breaker state (0=closed, 1=half_open,
	// 2=open).
	BreakerState int64
	// BreakerStateName is the state_name attribute value.
	BreakerStateName string
	// PendingSegments is the sealed/ backlog depth.
	PendingSegments int64
}

// SpoolStatsSource is the read interface RegisterSpoolInstruments needs
// at collection time. The gateway's adapter over *spool.Spool implements
// it; the indirection keeps this package free of the spool import cycle.
type SpoolStatsSource interface {
	// Snapshot returns one row per connector the spool knows about —
	// every registered track plus every unregistered name records were
	// bound to. Must be safe to call from the collection goroutine.
	Snapshot() []SpoolTrackSnapshot
}

// RegisterSpoolInstruments wires the gateway.spool.* observable
// instruments against source. Returns no error when source is nil — the
// instruments are omitted, useful in test contexts without a spool. The
// pod label on the two gauges uses podID so multi-replica deployments
// can tell whose backlog a series describes; the counters carry no pod
// label because the Prometheus scrape and the OTLP resource already
// identify the instance and the values are cumulative per process.
func RegisterSpoolInstruments(meter metric.Meter, source SpoolStatsSource, podID string) error {
	if source == nil {
		return nil
	}
	if meter == nil {
		return fmt.Errorf("observability: meter is required for spool instruments")
	}

	counter := func(name, desc string) (metric.Int64ObservableCounter, error) {
		c, err := meter.Int64ObservableCounter(name, metric.WithDescription(desc), metric.WithUnit("1"))
		if err != nil {
			return nil, fmt.Errorf("observability: register %s: %w", name, err)
		}
		return c, nil
	}
	// The gauges deliberately carry no unit: the Prometheus bridge
	// renders a unit-"1" gauge as <name>_ratio, which is wrong for a
	// state enum and a backlog depth and hides the series from anyone
	// grepping for the documented name.
	gauge := func(name, desc string) (metric.Int64ObservableGauge, error) {
		g, err := meter.Int64ObservableGauge(name, metric.WithDescription(desc))
		if err != nil {
			return nil, fmt.Errorf("observability: register %s: %w", name, err)
		}
		return g, nil
	}

	enqueued, err := counter(MetricSpoolEnqueuedTotal, "Records accepted onto a connector spool track's ring.")
	if err != nil {
		return err
	}
	dropped, err := counter(MetricSpoolDroppedTotal, "Records the connector spool lost before disk, by connector and reason (ring_full, no_track).")
	if err != nil {
		return err
	}
	written, err := counter(MetricSpoolWrittenTotal, "Records the spool drain wrote into a segment.")
	if err != nil {
		return err
	}
	writeErrors, err := counter(MetricSpoolWriteErrorsTotal, "Records lost because the spool segment write failed.")
	if err != nil {
		return err
	}
	sealed, err := counter(MetricSpoolSegmentsSealedTotal, "Spool segments sealed and handed to the uploader.")
	if err != nil {
		return err
	}
	uploads, err := counter(MetricSpoolUploadsTotal, "Spool segment upload outcomes, by connector and outcome (ok, retried, dlq).")
	if err != nil {
		return err
	}
	breaker, err := gauge(MetricSpoolBreakerState, "Per-connector spool circuit-breaker state: 0=closed, 1=half_open, 2=open.")
	if err != nil {
		return err
	}
	pending, err := gauge(MetricSpoolPendingSegments, "Sealed spool segments awaiting upload, per connector.")
	if err != nil {
		return err
	}

	cb := spoolInstrumentsCallback(source, podID, spoolInstruments{
		enqueued: enqueued, dropped: dropped, written: written, writeErrors: writeErrors,
		sealed: sealed, uploads: uploads, breaker: breaker, pending: pending,
	})
	if _, err := meter.RegisterCallback(cb,
		enqueued, dropped, written, writeErrors, sealed, uploads, breaker, pending); err != nil {
		return fmt.Errorf("observability: register spool callback: %w", err)
	}
	return nil
}

// spoolInstruments bundles the observables the single spool callback
// writes to, so the callback signature stays readable.
type spoolInstruments struct {
	enqueued, dropped, written, writeErrors, sealed, uploads metric.Int64ObservableCounter
	breaker, pending                                         metric.Int64ObservableGauge
}

// spoolInstrumentsCallback closes over source + podID and returns the
// collection callback that observes every spool instrument from one
// Snapshot call. Named so the closure is identifiable in a stack trace.
func spoolInstrumentsCallback(source SpoolStatsSource, podID string, ins spoolInstruments) metric.Callback {
	return func(_ context.Context, o metric.Observer) error {
		for _, row := range source.Snapshot() {
			conn := attribute.String("connector", row.Connector)
			if row.DroppedNoTrack > 0 || !row.Registered {
				o.ObserveInt64(ins.dropped, row.DroppedNoTrack, metric.WithAttributes(conn,
					attribute.String("reason", SpoolDropReasonNoTrack)))
			}
			if !row.Registered {
				continue
			}
			o.ObserveInt64(ins.enqueued, row.Enqueued, metric.WithAttributes(conn))
			o.ObserveInt64(ins.dropped, row.DroppedRing, metric.WithAttributes(conn,
				attribute.String("reason", SpoolDropReasonRingFull)))
			o.ObserveInt64(ins.written, row.Written, metric.WithAttributes(conn))
			o.ObserveInt64(ins.writeErrors, row.WriteErrors, metric.WithAttributes(conn))
			o.ObserveInt64(ins.sealed, row.SegmentsSealed, metric.WithAttributes(conn))
			o.ObserveInt64(ins.uploads, row.UploadsOK, metric.WithAttributes(conn,
				attribute.String("outcome", SpoolUploadOutcomeOK)))
			o.ObserveInt64(ins.uploads, row.UploadsRetried, metric.WithAttributes(conn,
				attribute.String("outcome", SpoolUploadOutcomeRetried)))
			o.ObserveInt64(ins.uploads, row.UploadsDLQ, metric.WithAttributes(conn,
				attribute.String("outcome", SpoolUploadOutcomeDLQ)))
			o.ObserveInt64(ins.breaker, row.BreakerState, metric.WithAttributes(conn,
				attribute.String("pod", podID),
				attribute.String("state_name", row.BreakerStateName)))
			o.ObserveInt64(ins.pending, row.PendingSegments, metric.WithAttributes(conn,
				attribute.String("pod", podID)))
		}
		return nil
	}
}
