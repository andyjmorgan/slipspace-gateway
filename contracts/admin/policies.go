package admin

// PoliciesResponse is the JSON shape returned by
// GET /admin/api/v1/policies. One row per configured resilience
// policy, with each row carrying its targets and the current
// per-pod circuit-breaker state for each (policy, target) pair the
// in-process store has observed. Targets the breaker has not yet
// seen report state="closed" — matches BreakerStore.State semantics.
//
// The SPA renders this as a read-only policies page in v1.2; v1.3+
// adds edit-in-place once the control-plane mutators land.
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

// PolicySummary is one resilience policy's surface — name, mode, the
// list of targets, and per-target breaker state. Fields mirror
// contracts/resilience.ResilienceConfig but project to a read-friendly
// shape (no nested action polymorphism, no per-policy CB cfg in this
// summary — those are part of the v1.3 detail endpoint).
type PolicySummary struct {
	// Name is the policy identifier used by rules to bind via
	// useResiliencePolicy.
	Name string `json:"name"`

	// Mode is the orchestration mode: "failover", "load_balance",
	// "load_balance_with_failover", or "none".
	Mode string `json:"mode"`

	// StrictWeights mirrors the policy's strict_weights flag. When
	// true the orchestrator does not re-roll on failure (canary
	// semantics).
	StrictWeights bool `json:"strict_weights,omitempty"`

	// FailureStatusCodes is the policy-level retry set. Empty when
	// the policy falls back to the orchestrator default 5xx-class.
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

	// Order is the failover target order (lower is tried first).
	// Always zero in load_balance modes.
	Order int `json:"order,omitempty"`

	// Weight is the load_balance weighted-random share. Always zero
	// in failover mode.
	Weight int `json:"weight,omitempty"`

	// CircuitState is the current breaker state per the in-process
	// store: "closed", "open", "half_open", or "unknown" when the
	// store has not observed this (policy, target) pair yet. The
	// SPA renders this as a coloured badge in the per-target row.
	CircuitState string `json:"circuit_state"`
}
