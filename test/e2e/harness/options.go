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
	// ReportingEnabled controls whether the harness injects a webhook
	// connector binding into the dev configuration. Nil = enabled
	// (default). Pointer so the zero-value Options{} is "no overrides"
	// rather than "force false". When false the gateway runs with no
	// connector binding and ExpectEvent reliably times out — exercises
	// the no-capture path.
	ReportingEnabled *bool

	// DrainTimeoutSeconds sets SLUICE_SHUTDOWN_DRAIN_SECONDS. 0 = leave
	// default. Short values exercise drain-timeout escalation.
	DrainTimeoutSeconds int

	// UpstreamResponseHeaderTimeoutSeconds sets
	// SLUICE_UPSTREAM_RESPONSE_HEADER_TIMEOUT_SECONDS. 0 = leave default
	// (120). Values below the 120 floor are rejected by the gateway at
	// startup, so this is only useful for asserting a valid override
	// threads through the binary without breaking forwarding.
	UpstreamResponseHeaderTimeoutSeconds int

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

	// ExternalURL sets SLUICE_EXTERNAL_URL on the spawned gateway,
	// resolving the {external_url} template reference used by
	// response-side body rewrites. Empty leaves it unset.
	ExternalURL string

	// WebhookMaxBodyBytes, when > 0, sets max_body_bytes on the injected
	// harness-webhook connector binding so a test can exercise the explicit
	// per-binding cap. 0 (default) leaves the binding's cap unset, which under
	// the no-default-cap policy means bodies ship intact. Ignored when
	// reporting is disabled.
	WebhookMaxBodyBytes int
}

// BoolPtr returns a pointer to b. Convenience for the Options.ReportingEnabled
// field so tests can write `ReportingEnabled: harness.BoolPtr(false)`.
func BoolPtr(b bool) *bool { return &b }
