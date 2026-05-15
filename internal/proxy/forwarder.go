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

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
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

// alwaysDropHeaders are headers the gateway never forwards upstream.
//
// Authorization and X-Sluice-Configuration carry the gateway's own auth
// state and must not propagate; the auth middleware re-injects the
// upstream credential via Destination.OutgoingHeaders for managed mode,
// and the cmd/gateway destination builder re-adds the inbound
// Authorization verbatim for passthrough mode.
//
// Origin, Referer, and Cookie are browser-session state. They have no
// meaning to upstream LLM APIs and, worse, trigger provider-side
// browser-CORS detection — Anthropic in particular rejects any request
// carrying a browser Origin header for organisations with custom
// retention policy. We strip them in both managed and passthrough modes
// because the gateway is the upstream's client, not the user's browser.
var alwaysDropHeaders = []string{
	"X-Sluice-Configuration",
	"Authorization",
	"Origin",
	"Referer",
	"Cookie",
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

	reqLogger := observability.FromContext(ctx)
	cw := &statusWriter{
		ResponseWriter: w,
		status:         http.StatusOK,
		ctx:            ctx,
		logger:         reqLogger,
	}

	// upstreamErr is set by ErrorHandler so the success-log path can skip
	// emission when the transport failed — the error log there is the
	// authoritative line for that case.
	var upstreamErr error

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			if reqLogger.Enabled(ctx, slog.LevelDebug) {
				reqLogger.DebugContext(ctx, "proxy: inbound headers",
					slog.Any("headers", redactSensitive(pr.In.Header)),
				)
			}

			rewriteURL(pr, dest.UpstreamURL)

			var dropped []string
			for _, h := range dropHeaders {
				if pr.Out.Header.Get(h) != "" {
					pr.Out.Header.Del(h)
					dropped = append(dropped, h)
				}
			}
			if len(dropped) > 0 && reqLogger.Enabled(ctx, slog.LevelDebug) {
				reqLogger.DebugContext(ctx, "proxy: stripped headers",
					slog.Any("headers", dropped),
				)
			}

			for k, vs := range dest.OutgoingHeaders {
				cloned := make([]string, len(vs))
				copy(cloned, vs)
				pr.Out.Header[http.CanonicalHeaderKey(k)] = cloned
			}

			if reqLogger.Enabled(ctx, slog.LevelDebug) {
				reqLogger.DebugContext(ctx, "proxy: outbound headers",
					slog.String("destination", dest.UpstreamURL.String()),
					slog.Any("headers", redactSensitive(pr.Out.Header)),
				)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			streaming := isSSE(resp.Header)
			cw.streaming = streaming

			if reqLogger.Enabled(ctx, slog.LevelDebug) {
				reqLogger.DebugContext(ctx, "proxy: upstream response headers",
					slog.Int("status_code", resp.StatusCode),
					slog.Bool("streaming", streaming),
					slog.Any("headers", redactSensitive(resp.Header)),
				)
			}

			f.observer.OnResponseHeaders(ctx, resp.StatusCode, resp.Header, streaming)
			return nil
		},
		ErrorHandler: func(rw http.ResponseWriter, _ *http.Request, err error) {
			upstreamErr = err
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

	f.observer.OnRequestStart(ctx, dest)
	start := time.Now()

	rp.ServeHTTP(cw, req)

	f.observer.OnComplete(ctx, cw.Status(), time.Since(start).Milliseconds())

	if upstreamErr == nil {
		f.logger.InfoContext(ctx, "proxy: upstream completed",
			slog.String("destination", baseKey),
			slog.String("upstream_url", dest.UpstreamURL.String()),
			slog.Int("status_code", cw.Status()),
			slog.Bool("streaming", cw.Streaming()),
		)
	}
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

// redactSensitive returns a shallow copy of h with values replaced by
// "[REDACTED]" for any header name that looks credential-bearing.
//
// Match is substring-based on the lowercased name so it catches every
// variant in one rule: Authorization, Proxy-Authorization, X-Api-Key,
// x-api-key, Anthropic-Api-Key, X-Goog-Api-Key, Cookie, Set-Cookie,
// X-Auth-Token, X-CSRF-Token, X-API-Secret, etc. — without enumerating
// each. Over-redacting in logs is the safer error.
//
// Used only by the debug-level header trace logs in Forward; production
// log output at info level is unaffected.
func redactSensitive(h http.Header) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		if isSensitiveHeaderName(k) {
			masked := make([]string, len(vs))
			for i := range vs {
				masked[i] = "[REDACTED]"
			}
			out[k] = masked
			continue
		}
		cloned := make([]string, len(vs))
		copy(cloned, vs)
		out[k] = cloned
	}
	return out
}

// isSensitiveHeaderName reports whether a header name looks credential-
// bearing under a permissive lowercase-substring match.
func isSensitiveHeaderName(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "auth") ||
		strings.Contains(lower, "api-key") ||
		strings.Contains(lower, "apikey") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "cookie") ||
		strings.Contains(lower, "secret")
}
