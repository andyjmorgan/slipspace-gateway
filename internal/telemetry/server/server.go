// Package server is the telemetry service's HTTP surface: liveness/readiness
// probes (open), and the operator console behind HTTP Basic auth. T1 serves a
// placeholder console shell; the DB-backed dashboard + message inspector APIs
// and the real SPA bundle land in later phases. Webhook ingest and the OTLP
// listener are separate surfaces wired up alongside their phases.
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
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
	// facets caches the distinct dropdown values so a dropdown open doesn't scan
	// the event table on every request; refreshed once past its TTL. See
	// cachedFacets.
	facets facetsCache
	// auth memoizes successful credential verifications so the cost-10 bcrypt
	// doesn't run on every console request. The SPA sends the Authorization
	// header on every fetch; without this, each call paid ~one bcrypt of pure
	// CPU. See credentialsValid / authCache.
	auth authCache
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
		// Open path: the Record ingest's own HMAC check is its auth, not Basic.
		mux.Handle("POST /api/v1/ingest/record", s.webhook)
	}
	s.registerQueryRoutes(mux)
	// The SPA + its assets are public (the API it calls is Basic-auth gated);
	// this catch-all sits behind the API + probe routes.
	mux.Handle("GET /", spaHandler())
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

// basicAuth guards a handler with the configured console credentials. The
// username and the bcrypt password check are both constant-time-ish: bcrypt is
// inherently so, and the username uses subtle.ConstantTimeCompare to avoid a
// length/early-exit oracle.
//
// A rejected request gets a BARE 401 — deliberately NO WWW-Authenticate header
// (mirrors internal/admin.BasicAuth). The console SPA drives the credential
// prompt with its own login form + the Authorization header on every fetch; if
// we sent `WWW-Authenticate: Basic …`, browsers would intercept the SPA's
// fetch() 401s and pop their native auth dialog over the SPA, re-firing on
// every poll. The header is not required by RFC 7617 for the scheme to work,
// only for browser-driven challenge-response, which we suppress. curl users
// can still pass --basic credentials directly.
func (s *Server) basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || !s.credentialsValid(user, pass) {
			writePlain(w, http.StatusUnauthorized, "unauthorized\n")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authCacheTTL bounds how long a verified credential stays cached before its
// next request re-runs bcrypt. Short enough that rotating the console password
// takes effect within minutes, long enough that a browsing session pays bcrypt
// at most once per window rather than once per fetch.
const authCacheTTL = 5 * time.Minute

// authCache memoizes successful credential verifications behind a mutex, keyed
// by a SHA-256 of the presented credential. Only successes are cached: a wrong
// password always pays the full bcrypt, which preserves brute-force cost and
// bounds the map to the number of valid credentials (one, plus any in-flight
// rotation) rather than letting an attacker grow it with distinct guesses.
type authCache struct {
	mu      sync.Mutex
	entries map[string]time.Time // credential digest -> expiry
}

// credentialsValid checks user/pass against the configured console creds. A
// previously-verified credential is served from authCache without re-hashing;
// the bcrypt (and constant-time username compare) only runs on a cache miss.
func (s *Server) credentialsValid(user, pass string) bool {
	key := credentialKey(user, pass)
	if s.auth.valid(key) {
		return true
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.console.Username)) == 1
	passErr := bcrypt.CompareHashAndPassword([]byte(s.console.PasswordHash), []byte(pass))
	// Evaluate both branches regardless so a wrong username and a wrong
	// password cost the same.
	ok := userOK && passErr == nil
	if ok {
		s.auth.store(key, time.Now().Add(authCacheTTL))
	}
	return ok
}

// credentialKey derives the cache key for a credential. The username and
// password are joined by a NUL (which cannot appear in either) so distinct
// pairs cannot collide, then hashed so the plaintext password never lives in
// the cache map.
func credentialKey(user, pass string) string {
	sum := sha256.Sum256([]byte(user + "\x00" + pass))
	return hex.EncodeToString(sum[:])
}

// valid reports whether key has a live (unexpired) cache entry. Expired entries
// are dropped lazily on lookup.
func (c *authCache) valid(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	exp, ok := c.entries[key]
	if !ok {
		return false
	}
	if !time.Now().Before(exp) {
		delete(c.entries, key)
		return false
	}
	return true
}

// store records key as valid until exp, allocating the map on first use.
func (c *authCache) store(key string, exp time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]time.Time)
	}
	c.entries[key] = exp
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
