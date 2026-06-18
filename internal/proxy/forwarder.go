// Package proxy is the thin wrapper around net/http/httputil.ReverseProxy
// that gives Sluice gateway-shaped ergonomics: per-destination transport
// caches, an Observer seam for telemetry and pipeline integration, and
// structured handling of upstream/transport errors.
package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/internal/headers"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
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

	// observerFactory mints a fresh Observer for every Forward call so
	// implementations can own per-request state as plain struct fields
	// instead of stashing it on context under a mutex. Never nil — New
	// substitutes a no-op factory when callers pass nil.
	observerFactory ObserverFactory

	// redactor masks credential-bearing headers in the proxy's debug
	// log envelopes. Never nil — New substitutes a default
	// (built-ins-only) Redactor when callers pass nil.
	redactor *headers.Redactor

	// responseBodyTransform, when set, is invoked from ModifyResponse
	// for every upstream response. It lets a caller mutate the response
	// body (e.g. the rules engine's response-phase rewrites) without
	// this package importing the rules engine — the closure reads the
	// per-request state off ctx. nil disables the hook.
	responseBodyTransform ResponseBodyTransformer

	// responseHeaderTimeout is stamped onto every lazily-created transport
	// as net/http.Transport.ResponseHeaderTimeout. New substitutes
	// DefaultResponseHeaderTimeout when the Options value is non-positive,
	// so this is always > 0 by the time getOrCreateTransport reads it.
	responseHeaderTimeout time.Duration
}

// ResponseBodyTransformer mutates an upstream response before the client
// sees it. streaming reports whether the response is server-sent events
// (the implementation is expected to leave streaming bodies untouched).
// A non-nil error aborts the response via the proxy's ErrorHandler.
type ResponseBodyTransformer func(ctx context.Context, resp *http.Response, streaming bool) error

// Options configures a new Forwarder. Every field is optional; New
// substitutes safe defaults for any nil/zero values.
type Options struct {
	Logger *slog.Logger

	// ObserverFactory is invoked once per Forward call to produce the
	// Observer that receives lifecycle signals for that single request.
	// A nil factory falls back to one that returns a no-op observer.
	ObserverFactory ObserverFactory

	// Redactor masks credential-bearing headers in the proxy's
	// debug-level header-trace logs. Nil falls back to a default
	// Redactor with the built-in substring list (auth / api-key /
	// token / cookie / secret / sluice-identity). Operator-supplied
	// extras flow through here from cmd/gateway at startup.
	Redactor *headers.Redactor

	// ResponseBodyTransform is invoked from ModifyResponse for every
	// upstream response (see ResponseBodyTransformer). nil disables it.
	ResponseBodyTransform ResponseBodyTransformer

	// ResponseHeaderTimeout caps time-to-first-byte from the upstream — the
	// wait for response headers after the request body is fully written. It
	// is set on every transport this Forwarder mints. A non-positive value
	// falls back to DefaultResponseHeaderTimeout.
	ResponseHeaderTimeout time.Duration
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
// nil ObserverFactory falls back to one that returns a no-op observer.
func New(opts Options) *Forwarder {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	factory := opts.ObserverFactory
	if factory == nil {
		factory = nopObserverFactory
	}
	redactor := opts.Redactor
	if redactor == nil {
		redactor = headers.NewRedactor(nil)
	}
	responseHeaderTimeout := opts.ResponseHeaderTimeout
	if responseHeaderTimeout <= 0 {
		responseHeaderTimeout = DefaultResponseHeaderTimeout
	}
	return &Forwarder{
		transports:            make(map[string]*http.Transport),
		logger:                logger,
		observerFactory:       factory,
		redactor:              redactor,
		responseBodyTransform: opts.ResponseBodyTransform,
		responseHeaderTimeout: responseHeaderTimeout,
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
//
// Accept-Encoding is stripped so upstreams return uncompressed bodies.
// The admin live-messages capture tees the response bytes that flow to
// the client, and compressed bytes render as binary garbage in the
// viewer. Stripping at the request edge means the capture, the upstream
// → gateway hop, and the gateway → client hop all carry plaintext
// without per-hop decompression logic. The .NET predecessor stripped
// Accept-Encoding for the same reason.
var alwaysDropHeaders = []string{
	"X-Sluice-Configuration",
	"X-Sluice-Identity",
	"Authorization",
	"Origin",
	"Referer",
	"Cookie",
	"Accept-Encoding",
}

// Forward sends req upstream as described by dest and writes the response
// to w. It returns a Result describing what the upstream produced and an
// error only when the Destination itself is unusable (missing URLs).
//
// Transport failures (connection refused, header timeout, etc.) flow
// through the configured ErrorHandler, are surfaced via Observer.
// OnUpstreamError, and land in Result.Err. The behaviour of writing a
// 502 to the client depends on w:
//
//   - When w is (or wraps) a *BufferingResponseWriter, the orchestrator
//     owns the response decision — ErrorHandler records the transport
//     error on the buffer via SetTransportError and does NOT write 502.
//     The orchestrator inspects ShouldRetry post-Forward to decide
//     whether to retry the next target.
//   - When w is a bare ResponseWriter (today's single-shot path),
//     ErrorHandler writes 502 Bad Gateway as before — back-compat for
//     callers that have no orchestrator wrapping the writer.
//
// A fresh Observer is minted at the top of Forward via the configured
// ObserverFactory. All four lifecycle hooks fire from this goroutine, so
// Observer implementations may hold per-request state as plain struct
// fields without internal synchronisation:
//
//  1. OnRequestStart fires synchronously before the upstream call.
//  2. OnResponseHeaders fires once the upstream response headers arrive,
//     inside httputil.ReverseProxy.ModifyResponse.
//  3. OnUpstreamError fires instead of OnResponseHeaders on transport
//     failure, inside httputil.ReverseProxy.ErrorHandler.
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
func (f *Forwarder) Forward(ctx context.Context, w http.ResponseWriter, req *http.Request, dest Destination) (*Result, error) {
	if dest.UpstreamURL == nil {
		return nil, errors.New("proxy: forward: destination missing UpstreamURL")
	}
	baseKey := destBaseKey(dest)
	if baseKey == "" {
		return nil, errors.New("proxy: forward: destination missing BaseURL")
	}

	observer := f.observerFactory(ctx, dest)
	if observer == nil {
		observer = nopObserver{}
	}

	headerTimeout, _ := ResponseHeaderTimeoutFromContext(ctx)
	transport := f.getOrCreateTransport(baseKey, headerTimeout)

	dropHeaders := append([]string{}, alwaysDropHeaders...)
	dropHeaders = append(dropHeaders, dest.DropHeaders...)

	reqLogger := observability.FromContext(ctx)
	cw := &statusWriter{
		ResponseWriter: w,
		status:         http.StatusOK,
		ctx:            ctx,
		logger:         reqLogger,
		redactor:       f.redactor,
	}
	cw.onChunk = func() { observer.OnResponseChunk(ctx, time.Now()) }

	// upstreamErr is set by ErrorHandler so the success-log path can skip
	// emission when the transport failed — the error log there is the
	// authoritative line for that case.
	var upstreamErr error

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			if reqLogger.Enabled(ctx, slog.LevelDebug) {
				reqLogger.DebugContext(ctx, "proxy: inbound headers",
					slog.Any("headers", f.redactor.Redact(pr.In.Header)),
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
					slog.Any("headers", f.redactor.Redact(pr.Out.Header)),
				)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			streaming := classifyStreaming(resp)
			cw.streaming = streaming

			if reqLogger.Enabled(ctx, slog.LevelDebug) {
				reqLogger.DebugContext(ctx, "proxy: upstream response headers",
					slog.Int("status_code", resp.StatusCode),
					slog.Bool("streaming", streaming),
					slog.Any("headers", f.redactor.Redact(resp.Header)),
				)
			}

			observer.OnResponseHeaders(ctx, resp.StatusCode, resp.Header, streaming)

			if f.responseBodyTransform != nil {
				if err := f.responseBodyTransform(ctx, resp, streaming); err != nil {
					return err
				}
			}
			return nil
		},
		ErrorHandler: func(rw http.ResponseWriter, _ *http.Request, err error) {
			upstreamErr = err
			observer.OnUpstreamError(ctx, err)
			f.logger.ErrorContext(ctx, "proxy: upstream forward failed",
				slog.String("destination", baseKey),
				slog.Any("error", err),
			)
			// If the writer chain has a BufferingResponseWriter (the
			// v1.2 orchestrator's wrapper), record the error there
			// and leave the response decision to the orchestrator —
			// it will inspect ShouldRetry and either retry the next
			// target or write the final 502 itself. Without the
			// buffer (today's single-shot path), preserve the
			// existing behaviour and write 502 directly.
			if buf := unwrapBufferingResponseWriter(rw); buf != nil {
				buf.SetTransportError(err)
				return
			}
			writeBadGateway(rw)
		},
		Transport:     transport,
		FlushInterval: -1,
	}

	observer.OnRequestStart(ctx, dest)
	start := time.Now()

	// OnComplete is deferred, not called inline, because rp.ServeHTTP can
	// panic: httputil.ReverseProxy raises http.ErrAbortHandler when a streamed
	// response copy fails (client disconnects, or the upstream breaks
	// mid-stream). Inline, that panic would skip OnComplete entirely — leaking
	// the ActiveRequests gauge (the +1 from OnRequestStart never balanced) and
	// dropping the terminal Record for the aborted stream. Deferred, the
	// reporter still settles its state, then the panic continues up to
	// recoverMiddleware. cw.Status() is read at defer time and reflects the
	// final written status either way.
	defer func() {
		observer.OnComplete(ctx, cw.Status(), time.Since(start).Milliseconds())
	}()

	rp.ServeHTTP(cw, req)

	if upstreamErr == nil {
		f.logger.InfoContext(ctx, "proxy: upstream completed",
			slog.String("destination", baseKey),
			slog.String("upstream_url", dest.UpstreamURL.String()),
			slog.Int("status_code", cw.Status()),
			slog.Bool("streaming", cw.Streaming()),
		)
	}
	return &Result{
		StatusCode: cw.Status(),
		Committed:  cw.Committed(),
		Err:        upstreamErr,
	}, nil
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

// sseSniffLimit caps the leading-body peek classifyStreaming performs when the
// upstream omits Content-Type. SSE framing is recognisable from its first few
// bytes (issue #308: the Cloudflare-fronted OpenAI Codex backend streams a real
// event stream with no Content-Type, so the prefix `event: response.created`
// is the only signal available), so a small cap keeps the regression guard cheap.
const sseSniffLimit = 512

// prefixedBody re-presents a partially-read response body as a single stream:
// the bytes already peeked by classifyStreaming, followed by the unread
// remainder. It exists because the Content-Type fallback (issue #308) must
// consume the leading bytes to classify them, yet the downstream proxy copy
// has to see a byte-identical body that still streams incrementally under
// FlushInterval:-1. Close closes the original body, never the peeked prefix.
type prefixedBody struct {
	io.Reader           // MultiReader over the peeked prefix then orig
	orig      io.Closer // the upstream body; closed by Close, not read early
}

// Close closes the original upstream body. The peeked prefix is an in-memory
// bytes.Reader with nothing to release, so only orig is closed here.
func (b *prefixedBody) Close() error { return b.orig.Close() }

// classifyStreaming decides whether an upstream response is an SSE stream.
// isSSE on the headers is the primary fast path, so providers that set
// Content-Type pay zero sniff cost. The fallback exists only for issue #308:
// the Cloudflare-fronted OpenAI Codex backend returns a genuine event stream
// with no Content-Type at all, which isSSE alone classifies as non-streaming —
// dropping gen_ai.request.stream from the span and the TTFC metric. When (and
// only when) Content-Type is empty and a body is present, it peeks the leading
// bytes, restores them so the downstream copy stays byte-identical, and reports
// whether they look like SSE framing.
func classifyStreaming(resp *http.Response) bool {
	if isSSE(resp.Header) {
		return true
	}
	// A non-empty Content-Type that wasn't text/event-stream is authoritative:
	// the header wins and we never touch the body.
	if resp.Header.Get("Content-Type") != "" {
		return false
	}
	if resp.Body == nil || resp.Body == http.NoBody {
		return false
	}

	buf := make([]byte, sseSniffLimit)
	n, err := io.ReadFull(resp.Body, buf)
	peeked := buf[:n]
	// Restore the consumed prefix ahead of the unread remainder so the
	// downstream copy stays byte-identical regardless of the outcome below.
	resp.Body = &prefixedBody{
		Reader: io.MultiReader(bytes.NewReader(peeked), resp.Body),
		orig:   resp.Body,
	}
	// Short bodies are expected, not failures: ErrUnexpectedEOF (read some,
	// then EOF) and EOF (empty) both leave peeked valid. Any other error is a
	// real read failure — classify non-SSE; the restored body still surfaces
	// the same error to the downstream copy.
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return false
	}
	return looksLikeSSE(peeked)
}

// looksLikeSSE reports whether b begins with SSE framing once leading ASCII
// whitespace is trimmed: an event/data/id/retry field, or a `:` comment line
// (heartbeat). JSON and plain text bodies begin with `{`, `[`, or a letter, so
// they never collide with these prefixes.
func looksLikeSSE(b []byte) bool {
	t := bytes.TrimLeft(b, " \t\r\n")
	for _, p := range [][]byte{
		[]byte("event:"),
		[]byte("data:"),
		[]byte("id:"),
		[]byte("retry:"),
		[]byte(":"),
	} {
		if bytes.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

func writeBadGateway(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	_, _ = w.Write([]byte(`{"error":{"type":"upstream_unavailable","message":"upstream provider unreachable"}}`))
}
