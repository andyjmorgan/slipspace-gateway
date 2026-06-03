// Package controlplane implements the connector.Connector that ships sealed
// spool segments to this gateway's own control plane — the CP acting as a spool
// destination (POST /api/v1/ingest/segment). The CP writes each record line to
// request_bodies, the heavy audit/replay payload joined to request_events by
// correlation_id.
//
// It is deliberately a sibling of, not a reuse of, the webhook connector. The
// two differ in exactly the ways that matter for an internal control-plane
// target:
//
//   - Auth is a Bearer token (the CP bootstrap token, the same SLUICE_CP_TOKEN
//     the reconciler presents on the fleet channel), not an HMAC signature.
//   - There is NO SSRF guard. The webhook connector refuses private/loopback
//     addresses to stop a customer-supplied URL exfiltrating to the gateway's
//     own network; the control plane IS that network (a ClusterIP), so the
//     guard would reject every legitimate target.
//
// Everything else — the segment file is buffered and POSTed as ndjson.zst, the
// per-attempt deadline is honoured, retry/permanent classification matches the
// spool's contract — follows the same pattern as the other connectors.
package controlplane
