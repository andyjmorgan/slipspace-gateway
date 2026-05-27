package proxy

import (
	"net"
	"net/http"
	"time"
)

// DefaultResponseHeaderTimeout caps how long the transport waits for an
// upstream to return its response headers after the request body is fully
// written. It is deliberately generous: a provider under load can take well
// over a minute to emit the first byte of a long completion, and the timeout
// only bounds time-to-first-byte — once headers arrive, streaming bodies are
// not subject to it. New substitutes this when Options.ResponseHeaderTimeout
// is non-positive.
const DefaultResponseHeaderTimeout = 120 * time.Second

// getOrCreateTransport returns the cached transport for baseURL, building one
// on first use. headerTimeout is a per-request override of
// ResponseHeaderTimeout (e.g. a resilience policy's shorter time-to-first-byte
// budget so failover gives up on a slow target fast): a positive value that
// differs from the Forwarder's default yields a transport keyed by
// (baseURL, headerTimeout), so policy-scoped timeouts don't clobber the shared
// default-timeout transport. A zero (or default-equal) value reuses the
// baseURL-keyed default transport.
func (f *Forwarder) getOrCreateTransport(baseURL string, headerTimeout time.Duration) *http.Transport {
	effective := f.responseHeaderTimeout
	key := baseURL
	if headerTimeout > 0 && headerTimeout != f.responseHeaderTimeout {
		effective = headerTimeout
		// NUL separates the timeout suffix so it can't collide with any
		// byte a URL can legally contain.
		key = baseURL + "\x00" + headerTimeout.String()
	}

	f.transportsMu.RLock()
	t, ok := f.transports[key]
	f.transportsMu.RUnlock()
	if ok {
		return t
	}

	f.transportsMu.Lock()
	defer f.transportsMu.Unlock()
	if t, ok := f.transports[key]; ok {
		return t
	}
	t = newTransport(effective)
	f.transports[key] = t
	return t
}

// newTransport builds a fresh upstream transport. responseHeaderTimeout sets
// ResponseHeaderTimeout (time-to-first-byte cap); a non-positive value falls
// back to DefaultResponseHeaderTimeout so a zero-valued Forwarder is still safe.
func newTransport(responseHeaderTimeout time.Duration) *http.Transport {
	if responseHeaderTimeout <= 0 {
		responseHeaderTimeout = DefaultResponseHeaderTimeout
	}
	return &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 50,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}
