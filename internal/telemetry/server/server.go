// Package server is the telemetry service's HTTP surface: liveness/readiness
// probes (open), and the operator console behind HTTP Basic auth. T1 serves a
// placeholder console shell; the DB-backed dashboard + message inspector APIs
// and the real SPA bundle land in later phases. Webhook ingest and the OTLP
// listener are separate surfaces wired up alongside their phases.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/config"
)

// readHeaderTimeout bounds how long a console request may take to send its
// headers; OTLP/webhook ingest run on their own servers with their own budgets.
const readHeaderTimeout = 10 * time.Second

// Pinger is the readiness dependency: anything that can confirm the store is
// reachable. Narrowed to an interface so the server is testable without a live
// Postgres.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Server holds the console's dependencies. Constructed once at startup with
// explicit injection — no globals, no DI container.
type Server struct {
	console config.Console
	store   Pinger
	queries Queries
	webhook http.Handler
	log     *slog.Logger
}

// New builds the console server. store backs the readiness probe; queries backs
// the DB-backed console API (may be nil to disable those routes); webhook is the
// HMAC-trusted large-payload ingest handler (may be nil to disable the route).
func New(console config.Console, st Pinger, queries Queries, webhook http.Handler, log *slog.Logger) *Server {
	return &Server{console: console, store: st, queries: queries, webhook: webhook, log: log}
}

// Handler returns the routed HTTP handler. Probes and the HMAC-authed webhook
// are open (the webhook authenticates itself via signature); the console API and
// shell are behind Basic auth.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	if s.webhook != nil {
		// Open path: the webhook's own HMAC check is its auth, not Basic.
		mux.Handle("POST /api/v1/ingest/payload", s.webhook)
	}
	s.registerQueryRoutes(mux)
	mux.Handle("GET /", s.basicAuth(http.HandlerFunc(s.handleConsole)))
	return mux
}

// HTTPServer wraps Handler in a *http.Server with sane timeouts.
func (s *Server) HTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
	}
}

// handleHealthz is liveness: the process is up. No dependency checks.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writePlain(w, http.StatusOK, "ok\n")
}

// handleReadyz is readiness: the store is reachable. Returns 503 otherwise so
// a load balancer drains the instance until Postgres recovers.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		s.log.Warn("readiness probe failed", "err", err)
		writePlain(w, http.StatusServiceUnavailable, "store unavailable\n")
		return
	}
	writePlain(w, http.StatusOK, "ready\n")
}

// handleConsole serves the operator console. T1 placeholder until the SPA
// bundle is embedded in T5.
func (s *Server) handleConsole(w http.ResponseWriter, _ *http.Request) {
	writePlain(w, http.StatusOK, "sluice telemetry console — coming soon\n")
}

// basicAuth guards a handler with the configured console credentials. The
// username and the bcrypt password check are both constant-time-ish: bcrypt is
// inherently so, and the username uses subtle.ConstantTimeCompare to avoid a
// length/early-exit oracle.
func (s *Server) basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || !s.credentialsValid(user, pass) {
			w.Header().Set("WWW-Authenticate", `Basic realm="sluice telemetry", charset="UTF-8"`)
			writePlain(w, http.StatusUnauthorized, "unauthorized\n")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// credentialsValid checks user/pass against the configured console creds.
func (s *Server) credentialsValid(user, pass string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.console.Username)) == 1
	passErr := bcrypt.CompareHashAndPassword([]byte(s.console.PasswordHash), []byte(pass))
	// Evaluate both branches regardless so a wrong username and a wrong
	// password cost the same.
	return userOK && passErr == nil
}

// writePlain is the single text/plain response helper.
func writePlain(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// writeJSON writes v as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON {"error":msg} body with the given status.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
