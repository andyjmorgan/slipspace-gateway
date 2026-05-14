//go:build e2e

package harness

// Options tweaks the per-test harness configuration. Defaults match the
// production-shaped config-dev/ fixture; flip individual fields to exercise
// alternate code paths (e.g. reporting disabled).
type Options struct {
	// ReportingEnabled overrides gateway.reporting.enabled in the materialized
	// config. Nil = leave the in-tree default (true). Pointer so the
	// zero-value Options{} is "no overrides" rather than "force false".
	ReportingEnabled *bool

	// StashThresholdBytes overrides gateway.reporting.nats.stash_threshold_bytes.
	// 0 = leave default (786432). Useful for the stash test which would
	// otherwise need to ship a >768 KiB payload.
	StashThresholdBytes int

	// DrainTimeoutSeconds overrides gateway.shutdown.drain_timeout_seconds.
	// 0 = leave default. Short values exercise drain-timeout escalation.
	DrainTimeoutSeconds int
}

// BoolPtr returns a pointer to b. Convenience for the Options.ReportingEnabled
// field so tests can write `ReportingEnabled: harness.BoolPtr(false)`.
func BoolPtr(b bool) *bool { return &b }
