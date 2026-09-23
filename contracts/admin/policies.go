package admin

// PoliciesResponse is the JSON shape returned by
// GET /admin/api/v1/policies. One row per configured resilience
// policy, with each row carrying its targets and the current
// per-pod circuit-breaker state for each (policy, target) pair the
// in-process store has observed. Targets the breaker has not yet
// seen report state="closed" — matches BreakerStore.State semantics.
//
// The SPA renders this as the policies overview, with the live
// per-pod breaker state; editing a group is a link out to the group
// editor backed by the /api/v1/config/groups CRUD endpoints, while
// the richer live-breaker projection stays here on /api/v1/policies.
type PoliciesResponse struct {
	// Pod is the gateway hostname the response originated from.
	// Multi-pod deployments fetch from each replica to assemble a
	// cluster view; in single-pod operation this is identical across
	// every call.
	Pod string `json:"pod"`

	// Policies is the list of configured resilience policies, in
	// declaration order from the merged YAML.
	Policies []PolicySummary `json:"policies"`
}

// PolicySummary is one v2 resilience group's surface — name, mode, the
// list of targets, and per-target breaker state. Fields mirror
// contracts/resilience.ResilienceConfig but project to a read-friendly
// shape (no nested action polymorphism, no per-group CB cfg in this
// summary — those are available from the group CRUD endpoints,
// /api/v1/config/groups/{name}).
type PolicySummary struct {
	// Name is the group name from the top-level groups block;
	// configurations reach it through a binding's group field (the
	// v1 useResiliencePolicy rule action is inert in v2).
	Name string `json:"name"`

	// Mode is the orchestration mode: "failover", "load_balance",
	// "load_balance_with_failover", or "none".
	Mode string `json:"mode"`

	// StrictWeights mirrors the policy's strict_weights flag. When
	// true the orchestrator does not re-roll on failure (canary
	// semantics).
	StrictWeights bool `json:"strict_weights,omitempty"`

	// FailureStatusCodes is the policy-level retry set. Empty when
	// the policy falls back to the orchestrator default retry set
	// [500, 502, 503, 504] (defaultFailureStatusCodes in
	// internal/middleware/resilience/middleware.go) — not every 5xx.
	FailureStatusCodes []int `json:"failure_status_codes,omitempty"`

	// CircuitBreakerEnabled is true when the policy declares a
	// circuit_breaker block with enabled=true. Surfaced separately
	// from the per-target state so operators can tell config from
	// observed health at a glance.
	CircuitBreakerEnabled bool `json:"circuit_breaker_enabled,omitempty"`

	// Targets is the resolved per-target list in policy order.
	Targets []PolicyTarget `json:"targets"`
}

// PolicyTarget is one entry inside PolicySummary.Targets. Carries
// the target's static configuration plus the dynamic per-pod CB state.
type PolicyTarget struct {
	// Name is the target identifier.
	Name string `json:"name"`

	// Provider is the upstream provider name the target routes to
	// when no per-target Actions override it.
	Provider string `json:"provider,omitempty"`

	// Order is the target's 1-based declaration position within the
	// group, populated in every mode. Declaration order is what drives
	// failover sequencing (lower is tried first); it carries no meaning
	// in load_balance modes.
	Order int `json:"order,omitempty"`

	// Weight is the authored per-target load_balance share, passed
	// through verbatim in every mode. The orchestrator ignores it in
	// failover mode.
	Weight int `json:"weight,omitempty"`

	// CircuitState is the current breaker state per the in-process
	// store: "closed", "open", or "half_open". A (policy, target)
	// pair the store has not observed yet reports "closed" —
	// BreakerStore.State semantics. "unknown" appears only when the
	// gateway has no breaker source wired at all (partial boot),
	// never per-target. The SPA renders this as a coloured badge in
	// the per-target row.
	CircuitState string `json:"circuit_state"`
}
