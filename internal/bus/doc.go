// Package bus is the side-channel reporting publisher. It accepts
// Envelope values via a non-blocking Publish call, serializes them as
// MessagePack, and ships them to a JetStream subject. Payloads larger than
// the configured threshold are stashed in an Object Store bucket and the
// envelope carries an ObjectRef instead of the inline bytes.
//
// The package exists to enforce one invariant: the request path never
// blocks on reporting. If the worker queue is full, the publish fails,
// the dispatch fails, or NATS is unreachable, the event is dropped and
// the drop counter advances. There is no retry, no backoff, no in-memory
// replay buffer — the next event is always more valuable than this one.
//
// The wire format is documented in the "NATS Reporting (Envelope Pattern)"
// design note. The matching test-side decoder lives in
// test/e2e/harness/events.go.
package bus
