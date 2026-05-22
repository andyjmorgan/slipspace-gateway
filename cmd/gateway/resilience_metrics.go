package main

import (
	"context"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	resiliencemw "github.com/andyjmorgan/sluice-gateway/internal/middleware/resilience"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// circuitBreakerTransitionListener returns the StateListener wired into
// NewInMemoryBreakerStore so every breaker state change bumps
// gateway.cb.transitions.total{policy, target, to_state}. Returns nil
// when meters is nil (test contexts), so the store defaults to its
// internal no-op listener.
func circuitBreakerTransitionListener(meters *observability.Meters) resiliencemw.StateListener {
	if meters == nil || meters.CircuitBreakerTransitionTotal == nil {
		return nil
	}
	return func(policy, target string, _, to resiliencemw.State, _ string) {
		// The breaker's StateListener fires from inside breaker.mu and
		// does not carry a context — transitions are state-machine
		// events, not request-scoped. context.Background() is
		// deliberate here.
		meters.CircuitBreakerTransitionTotal.Add(context.Background(), 1, metric.WithAttributes( //nolint:contextcheck // listener has no ctx; transition is a state-machine event
			attribute.String("policy", policy),
			attribute.String("target", target),
			attribute.String("to_state", to.String()),
		))
	}
}

// registerBreakerStateGauge mounts the gateway.cb.state ObservableGauge
// against the supplied store via the observability helper. The pod
// label uses the process hostname; when unresolvable we fall back to
// "unknown" so the gauge always emits a complete labelset.
func registerBreakerStateGauge(obs *observability.Provider, store resiliencemw.BreakerStore) error {
	if obs == nil || store == nil {
		return nil
	}
	if obs.MeterProvider == nil {
		return nil
	}
	meter := obs.MeterProvider.Meter(observability.MeterName)
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return observability.RegisterCircuitBreakerStateGauge(meter, breakerStoreAdapter{store: store}, host)
}

// breakerStoreAdapter bridges resilience.BreakerStore to two consumer
// interfaces in one type:
//   - observability.CircuitBreakerStateSource (gauge callback, via
//     Snapshot)
//   - admin.CircuitBreakerStateSource (policies endpoint, via State)
//
// Keeps the observability and admin packages free of the resilience
// middleware import while exposing the same data the orchestrator
// already tracks.
type breakerStoreAdapter struct {
	store resiliencemw.BreakerStore
}

func (a breakerStoreAdapter) Snapshot() []observability.CircuitBreakerSnapshot {
	src := a.store.Snapshot()
	out := make([]observability.CircuitBreakerSnapshot, 0, len(src))
	for _, s := range src {
		out = append(out, observability.CircuitBreakerSnapshot{
			Policy:    s.Policy,
			Target:    s.Target,
			State:     int64(s.State),
			StateName: s.State.String(),
		})
	}
	return out
}

// State satisfies admin.CircuitBreakerStateSource. Delegates straight
// to the underlying store's State which returns the State enum;
// converted to the canonical lower-case string the SPA expects.
func (a breakerStoreAdapter) State(policy, target string) string {
	return a.store.State(policy, target).String()
}
