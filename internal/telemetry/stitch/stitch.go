// Package stitch assembles the correlated views the console renders from the
// separate feeds the store holds: a per-request view (the request_event joined
// to its large payloads, keyed by correlation_id) and a per-session rollup
// (every request sharing a session_id, plus aggregate totals). The join keys
// are correlation_id (one request) and session_id (a conversation); ordering of
// payloads across instances follows the stable (ts_ns, instance_id, seq) key
// the store already applies (invariant #8).
package stitch

import (
	"encoding/json"

	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

// RequestView is one request stitched together: the lean event plus the latest
// captured payload of each kind. Payloads maps a store.Kind* to the raw body.
type RequestView struct {
	Event    store.RequestEvent         `json:"event"`
	Payloads map[string]json.RawMessage `json:"payloads,omitempty"`
}

// LatestPayloadsByKind picks the most recent payload per kind. The store returns
// payloads already in (ts_ns, instance_id, seq) order, so the last one seen for
// a kind is the most complete (e.g. the final response over a retried one).
func LatestPayloadsByKind(payloads []store.Payload) map[string]json.RawMessage {
	if len(payloads) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(payloads))
	for _, p := range payloads {
		out[p.Kind] = json.RawMessage(p.Body)
	}
	return out
}

// BuildRequestView stitches an event and its payloads into a RequestView.
func BuildRequestView(event store.RequestEvent, payloads []store.Payload) RequestView {
	return RequestView{Event: event, Payloads: LatestPayloadsByKind(payloads)}
}

// SessionTotals is the aggregate rollup over a session's requests.
type SessionTotals struct {
	Requests  int   `json:"requests"`
	Errors    int   `json:"errors"`
	TokensIn  int64 `json:"tokens_in"`
	TokensOut int64 `json:"tokens_out"`
}

// SessionView is every request in a session plus its aggregate totals.
type SessionView struct {
	SessionID string               `json:"session_id"`
	Requests  []store.RequestEvent `json:"requests"`
	Totals    SessionTotals        `json:"totals"`
}

// BuildSessionView groups a session's events (assumed oldest-first) and computes
// its totals. A request with status >= 400 counts as an error.
func BuildSessionView(sessionID string, events []store.RequestEvent) SessionView {
	v := SessionView{SessionID: sessionID, Requests: events}
	for _, e := range events {
		v.Totals.Requests++
		if e.StatusCode >= 400 {
			v.Totals.Errors++
		}
		v.Totals.TokensIn += e.TokensIn
		v.Totals.TokensOut += e.TokensOut
	}
	return v
}
