package proxy

import (
	"net"
	"net/http"
	"time"
)

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
	t = newTransport()
	f.transports[baseURL] = t
	return t
}

func newTransport() *http.Transport {
	return &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 50,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}
