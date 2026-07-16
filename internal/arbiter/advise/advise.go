// Package advise implements the Arbiter's agent-aware routing advisor: an
// HMAC-trusted endpoint (POST /api/v1/advise/route) a gateway asks to
// classify one agent conversation at birth. The advisor prompts a judge model
// (conventionally routed back through a gateway on a dedicated advisor
// configuration) and answers with an advise.Verdict.
//
// Channel discipline (gateway invariant #4): this is control-plane advice —
// a third surface distinct from OTLP telemetry and the Record webhook. It
// shares the Record webhook's gateway HMAC registry for trust, nothing else.
// The advisor recommends; the gateway's allow_models list decides.
package advise

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	contractsadvise "github.com/andyjmorgan/slipspace-gateway/contracts/advise"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/registry"
)

// Webhook headers a gateway sets on each advisory request — the same trust
// convention as the Record webhook.
const (
	headerGatewayID = "X-Slipspace-Gateway-Id"
	headerSignature = "X-Slipspace-Signature"
)

// maxRequestBytes caps one advisory POST. Prompt excerpts are gateway-side
// truncated to a few KiB, so anything large is malformed.
const maxRequestBytes = 256 << 10

// verifier is the registry slice the handler needs: HMAC trust.
type verifier interface {
	Verify(gatewayID string, body []byte, sigHex string) error
}

// judge classifies one request into a verdict. Implemented by Judge; stubbed
// in tests.
type judge interface {
	Judge(ctx context.Context, req contractsadvise.Request) (contractsadvise.Verdict, error)
}

// cacheEntry is one cached verdict.
type cacheEntry struct {
	verdict contractsadvise.Verdict
	expires time.Time
}

// maxCacheEntries bounds the verdict cache.
const maxCacheEntries = 10_000

// Handler answers advisory classification requests.
type Handler struct {
	registry verifier
	judge    judge
	logger   *slog.Logger
	cacheTTL time.Duration

	mu    sync.Mutex
	cache map[string]cacheEntry
	now   func() time.Time
}

// NewHandler builds the advisory handler.
func NewHandler(reg verifier, j judge, cacheTTL time.Duration, logger *slog.Logger) *Handler {
	return &Handler{
		registry: reg,
		judge:    j,
		logger:   logger,
		cacheTTL: cacheTTL,
		cache:    make(map[string]cacheEntry),
		now:      time.Now,
	}
}

// ServeHTTP handles POST of one advise.Request. HMAC verification mirrors the
// Record webhook; classification errors return 503 so the gateway fails open
// and may retry on a later request of the same conversation.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()

	gatewayID := r.Header.Get(headerGatewayID)
	sig := r.Header.Get(headerSignature)
	if gatewayID == "" || sig == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing gateway id or signature"})
		return
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request too large"})
		return
	}

	if err := h.registry.Verify(gatewayID, raw, sig); err != nil {
		if errors.Is(err, registry.ErrUnknownGateway) || errors.Is(err, registry.ErrBadSignature) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "signature rejected"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "verify"})
		return
	}

	var req contractsadvise.Request
	if err := json.Unmarshal(raw, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed request"})
		return
	}

	key := templateHash(req)
	if v, ok := h.cached(key); ok {
		writeJSON(w, http.StatusOK, v)
		return
	}

	verdict, err := h.judge.Judge(r.Context(), req)
	if err != nil {
		h.logger.Warn("advise: judge failed", "gateway", gatewayID,
			"conversation_id", req.ConversationID, "error", err.Error())
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "judge unavailable"})
		return
	}

	h.store(key, verdict)
	h.logger.Info("advise: verdict",
		"gateway", gatewayID,
		"conversation_id", req.ConversationID,
		"agent_family", req.AgentFamily,
		"switch", verdict.Switch,
		"model", verdict.Model,
		"reason", verdict.Reason)
	writeJSON(w, http.StatusOK, verdict)
}

// cached returns a live cache hit.
func (h *Handler) cached(key string) (contractsadvise.Verdict, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.cache[key]
	if !ok || h.now().After(e.expires) {
		return contractsadvise.Verdict{}, false
	}
	return e.verdict, true
}

// store caches a verdict, sweeping expired entries on overflow.
func (h *Handler) store(key string, v contractsadvise.Verdict) {
	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.cache) >= maxCacheEntries {
		for k, e := range h.cache {
			if now.After(e.expires) {
				delete(h.cache, k)
			}
		}
		for k := range h.cache {
			if len(h.cache) < maxCacheEntries {
				break
			}
			delete(h.cache, k)
		}
	}
	h.cache[key] = cacheEntry{verdict: v, expires: now.Add(h.cacheTTL)}
}

// templateHash keys the verdict cache: identical agent template + task text
// classifies identically. The task text is included because difficulty lives
// in it — the hit rate comes from repeated templates (retried conversations,
// templated fan-outs), not from cross-task reuse.
func templateHash(req contractsadvise.Request) string {
	tools := append([]string(nil), req.ToolNames...)
	sort.Strings(tools)
	sum := sha256.Sum256([]byte(strings.Join([]string{
		req.AgentFamily,
		req.Entrypoint,
		req.Model,
		req.SystemPrefix,
		req.FirstUserMessage,
		strings.Join(tools, ","),
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// writeJSON writes a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
