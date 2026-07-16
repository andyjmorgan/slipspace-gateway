package agentroute

import (
	"sync"
	"time"
)

// maxEntries caps the register so unbounded conversation churn cannot grow it
// without limit. On overflow the sweep drops expired entries first, then
// arbitrary ones — a lost pin only costs a re-classification.
const maxEntries = 100_000

// Pin is one applied routing decision: the model a conversation is pinned to
// and why.
type Pin struct {
	// Model is the pinned model name (already validated against the owning
	// configuration's allow_models).
	Model string

	// Reason is the advisor's justification, propagated to telemetry tags.
	Reason string
}

// entry is one conversation's lifecycle in the register.
type entry struct {
	// pin is the applied decision; nil until a verdict lands (or forever,
	// when the verdict was "continue" / discarded).
	pin *Pin

	// decided is true once a verdict (including "continue") has been
	// processed — no further advisory calls fire for the conversation.
	decided bool

	// seen counts eligible requests observed for the conversation; the apply
	// window is enforced against it when the verdict lands.
	seen int

	// expires is when the entry (and its pin) lapses.
	expires time.Time
}

// Register is the in-process pin store, keyed by conversation id. It is the
// structural sibling of the resilience circuit-breaker store: per-pod,
// mutex-guarded, non-persistent — a restart only costs re-classification.
type Register struct {
	mu      sync.Mutex
	entries map[string]*entry

	// now is the clock, swappable in tests.
	now func() time.Time
}

// NewRegister builds an empty register.
func NewRegister() *Register {
	return &Register{entries: make(map[string]*entry), now: time.Now}
}

// Observe records one eligible request for the conversation and returns the
// current decision state: pin is the model override to apply (nil when none),
// and fire is true when the caller should launch an advisory call — exactly
// once per conversation per TTL.
func (r *Register) Observe(key string, ttl time.Duration) (pin *Pin, fire bool) {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.entries[key]
	if !ok || now.After(e.expires) {
		if !ok && len(r.entries) >= maxEntries {
			r.sweepLocked(now)
		}
		r.entries[key] = &entry{seen: 1, expires: now.Add(ttl)}
		return nil, true
	}

	e.seen++
	return e.pin, false
}

// Complete lands a verdict for the conversation. pin nil means "continue as
// configured". The apply window is enforced here: a verdict arriving after
// the conversation has already seen more than window requests is discarded —
// the prompt cache is warm on the default model and a late switch costs more
// than it saves.
func (r *Register) Complete(key string, pin *Pin, window int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.entries[key]
	if !ok {
		return
	}
	e.decided = true
	if pin != nil && e.seen <= window {
		e.pin = pin
	}
}

// Fail abandons an in-flight advisory attempt so a later request of the same
// conversation may retry classification.
func (r *Register) Fail(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if e, ok := r.entries[key]; ok && !e.decided {
		delete(r.entries, key)
	}
}

// sweepLocked drops expired entries; if none expired it drops arbitrary
// entries until 1% headroom exists. Caller holds mu.
func (r *Register) sweepLocked(now time.Time) {
	for k, e := range r.entries {
		if now.After(e.expires) {
			delete(r.entries, k)
		}
	}
	over := len(r.entries) - (maxEntries - maxEntries/100)
	for k := range r.entries {
		if over <= 0 {
			break
		}
		delete(r.entries, k)
		over--
	}
}
