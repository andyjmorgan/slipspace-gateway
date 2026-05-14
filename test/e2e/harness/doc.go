//go:build e2e

// Package harness builds and tears down the moving parts of a black-box E2E
// test: a JetStream-enabled NATS via testcontainers, a mockllm process, and
// the gateway binary. Tests construct a Harness via [New] and interact with
// the gateway through [Harness.PostJSON] / [Harness.PostStream], and inspect
// the reporting bus via [Harness.ExpectEvent] / [Harness.ExpectNoEvent].
//
// Build tag e2e guards the entire package so unit-test builds do not pull in
// testcontainers, the NATS client, or msgpack.
package harness
