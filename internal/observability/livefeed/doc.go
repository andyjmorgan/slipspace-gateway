// Package livefeed is the in-process live tail of completed requests
// that backs the admin console's "Live messages" pane.
//
// Each completed request appends one Entry to a bounded Ring. Operators
// open the pane in the SPA, which fetches the current ring via the
// /admin/api/v1/messages/recent endpoint and then subscribes to
// /admin/api/v1/messages/stream (server-sent events) for new entries
// as they happen.
//
// The ring lives entirely in process memory. Restart wipes it; this is
// not the audit trail (the connector spool under internal/spool carries
// that). The capacity is deliberately small — single-deployment scope,
// single-pod operator view, sized for a few minutes of tail rather than
// long history.
//
// Append never blocks the request goroutine. Per-subscriber channels
// are buffered and drop on full so a slow SSE client cannot stall the
// ring writer.
package livefeed
