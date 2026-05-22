package admin

import (
	"net/http"
	"os"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	contractsres "github.com/andyjmorgan/sluice-gateway/contracts/resilience"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

// CircuitBreakerStateSource is the read interface PoliciesHandler
// needs to project per-target breaker state into the response. The
// orchestrator's BreakerStore satisfies it; tests use a stub. Kept
// minimal so the admin package doesn't import the resilience
// middleware (which would pull the entire orchestrator into the admin
// build graph for what is otherwise a read-only DTO mapping).
type CircuitBreakerStateSource interface {
	// State returns the current breaker lifecycle position for one
	// (policy, target) pair. Implementations report "closed" for
	// unknown pairs.
	State(policy, target string) string
}

// PoliciesHandler serves GET /admin/api/v1/policies. Returns 503 when
// resolved is nil (no config bound) so a partial boot still produces
// a clean response. The CB source may be nil — every target then
// reports state="unknown", letting the SPA boot before the
// orchestrator is fully wired.
func PoliciesHandler(resolved *config.ResolvedConfig, cb CircuitBreakerStateSource) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if resolved == nil {
			http.Error(w, "config not loaded", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, buildPoliciesResponse(resolved, cb))
	})
}

// buildPoliciesResponse projects resolved + cb into the wire DTO. Kept
// pure so the unit tests can exercise it without spinning up an HTTP
// recorder.
func buildPoliciesResponse(resolved *config.ResolvedConfig, cb CircuitBreakerStateSource) adminc.PoliciesResponse {
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	out := adminc.PoliciesResponse{
		Pod:      host,
		Policies: make([]adminc.PolicySummary, 0, len(resolved.ResiliencePolicies)),
	}
	for i := range resolved.ResiliencePolicies {
		out.Policies = append(out.Policies, summarisePolicy(resolved.ResiliencePolicies[i], cb))
	}
	return out
}

func summarisePolicy(pol contractsres.ResilienceConfig, cb CircuitBreakerStateSource) adminc.PolicySummary {
	summary := adminc.PolicySummary{
		Name:               pol.Name,
		Mode:               string(pol.Mode),
		StrictWeights:      pol.StrictWeights,
		FailureStatusCodes: append([]int(nil), pol.FailureStatusCodes...),
		Targets:            make([]adminc.PolicyTarget, 0, len(pol.Targets)),
	}
	if pol.CircuitBreaker != nil && pol.CircuitBreaker.Enabled {
		summary.CircuitBreakerEnabled = true
	}
	for _, t := range pol.Targets {
		summary.Targets = append(summary.Targets, summariseTarget(pol.Name, t, cb))
	}
	return summary
}

func summariseTarget(policy string, t contractsres.ResilienceTarget, cb CircuitBreakerStateSource) adminc.PolicyTarget {
	tgt := adminc.PolicyTarget{
		Name:         t.Name,
		Provider:     t.Provider,
		Order:        t.Order,
		Weight:       t.Weight,
		CircuitState: "unknown",
	}
	if cb != nil {
		tgt.CircuitState = cb.State(policy, t.Name)
	}
	return tgt
}
