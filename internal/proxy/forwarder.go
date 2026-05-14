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
//
// A single Forwarder is shared across requests. Transports are keyed by
// upstream base URL so connection pools and HTTP/2 reuse survive across
// requests targeting the same provider.
type Forwarder struct {
	// transports is the per-base-URL connection-pool cache. Entries are
	// created lazily by getOrCreateTransport and never evicted — the cache
	// is bounded by the number of distinct upstream base URLs in the
	// resolved config, which is small (single digits per gateway).
	transports map[string]*http.Transport

	// transportsMu guards transports. Reads dominate writes (the cache is
	// effectively populated once at warm-up), so it is an RWMutex.
	transportsMu sync.RWMutex

	// logger is owned by the Forwarder and used for upstream-error logs.
	// It carries no per-request enrichment; per-request loggers travel on
	// context via observability.WithLogger.
	logger *slog.Logger

	// observer receives lifecycle signals so telemetry and (in a follow-up
	// wave) the typed-message pipeline can react to forwards without
	// coupling to the proxy internals. Never nil — New substitutes a no-op
	// when callers pass nil.
	observer Observer
}

// Options configures a new Forwarder. Both fields are optional; New
// substitutes slog.Default and a no-op Observer for nil values.
type Options struct {
	Logger *slog.Logger

	Observer Observer
}

// Destination describes the resolved upstream target for a single Forward
// call: the upstream URL the proxy should hit, the headers to set on the
// outgoing request (post auth-swap), and the headers to drop.
//
// Header precedence: alwaysDropHeaders ∪ DropHeaders are removed first,
// then OutgoingHeaders are applied (overwriting any value that survived
// the drop step). Effectively OutgoingHeaders wins over DropHeaders for
// any key that appears in both.
type Destination struct {
	// BaseURL is the upstream scheme+host used as the transport cache key.
	// When BaseURL is nil or hostless the cache key falls back to the
	// scheme+host of UpstreamURL.
	BaseURL *url.URL

	// UpstreamURL is the fully resolved target URL — scheme, host, path,
	// and (optional) raw query — the proxy rewrites the outgoing request
	// to. Required.
	UpstreamURL *url.URL

	// OutgoingHeaders are headers set on the outgoing request after the
	// drop step. Auth middleware uses this to inject the upstream
	// Authorization header for managed-mode requests.
	OutgoingHeaders http.Header

	// DropHeaders are extra header names removed from the outgoing
	// request, in addition to alwaysDropHeaders. Applied before
	// OutgoingHeaders, so any key in both still ends up set.
	DropHeaders []string
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

// Forward sends req upstream as described by dest and writes the response
// to w. It returns an error only when the Destination is unusable;
// transport failures flow through the configured ErrorHandler and are
// surfaced via Observer.OnUpstreamError, with a 502 Bad Gateway written to
// the client.
//
// Observer lifecycle for a single call:
//
//  1. OnRequestStart fires synchronously before the upstream call.
//  2. OnResponseHeaders fires once the upstream response headers arrive.
//  3. OnUpstreamError fires instead of OnResponseHeaders on transport
//     failure.
//  4. OnComplete fires once after ServeHTTP returns, carrying the captured
//     final status (via statusWriter) and total wall-clock duration.
//
// Two details are load-bearing:
//
//   - FlushInterval is set to -1 so httputil.ReverseProxy flushes after
//     every write, which is required for the SSE streaming used by all
//     three providers — without it, chunks are buffered and clients see
//     batched events.
//   - statusWriter wraps w to capture the final HTTP status; httputil
//     does not expose it on the response we hand back through
//     ModifyResponse alone, because ErrorHandler may have written a
//     different code.
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
