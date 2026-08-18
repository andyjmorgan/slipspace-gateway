// Package spool implements the on-disk write buffer that sits between
// the gateway's body-capture middleware and the upload workers that ship
// sealed segments to connector destinations.
//
// The package implements two layers of primitives:
//
// Disk-level:
//
//   - Segment — one ndjson.zst file currently being written, with
//     rotation triggers and frame validation.
//   - Manager — the active/sealed/uploading/deadletter/quarantine
//     directory layout per connector, with atomic-rename state
//     transitions.
//   - Recover — startup scan that restores invariants after a crash
//     (uploading → sealed, torn active → quarantine).
//
// In-memory runtime:
//
//   - Spool — the root lifecycle manager with Start/Stop.
//   - Track — per-connector ring buffer with rotation, retry, and
//     circuit-breaker orchestration.
//   - Options, RegisterTrackOptions, RotationOpts, RetryOpts,
//     BreakerOpts — configuration for the spool, its tracks, and the
//     retry/circuit-breaker policies applied to individual upload
//     attempts. The backoff and breaker mechanics themselves are
//     unexported.
//
// Loss policy is best-effort: rotation does an fsync; crash mid-write
// can lose the current segment's unflushed tail (sub-MB typically).
// Disk full is not the caller's problem to solve — a failed segment
// write bumps the track's writeErrors counter and the record is lost.
// Enqueue is fire-and-forget and returns nothing, so there is no
// drop-oldest/sleep/panic choice to hand back; the request path must
// never block on the spool.
package spool
