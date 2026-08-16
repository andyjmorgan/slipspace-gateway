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
//   - Options, BackOff, CircuitBreaker — configuration and resilience
//     policies for individual upload attempts.
//
// Loss policy is best-effort: rotation does an fsync; crash mid-write
// can lose the current segment's unflushed tail (sub-MB typically).
// Disk full is the caller's responsibility — Segment.Write surfaces
// the os error and the caller decides whether to drop oldest, sleep,
// or panic.
package spool
