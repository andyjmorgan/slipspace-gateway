// Package proxy is the thin wrapper around net/http/httputil.ReverseProxy
// that gives Sluice gateway-shaped ergonomics: per-destination transport
// caches, an Observer seam for telemetry and pipeline integration, and
// structured handling of upstream/transport errors.
package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Forwarder wraps httputil.ReverseProxy with per-destination transports and
// structured error handling. The pipeline integration is a follow-up: the
// Observer seam is the bridge.
type Forwarder struct {
	transports   map[string]*http.Transport
	transportsMu sync.RWMutex
	logger       *slog.Logger
	observer     Observer
}

// Options configures a new Forwarder.
type Options struct {
	Logger   *slog.Logger
	Observer Observer
}

// Destination describes the resolved upstream target for a single Forward
// call: the upstream URL the proxy should hit, the headers to set on the
// outgoing request (post auth-swap), and the headers to drop.
type Destination struct {
	BaseURL         *url.URL
	UpstreamURL     *url.URL
	OutgoingHeaders http.Header
	DropHeaders     []string
}

// New constructs a Forwarder. A nil Logger falls back to slog.Default and a
// nil Observer falls back to a no-op.
func New(opts Options) *Forwarder {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	observer := opts.Observer
	if observer == nil {
		observer = nopObserver{}
	}
	return &Forwarder{
		transports: make(map[string]*http.Transport),
		logger:     logger,
		observer:   observer,
	}
}

// alwaysDropHeaders are headers the gateway never forwards upstream. The
// Authorization header must be re-set by the auth middleware via
// Destination.OutgoingHeaders for managed-mode requests.
var alwaysDropHeaders = []string{
	"X-Sluice-Configuration",
	"Authorization",
}

// Forward sends req upstream as described by dest and writes the response to
// w. It returns an error only when the Destination is unusable; transport
// failures flow through the configured ErrorHandler and are surfaced via the
// Observer.
func (f *Forwarder) Forward(ctx context.Context, w http.ResponseWriter, req *http.Request, dest Destination) error {
	if dest.UpstreamURL == nil {
		return errors.New("proxy: forward: destination missing UpstreamURL")
	}
	baseKey := destBaseKey(dest)
	if baseKey == "" {
		return errors.New("proxy: forward: destination missing BaseURL")
	}

	transport := f.getOrCreateTransport(baseKey)

	dropHeaders := append([]string{}, alwaysDropHeaders...)
	dropHeaders = append(dropHeaders, dest.DropHeaders...)

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			rewriteURL(pr, dest.UpstreamURL)
			for _, h := range dropHeaders {
				pr.Out.Header.Del(h)
			}
			for k, vs := range dest.OutgoingHeaders {
				cloned := make([]string, len(vs))
				copy(cloned, vs)
				pr.Out.Header[http.CanonicalHeaderKey(k)] = cloned
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			f.observer.OnResponseHeaders(ctx, resp.StatusCode, resp.Header, isSSE(resp.Header))
			return nil
		},
		ErrorHandler: func(rw http.ResponseWriter, _ *http.Request, err error) {
			f.observer.OnUpstreamError(ctx, err)
			f.logger.ErrorContext(ctx, "proxy: upstream forward failed",
				slog.String("destination", baseKey),
				slog.Any("error", err),
			)
			writeBadGateway(rw)
		},
		Transport:     transport,
		FlushInterval: -1,
	}

	cw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	f.observer.OnRequestStart(ctx, dest)
	start := time.Now()

	rp.ServeHTTP(cw, req)

	f.observer.OnComplete(ctx, cw.status, time.Since(start).Milliseconds())
	return nil
}

func destBaseKey(dest Destination) string {
	if dest.BaseURL != nil && dest.BaseURL.Host != "" {
		return dest.BaseURL.String()
	}
	if dest.UpstreamURL != nil && dest.UpstreamURL.Host != "" {
		return dest.UpstreamURL.Scheme + "://" + dest.UpstreamURL.Host
	}
	return ""
}

func rewriteURL(pr *httputil.ProxyRequest, target *url.URL) {
	pr.Out.URL.Scheme = target.Scheme
	pr.Out.URL.Host = target.Host
	pr.Out.URL.Path = target.Path
	pr.Out.URL.RawPath = target.RawPath
	if target.RawQuery != "" {
		pr.Out.URL.RawQuery = target.RawQuery
	}
	pr.Out.Host = ""
}

func isSSE(h http.Header) bool {
	ct := h.Get("Content-Type")
	if ct == "" {
		return false
	}
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.EqualFold(strings.TrimSpace(ct), "text/event-stream")
}

func writeBadGateway(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	_, _ = w.Write([]byte(`{"error":{"type":"upstream_unavailable","message":"upstream provider unreachable"}}`))
}
