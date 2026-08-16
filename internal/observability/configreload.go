package observability

import (
	"context"
	"sync/atomic"

	"go.opentelemetry.io/otel/metric"
)

// ConfigReloadCounter returns the callback that increments counter once per
// live configuration swap, for wiring into config.Store.Subscribe.
//
// Subscribe deliberately invokes a new subscriber immediately with the
// current snapshot, so that consumers start from a consistent state rather
// than waiting for the first replacement. That initial call is a
// registration artefact, not a reload — counting it would report one swap
// on a gateway that has never had its configuration changed. The returned
// callback swallows exactly the first invocation and counts every one after
// it.
//
// The counter is the only observability signal for the admin write API,
// which applies edits live (CLAUDE.md invariant #9). It is deliberately
// unlabelled: which block changed is an audit-trail question the connector
// record answers, not a metric dimension.
//
// A nil counter is a no-op, matching the rest of the meters surface, so
// callers constructed without telemetry need not short-circuit.
func ConfigReloadCounter(ctx context.Context, counter metric.Int64Counter) func() {
	var registered atomic.Bool
	return func() {
		// Swap returns the previous value: false exactly once, on the
		// immediate call Subscribe makes at registration.
		if !registered.Swap(true) {
			return
		}
		if counter == nil {
			return
		}
		counter.Add(ctx, 1)
	}
}
