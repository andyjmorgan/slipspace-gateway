// Package events defines the JSON-encoded payload shapes carried inside
// the gateway's NATS event envelopes. Each top-level type maps 1:1 to a
// subject under the gateway. prefix (e.g. Request → gateway.request,
// RuleMatched → gateway.rule.matched).
//
// The schemas live in a public package so downstream consumers — the v1.1
// control plane subscriber, the SQLite mirror, the Web UI, and external
// tooling — can decode bus traffic against a typed contract rather than
// re-deriving the shape from documentation. JSON tags are the source of
// truth; field additions are backward-compatible (omitempty on optional
// fields), removals and renames are breaking.
package events
