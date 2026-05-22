// Package spool implements the on-disk write buffer that sits between
// the gateway's body-capture middleware and the upload workers that ship
// sealed segments to connector destinations.
//
// The package ships in two layers. This PR lands the disk-level
// primitives:
//
//   - Segment — one ndjson.zst file currently being written, with
//     rotation triggers and frame validation.
//   - Manager — the active/sealed/uploading/deadletter/quarantine
//     directory layout per connector, with atomic-rename state
//     transitions.
//   - Recover — startup scan that restores invariants after a crash
//     (uploading → sealed, torn active → quarantine).
//
// A later PR adds the in-memory ring, the Start/Stop lifecycle, and the
// fan-out across connectors. Those decisions depend on how body capture
// hands buffers to the spool, which is the subject of the next PR in
// the series.
//
// Loss policy is best-effort: rotation does an fsync; crash mid-write
// can lose the current segment's unflushed tail (sub-MB typically).
// Disk full is the caller's responsibility — Segment.Write surfaces
// the os error and the caller decides whether to drop oldest, sleep,
// or panic.
package spool
