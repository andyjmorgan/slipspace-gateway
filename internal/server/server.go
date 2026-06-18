// Package server wraps net/http.Server with the gateway's graceful-shutdown
// lifecycle: a context-driven drain that flips /healthz to 503 before
// http.Server.Shutdown begins, plus a hard Server.Close fallback if drain
// exceeds the configured timeout. Run is the single entrypoint and blocks
// until the supplied context is cancelled (typically by signal.NotifyContext).
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
	"github.com/andyjmorgan/slipspace-gateway/internal/safego"
)

// DefaultDrainTimeout matches Helm's terminationGracePeriodSeconds: 330 with
// a 30s buffer for the kubelet's own SIGKILL deadline.
const DefaultDrainTimeout = 300 * time.Second

// Options configures a Server. Bind is required; the rest have sensible
// fallbacks (DrainTimeout → DefaultDrainTimeout, Logger → slog.Default,
// ReadHeaderTimeout → 10s).
type Options struct {
	// Bind is the listen address in net.Listen("tcp", ...) form, e.g.
	// ":8080" or "127.0.0.1:0". An empty string causes Run to fail.
	Bind string

	// Handler is the application handler mounted at "/". Requests to
	// "/healthz" are intercepted before they reach it. A nil Handler is
	// permitted (and exercised by tests); only /healthz responds.
	Handler http.Handler

	// DrainTimeout caps how long graceful shutdown waits for in-flight
	// requests to finish after /healthz flips to draining. On overrun the
	// server hard-closes remaining connections.
	DrainTimeout time.Duration

	// ReadHeaderTimeout is set on the underlying http.Server. Defaults to
	// 10s when zero — leaving it unset would trip linters.
	ReadHeaderTimeout time.Duration

	Logger *slog.Logger

	// Meters is the gateway's metrics bundle. Used only to record
	// panics caught by the Serve-goroutine safego wrap. Nil-safe.
	Meters *observability.Meters
}

// Server wraps net/http.Server with drain lifecycle and an automatically
// mounted /healthz endpoint.
type Server struct {
	// httpSrv is owned by the Server. Construction wires the /healthz
	// mux ahead of opts.Handler; callers must not reach in and replace
	// the Handler after New.
	httpSrv *http.Server

	bind string

	// drain is the deadline applied to http.Server.Shutdown after
	// /healthz has been flipped to 503.
	drain time.Duration

	logger *slog.Logger

	// meters is retained for the Serve-goroutine safego wrap so a
	// panic inside http.Server.Serve increments the goroutine-panics
	// counter (very unlikely, but the invariant is no goroutine without
	// recover). Nil-tolerant.
	meters *observability.Meters

	// healthz is the readiness probe handler. Mounted on the internal
	// mux at construction and exposed via Healthz() so tests can inspect
	// drain transitions without hitting the HTTP surface.
	healthz *HealthzHandler

	// addrMu guards addr. Run writes addr once at startup; Addr reads it
	// concurrently from tests that bind to ":0" and need the assigned
	// port.
	addrMu sync.RWMutex
	addr   string
}

// New builds a Server. The /healthz endpoint is mounted in front of the
// caller's Handler — requests to /healthz are intercepted; everything else
// falls through to opts.Handler.
func New(opts Options) *Server {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	drain := opts.DrainTimeout
	if drain <= 0 {
		drain = DefaultDrainTimeout
	}

	readHeader := opts.ReadHeaderTimeout
	if readHeader <= 0 {
		readHeader = 10 * time.Second
	}

	healthz := NewHealthzHandler()

	mux := http.NewServeMux()
	mux.Handle("/healthz", healthz)
	if opts.Handler != nil {
		mux.Handle("/", opts.Handler)
	}

	httpSrv := &http.Server{
		Addr:              opts.Bind,
		Handler:           mux,
		ReadHeaderTimeout: readHeader,
	}

	return &Server{
		httpSrv: httpSrv,
		bind:    opts.Bind,
		drain:   drain,
		logger:  logger,
		meters:  opts.Meters,
		healthz: healthz,
	}
}

// Addr returns the bound address after Run has started accepting. Useful for
// tests that bind to ":0" and need the assigned port. Returns the empty
// string before the listener is opened.
func (s *Server) Addr() string {
	s.addrMu.RLock()
	defer s.addrMu.RUnlock()
	return s.addr
}

// Healthz returns the underlying /healthz handler. Exposed primarily so
// tests can inspect drain transitions without parsing HTTP responses.
func (s *Server) Healthz() *HealthzHandler {
	return s.healthz
}

// Run blocks until ctx is cancelled (typically by signal.NotifyContext) or
// the listener dies. It returns nil on clean shutdown, or a wrapped error
// when drain exceeded the timeout or the listener failed unexpectedly.
//
// The shutdown sequence is:
//  1. ctx is cancelled
//  2. /healthz flips to 503 (so k8s readiness probes fail and load balancers stop sending traffic)
//  3. http.Server.Shutdown begins draining in-flight requests
//  4. If drain exceeds s.drain, Server.Close hard-kills remaining connections
func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.bind)
	if err != nil {
		return fmt.Errorf("server: listen %s: %w", s.bind, err)
	}

	s.addrMu.Lock()
	s.addr = listener.Addr().String()
	s.addrMu.Unlock()

	// BaseContext propagates the per-server context to every accepted
	// connection so request handlers see ctx.Done when shutdown begins.
	s.httpSrv.BaseContext = func(_ net.Listener) context.Context { return ctx }

	listenErr := make(chan error, 1)
	safego.Go(ctx, "server.serve", s.logger, s.meters, func() {
		s.logger.InfoContext(ctx, "server listening", "addr", s.addr)
		serveErr := s.httpSrv.Serve(listener)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			listenErr <- serveErr
		}
		close(listenErr)
	})

	select {
	case err, ok := <-listenErr:
		if ok && err != nil {
			return fmt.Errorf("server: serve: %w", err)
		}
		return nil
	case <-ctx.Done():
		s.logger.InfoContext(ctx, "server shutdown initiated", "drain_timeout", s.drain)
	}

	s.healthz.MarkDraining()

	// Shutdown context is intentionally detached from ctx: the drain budget
	// must outlive the SIGTERM that triggered it.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.drain)
	defer cancel()

	if err := s.httpSrv.Shutdown(shutdownCtx); err != nil { //nolint:contextcheck // shutdown context is intentionally detached
		if errors.Is(err, context.DeadlineExceeded) {
			s.logger.WarnContext(ctx, "drain timeout exceeded, hard-closing", "drain_timeout", s.drain)
			_ = s.httpSrv.Close()
			return fmt.Errorf("server: shutdown: %w", err)
		}
		return fmt.Errorf("server: shutdown: %w", err)
	}

	s.logger.InfoContext(ctx, "server shutdown complete")
	return nil
}
