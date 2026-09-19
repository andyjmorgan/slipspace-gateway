package proxy

import "net/http"

// Result reports the outcome of a single Forward call. The resilience
// orchestrator inspects it to decide whether to commit to the client's
// response or try the next target. Every gateway request goes through
// the orchestrator; direct callers and tests that pass a bare
// ResponseWriter can ignore Result — ErrorHandler writes the 502 on
// transport error in that case.
//
// Field semantics:
//
//   - StatusCode is the upstream's HTTP status as observed by the
//     forwarder's internal statusWriter. Zero when no response was
//     observed (transport error, hang). Defaulted to 200 when the
//     upstream emitted no explicit status — matching net/http's
//     implicit-200-on-first-Write contract.
//   - Committed is true when WriteHeader (or implicit-WriteHeader via
//     Write) was observed. Together with StatusCode, this lets the
//     orchestrator distinguish "upstream responded with N" from "no
//     response received."
//   - Err is the transport-level error captured from ReverseProxy's
//     ErrorHandler — non-nil only when the upstream call failed
//     before any response was received. A 5xx from the upstream is
//     NOT a transport error; it lands in StatusCode with Err nil.
type Result struct {
	StatusCode int

	Committed bool

	Err error
}

// unwrapBufferingResponseWriter walks the standard "Unwrap" chain
// (mirroring net/http.ResponseController) to find a wrapped
// *BufferingResponseWriter, returning nil when none is in the chain.
// Used by Forward's ErrorHandler to deliver transport errors to the
// orchestrator's buffering layer when one is in use.
func unwrapBufferingResponseWriter(rw http.ResponseWriter) *BufferingResponseWriter {
	for {
		if b, ok := rw.(*BufferingResponseWriter); ok {
			return b
		}
		u, ok := rw.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil
		}
		inner := u.Unwrap()
		if inner == rw {
			return nil
		}
		rw = inner
	}
}
