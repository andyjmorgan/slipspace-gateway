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

func (f *Forwarder) getOrCreateTransport(baseURL string) *http.Transport {
	f.transportsMu.RLock()
	t, ok := f.transports[baseURL]
	f.transportsMu.RUnlock()
	if ok {
		return t
	}

	f.transportsMu.Lock()
	defer f.transportsMu.Unlock()
	if t, ok := f.transports[baseURL]; ok {
		return t
	}
	t = newTransport(f.responseHeaderTimeout)
	f.transports[baseURL] = t
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
