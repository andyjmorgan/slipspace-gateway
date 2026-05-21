package main

import (
	"strings"
	"sync"
)

// StreamChunk is one SSE event in a streamed canned response. DelayMs is
// the inter-chunk delay applied *before* the chunk is written; the first
// chunk inherits its delay from CannedResponse.DelayMs if any.
type StreamChunk struct {
	Data string `json:"data" yaml:"data"`

	DelayMs int `json:"delay_ms,omitempty" yaml:"delay_ms,omitempty"`
}

// CannedResponse is the stored shape that the server returns when a
// request matches its Method/Path/RequestBodyContains predicates.
//
// MaxResponses, DelayMs, and Behavior are the resilience-testing
// primitives — they let a session stage multi-step scenarios such as
// "primary returns 503 twice then 200" (MaxResponses) or "stall N ms
// before status line" (DelayMs) or "close the connection without
// writing status" (Behavior).
type CannedResponse struct {
	Method string `json:"method,omitempty" yaml:"method,omitempty"`

	Path string `json:"path,omitempty" yaml:"path,omitempty"`

	RequestBodyContains string `json:"request_body_contains,omitempty" yaml:"request_body_contains,omitempty"`

	Status int `json:"status,omitempty" yaml:"status,omitempty"`

	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`

	Streaming bool `json:"streaming,omitempty" yaml:"streaming,omitempty"`

	Body string `json:"body,omitempty" yaml:"body,omitempty"`

	StreamChunks []StreamChunk `json:"stream_chunks,omitempty" yaml:"stream_chunks,omitempty"`

	// MaxResponses caps how many times this entry will match. Zero means
	// "unlimited" (the legacy default). On every match the counter is
	// decremented; when it reaches zero the entry is removed from the
	// pool. Pop is scoped to the session the entry was staged in.
	MaxResponses int `json:"max_responses,omitempty" yaml:"max_responses,omitempty"`

	// DelayMs is the pre-response delay before the status line is
	// written. Honors request-context cancellation so client disconnect
	// terminates promptly. Distinct from per-chunk StreamChunk.DelayMs,
	// which delays between SSE events after the status line has gone
	// out.
	DelayMs int `json:"delay_ms,omitempty" yaml:"delay_ms,omitempty"`

	// Behavior overrides the normal write path with a transport-level
	// failure simulation. Recognised values:
	//   ""      — normal write (default)
	//   "close" — hijack the TCP connection and close without writing
	//             any status line (transport error path)
	//   "hang"  — block until request context is cancelled (per-attempt
	//             header-timeout path)
	// Unknown values fall through to the normal write path.
	Behavior string `json:"behavior,omitempty" yaml:"behavior,omitempty"`
}

// BehaviorClose closes the upstream TCP connection without writing
// status or body. Models a transport-error condition for resilience
// tests.
const BehaviorClose = "close"

// BehaviorHang blocks the upstream handler until the request context
// is cancelled. Models a hung upstream for per-attempt header-timeout
// tests.
const BehaviorHang = "hang"

// registry holds staged canned responses keyed by session ID. The
// empty-string key is the global pool, which is what file-loaded
// responses and session-less control-plane calls populate. Session
// IDs come from the request's X-Sluice-Session-Id header, letting one
// mockllm instance serve multiple independent scenarios in the same
// test run.
type registry struct {
	mu sync.Mutex

	sessions map[string][]CannedResponse
}

func newRegistry() *registry {
	return &registry{sessions: make(map[string][]CannedResponse)}
}

// add appends c to the session pool for sessionID. An empty sessionID
// targets the global pool.
func (r *registry) add(sessionID string, c CannedResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[sessionID] = append(r.sessions[sessionID], c)
}

// clearSession drops one session's pool. The global pool is targeted
// when sessionID is empty.
func (r *registry) clearSession(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, sessionID)
}

// clearAll wipes every session and the global pool. Used by the
// session-less DELETE /control/responses for back-compat with tests
// that expect "DELETE clears everything."
func (r *registry) clearAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions = make(map[string][]CannedResponse)
}

// replace wipes every pool and seeds the global pool with rs. Used by
// the --responses file loader at startup.
func (r *registry) replace(rs []CannedResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions = make(map[string][]CannedResponse)
	if len(rs) > 0 {
		r.sessions[""] = append([]CannedResponse{}, rs...)
	}
}

// snapshotGlobal returns a copy of the global pool. The /control/state
// endpoint uses this so existing tests that only know about the legacy
// single-pool model keep working unchanged.
func (r *registry) snapshotGlobal() []CannedResponse {
	r.mu.Lock()
	defer r.mu.Unlock()
	src := r.sessions[""]
	out := make([]CannedResponse, len(src))
	copy(out, src)
	return out
}

// snapshotSessions returns a deep copy of every non-global session
// pool. Empty when only the global pool has entries.
func (r *registry) snapshotSessions() map[string][]CannedResponse {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string][]CannedResponse, len(r.sessions))
	for k, v := range r.sessions {
		if k == "" {
			continue
		}
		cp := make([]CannedResponse, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// match locates the first canned response whose predicates match the
// inbound request. The session pool is consulted first; on miss the
// global pool is consulted as a fall-back. When MaxResponses is set,
// the matched entry is decremented (and removed at zero) inside the
// pool it came from — pop is scoped to the pool, never crossing
// sessions.
//
// The lock is acquired exclusive (not RLock) because match may mutate
// the pool to apply pop semantics. Match traffic is low (one per
// upstream request the gateway makes) so contention is not a concern.
func (r *registry) match(sessionID, method, path, body string) (CannedResponse, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if c, ok := r.matchInPool(sessionID, method, path, body); ok {
		return c, true
	}
	if sessionID != "" {
		if c, ok := r.matchInPool("", method, path, body); ok {
			return c, true
		}
	}
	return CannedResponse{}, false
}

// matchInPool is the inner per-pool match. Caller must hold r.mu.
func (r *registry) matchInPool(sessionID, method, path, body string) (CannedResponse, bool) {
	pool := r.sessions[sessionID]
	for i, c := range pool {
		if !matchesField(c.Method, method) {
			continue
		}
		if !matchesField(c.Path, path) {
			continue
		}
		if c.RequestBodyContains != "" && !strings.Contains(body, c.RequestBodyContains) {
			continue
		}
		if c.MaxResponses > 0 {
			remaining := c.MaxResponses - 1
			if remaining == 0 {
				pool = append(pool[:i], pool[i+1:]...)
				if len(pool) == 0 {
					delete(r.sessions, sessionID)
				} else {
					r.sessions[sessionID] = pool
				}
			} else {
				pool[i].MaxResponses = remaining
				r.sessions[sessionID] = pool
			}
		}
		return c, true
	}
	return CannedResponse{}, false
}

func matchesField(criterion, actual string) bool {
	if criterion == "" {
		return true
	}
	return strings.EqualFold(criterion, actual)
}
