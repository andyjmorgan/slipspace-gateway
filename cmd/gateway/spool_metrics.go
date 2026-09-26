package main

import (
	"os"
	"sort"

	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
	"github.com/andyjmorgan/slipspace-gateway/internal/spool"
)

// registerSpoolInstruments mounts the gateway.spool.* observable
// instruments against s via the observability helper. No-op when there
// is no spool or no meter provider (test contexts). The pod label uses
// the process hostname, falling back to "unknown" like the cb.state
// gauge so the label set is always complete.
func registerSpoolInstruments(obs *observability.Provider, s *spool.Spool) error {
	if obs == nil || s == nil || obs.MeterProvider == nil {
		return nil
	}
	meter := obs.MeterProvider.Meter(observability.MeterName)
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return observability.RegisterSpoolInstruments(meter, spoolStatsAdapter{spool: s}, host)
}

// spoolStatsAdapter bridges spool.Stats to observability.SpoolStatsSource
// so the observability package stays free of the spool import (spool →
// safego → observability would otherwise cycle).
type spoolStatsAdapter struct {
	spool *spool.Spool
}

// Snapshot converts one spool.Stats() call into the row-per-connector
// shape the metric callback emits: every registered track, plus a
// Registered=false row for each name that received records with no
// track to route them to. Rows are sorted by connector name so the
// emitted series order is stable across collections.
func (a spoolStatsAdapter) Snapshot() []observability.SpoolTrackSnapshot {
	st := a.spool.Stats()
	rows := make([]observability.SpoolTrackSnapshot, 0, len(st.Tracks)+len(st.Unrouted))
	for name, ts := range st.Tracks {
		rows = append(rows, observability.SpoolTrackSnapshot{
			Connector:        name,
			Registered:       true,
			Enqueued:         clampUint64(ts.Enqueued),
			DroppedRing:      clampUint64(ts.DroppedRing),
			DroppedNoTrack:   clampUint64(st.Unrouted[name]),
			Written:          clampUint64(ts.Written),
			WriteErrors:      clampUint64(ts.WriteErrors),
			SegmentsSealed:   clampUint64(ts.SegmentsSealed),
			UploadsOK:        clampUint64(ts.UploadsOK),
			UploadsRetried:   clampUint64(ts.UploadsRetried),
			UploadsDLQ:       clampUint64(ts.UploadsDLQ),
			BreakerState:     int64(ts.BreakerState),
			BreakerStateName: ts.BreakerState.String(),
			PendingSegments:  int64(ts.PendingSegments),
		})
	}
	for name, n := range st.Unrouted {
		if _, registered := st.Tracks[name]; registered {
			continue // already carried on the track's row
		}
		rows = append(rows, observability.SpoolTrackSnapshot{
			Connector:      name,
			DroppedNoTrack: clampUint64(n),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Connector < rows[j].Connector })
	return rows
}

// clampUint64 narrows a uint64 counter to the int64 the OTel API takes.
// The counters cannot realistically reach 2^63, but a saturating
// conversion is safer than a silent wraparound to negative.
func clampUint64(v uint64) int64 {
	const maxInt64 = uint64(1<<63 - 1)
	if v > maxInt64 {
		return int64(maxInt64)
	}
	return int64(v)
}
