package server

import (
	"net/http"
	"sync/atomic"
)

// HealthzHandler responds to GET /healthz with 200 "ok" until MarkDraining
// flips it into 503 "draining". The transition is the contract Kubernetes
// uses to stop sending new traffic before SIGTERM-driven shutdown completes.
type HealthzHandler struct {
	draining atomic.Bool
}

// NewHealthzHandler returns a HealthzHandler in the healthy state.
func NewHealthzHandler() *HealthzHandler {
	return &HealthzHandler{}
}

// MarkDraining flips the handler into the 503 "draining" response mode. It
// is safe to call concurrently with ServeHTTP and is idempotent.
func (h *HealthzHandler) MarkDraining() {
	h.draining.Store(true)
}

// IsDraining reports whether MarkDraining has been called. Exposed primarily
// so tests can assert lifecycle transitions without racing on the response.
func (h *HealthzHandler) IsDraining() bool {
	return h.draining.Load()
}

func (h *HealthzHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Defensive: if the request context is already cancelled (client
	// disconnect, upstream timeout), skip the body write to avoid noisy
	// "use of closed connection" errors.
	if err := r.Context().Err(); err != nil {
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	if h.draining.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("draining\n"))
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}
