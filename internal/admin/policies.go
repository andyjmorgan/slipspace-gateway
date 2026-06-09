package admin

import (
	"net/http"
	"os"
	"sort"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
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
// the store is nil (no config bound) so a partial boot still produces
// a clean response. The CB source may be nil — every target then
// reports state="unknown", letting the SPA boot before the
// orchestrator is fully wired.
//
// The handler snapshots the store once at the top of the request and
// reads through that snapshot for the rest of the call.
func PoliciesHandler(store *config.Store, cb CircuitBreakerStateSource) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resolved := snapshot(store)
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
	names := make([]string, 0, len(resolved.Groups))
	for name := range resolved.Groups {
		names = append(names, name)
	}
	sort.Strings(names)
	out := adminc.PoliciesResponse{
		Pod:      host,
		Policies: make([]adminc.PolicySummary, 0, len(names)),
	}
	for _, name := range names {
		out.Policies = append(out.Policies, summariseGroup(name, resolved.Groups[name], cb))
	}
	return out
}

// summariseGroup projects a v2 resilience group onto the policy DTO. Group
// targets are providers (with an optional model alias); per-attempt circuit
// state is keyed by (group, provider).
func summariseGroup(name string, g contractsconfig.Group, cb CircuitBreakerStateSource) adminc.PolicySummary {
	summary := adminc.PolicySummary{
		Name:               name,
		Mode:               string(g.Mode),
		FailureStatusCodes: append([]int(nil), g.FailureStatusCodes...),
		Targets:            make([]adminc.PolicyTarget, 0, len(g.Targets)),
	}
	if g.CircuitBreaker != nil && g.CircuitBreaker.Enabled {
		summary.CircuitBreakerEnabled = true
	}
	for i, t := range g.Targets {
		summary.Targets = append(summary.Targets, summariseGroupTarget(name, i+1, t, cb))
	}
	return summary
}

func summariseGroupTarget(group string, order int, t contractsconfig.Target, cb CircuitBreakerStateSource) adminc.PolicyTarget {
	tgt := adminc.PolicyTarget{
		Name:         t.Provider,
		Provider:     t.Provider,
		Order:        order,
		Weight:       t.Weight,
		CircuitState: "unknown",
	}
	if cb != nil {
		tgt.CircuitState = cb.State(group, t.Provider)
	}
	return tgt
}
