// Package stitch assembles the correlated views the console renders. In the
// single-writer model the entity (request_events) is the OTel span; the Record
// is a lazy verbatim blob joined by correlation_id only when an inspector tab
// opens. This package builds the per-request inspector view (the event + the
// optionally-present decoded record) and the per-session rollup (every request
// sharing a session_id, plus aggregate totals).
package stitch

import (
	cc "github.com/andyjmorgan/slipspace-gateway/contracts/connector"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
)

// RequestView is one request stitched together for the inspector: the entity
// event (span scalars + the span_event blob) plus the lazily-joined record. The
// record is the gateway's verbatim cc.Record — present only when reporting
// forwarding was on for the request; nil otherwise.
type RequestView struct {
	Event  store.RequestEvent `json:"event"`
	Record *cc.Record         `json:"record,omitempty"`
}

// BuildRequestView stitches an event and its (optional) decoded record into a
// RequestView. A nil record means reporting was off / the push hasn't arrived.
func BuildRequestView(event store.RequestEvent, record *cc.Record) RequestView {
	return RequestView{Event: event, Record: record}
}

// SessionTotals is the aggregate rollup over a session's requests.
type SessionTotals struct {
	Requests  int   `json:"requests"`
	Errors    int   `json:"errors"`
	TokensIn  int64 `json:"tokens_in"`
	TokensOut int64 `json:"tokens_out"`
	// CostUSD is the session's summed estimated spend, from each event's
	// span blob (same source as the token totals here). Zero when costing
	// was off for every request.
	CostUSD float64 `json:"cost_usd"`
}

// SessionView is every request in a session plus its aggregate totals.
type SessionView struct {
	SessionID string               `json:"session_id"`
	Requests  []store.RequestEvent `json:"requests"`
	Totals    SessionTotals        `json:"totals"`
}

// BuildSessionView groups a session's events (assumed oldest-first) and computes
// its totals. A request with status >= 400 counts as an error; token totals are
// read out of each event's span_event blob (no promoted token columns).
func BuildSessionView(sessionID string, events []store.RequestEvent) SessionView {
	v := SessionView{SessionID: sessionID, Requests: events}
	for _, e := range events {
		v.Totals.Requests++
		if e.StatusCode >= 400 {
			v.Totals.Errors++
		}
		f := e.DecodeSpanFields()
		v.Totals.TokensIn += f.TokensIn
		v.Totals.TokensOut += f.TokensOut
		v.Totals.CostUSD += f.CostUSD
	}
	return v
}
