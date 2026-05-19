//go:build e2e

package harness

// Options tweaks the per-test harness configuration. Defaults match the
// production-shaped config-dev/ fixture; flip individual fields to exercise
// alternate code paths (e.g. reporting disabled).
//
// Each override is applied as a SLUICE_* env var at gateway spawn time
// rather than as a YAML mutation, since server-level configuration moved
// out of gateway.yaml in the three-plane refactor.
type Options struct {
	// ReportingEnabled controls whether SLUICE_NATS_URL is set on the
	// spawned gateway. Nil = enabled (default). Pointer so the
	// zero-value Options{} is "no overrides" rather than "force false".
	ReportingEnabled *bool

	// StashThresholdBytes sets SLUICE_NATS_STASH_THRESHOLD_BYTES. 0 = leave
	// default (786432). Useful for the stash test which would otherwise
	// need to ship a >768 KiB payload.
	StashThresholdBytes int

	// DrainTimeoutSeconds sets SLUICE_SHUTDOWN_DRAIN_SECONDS. 0 = leave
	// default. Short values exercise drain-timeout escalation.
	DrainTimeoutSeconds int

	// PolicyYAML, when non-empty, replaces the policy.yaml content the
	// harness would otherwise copy from config-dev/. Tests that need
	// custom rules (terminating actions, multi-rule priority ordering,
	// etc.) supply a full policy.yaml here; providers.yaml is still
	// copied from config-dev/ so the upstream wiring stays consistent.
	PolicyYAML string

	// AdminEnabled flips the management console on for this harness.
	// The harness always allocates a free port for the admin listener
	// — collision-free across parallel tests — and writes an admin.yaml
	// into the materialized config dir. Disabled by default so existing
	// tests pay no extra setup cost.
	AdminEnabled bool

	// AdminPassword sets the operator credential the harness will issue
	// to the gateway. Empty defaults to "test-password" when
	// AdminEnabled. Ignored when AdminEnabled is false.
	AdminPassword string
}

// BoolPtr returns a pointer to b. Convenience for the Options.ReportingEnabled
// field so tests can write `ReportingEnabled: harness.BoolPtr(false)`.
func BoolPtr(b bool) *bool { return &b }
