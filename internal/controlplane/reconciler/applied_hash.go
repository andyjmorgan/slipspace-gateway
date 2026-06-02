package reconciler

import "sync"

// AppliedHash is the content hash of the config closure the gateway is currently
// serving. The config syncer writes it on each apply; the reconciler reads it
// and reports it on heartbeat, so the control plane can tell which fleet members
// are running the latest published config and which have drifted. Safe for
// concurrent use; a nil *AppliedHash is a no-op (the standalone/register-only
// gateway has no synced config to report).
type AppliedHash struct {
	mu   sync.RWMutex
	hash string
}

// Set records the currently-applied closure hash.
func (a *AppliedHash) Set(hash string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.hash = hash
	a.mu.Unlock()
}

// Get returns the currently-applied closure hash, or "" if none has been
// applied yet.
func (a *AppliedHash) Get() string {
	if a == nil {
		return ""
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.hash
}
