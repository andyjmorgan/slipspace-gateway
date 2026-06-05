//go:build e2e

// Package harness builds and tears down the moving parts of a black-box E2E
// test: an in-process webhook capture server (httptest.Server), a mockllm
// process, and the gateway binary. Tests construct a Harness via [New]
// and interact with the gateway through [Harness.PostJSON] /
// [Harness.PostStream], and inspect the reporting side-channel via
// [Harness.ExpectEvent] / [Harness.ExpectNoEvent]. The harness wires the
// gateway with a webhook connector (a real-time, non-spooled pusher) pointed
// at its capture server: each request POSTs one contracts/connector.Record as
// JSON, which the harness translates back into the legacy Envelope shape so
// existing tests don't need to change.
//
// Build tag e2e guards the entire package so unit-test builds do not pull
// in testcontainers.
package harness
