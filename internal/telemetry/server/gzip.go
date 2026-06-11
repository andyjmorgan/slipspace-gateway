package server

import (
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// This file is the console API's response compression. The query surface
// serves large, highly repetitive JSON (span pages, event lists) that gzips
// >10x, and the prod console fronts the service over WAN links where those
// megabytes dominate page latency. Compression is negotiated per request via
// Accept-Encoding; clients that don't ask (or ask with q=0) get identity
// bytes, so curl/jq workflows keep working unchanged. Static SPA assets and
// the open probe/ingest routes are NOT wrapped — only the Basic-auth-gated
// query handlers (see registerQueryRoutes).

// gzipPool recycles gzip writers across requests; allocating the deflate
// state (hundreds of KB) per response would otherwise dominate small
// responses. BestSpeed keeps CPU off the latency path — the console's JSON
// compresses >10x even at the fastest level, and higher levels only buy a
// few percent more.
var gzipPool = sync.Pool{
	New: func() any {
		zw, err := gzip.NewWriterLevel(nil, gzip.BestSpeed)
		if err != nil {
			// Unreachable: BestSpeed is a valid constant level.
			panic(err)
		}
		return zw
	},
}

// gzipResponseWriter routes the handler's body writes through the gzip
// stream while headers/status pass straight to the wrapped writer.
type gzipResponseWriter struct {
	http.ResponseWriter
	zw *gzip.Writer
}

// Write compresses the handler's body bytes into the underlying writer.
func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	return g.zw.Write(b)
}

// withGzip wraps next with negotiated gzip response compression. The
// Content-Encoding header is set before the handler runs (the handlers
// stream JSON straight to the writer, so there is no later point to decide),
// and Vary: Accept-Encoding keeps shared caches honest. Handlers under this
// middleware must not set Content-Length — none of the console handlers do.
func withGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		zw := gzipPool.Get().(*gzip.Writer)
		zw.Reset(w)
		defer func() {
			// Close flushes the gzip trailer; without it the client sees a
			// truncated stream. Errors here mean the connection already died.
			_ = zw.Close()
			gzipPool.Put(zw)
		}()
		next.ServeHTTP(&gzipResponseWriter{ResponseWriter: w, zw: zw}, r)
	})
}

// acceptsGzip reports whether the request negotiates gzip: an
// Accept-Encoding member named gzip whose q-value (when present) is
// non-zero. Deliberately minimal — wildcard (*) and q-value ranking across
// codings are ignored since gzip is the only encoding we offer.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc, params, hasQ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(enc), "gzip") {
			continue
		}
		if !hasQ {
			return true
		}
		q := strings.TrimSpace(params)
		if v, ok := strings.CutPrefix(q, "q="); ok {
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			return err == nil && f > 0
		}
		return true
	}
	return false
}
