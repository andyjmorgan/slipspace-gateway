// Package events defines the JSON-encoded payload shapes the gateway
// uses for in-process audit events fed to the live-messages ring and
// the e2e harness translation layer. Each top-level type historically
// mapped 1:1 to a subject under the gateway. prefix (e.g.
// Request → gateway.request, RuleMatched → gateway.rule.matched); the
// subject-style naming is retained for harness compatibility even
// though the NATS bus has been replaced by the connector pipeline.
//
// The schemas live in a public package so downstream consumers — the
// admin console SPA, the live-messages SSE stream, and external
// tooling that decodes harness output — can decode against a typed
// contract rather than re-deriving the shape from documentation.
// JSON tags are the source of truth; field additions are
// backward-compatible (omitempty on optional fields), removals and
// renames are breaking.
package events
