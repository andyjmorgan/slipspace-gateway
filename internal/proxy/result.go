package proxy

import "net/http"

// Result reports the outcome of a single Forward call. The resilience
// orchestrator inspects it to decide whether to commit to the client's
// response or try the next target — but only for failover and
// load-balance attempts, which run behind a BufferingResponseWriter.
// Passthrough requests and single-target (ModeNone) policies forward
// through a bare ResponseWriter, where ErrorHandler writes the 502
// upstream_unavailable body directly on a transport error.
//
// Field semantics:
//
//   - StatusCode is the upstream's HTTP status as observed by the
//     forwarder's internal statusWriter. The statusWriter starts at 200
//     (net/http's implicit-200-on-first-Write contract), so StatusCode
//     is never zero. After a transport error behind a
//     BufferingResponseWriter it reads 200 with Committed false and Err
//     non-nil; with a bare writer it reads 502. Callers must tell "no
//     response" apart via Err/Committed, never StatusCode == 0.
//   - Committed is true when WriteHeader (or implicit-WriteHeader via
//     Write) was observed. Together with Err, this lets the
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
