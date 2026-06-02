package controlplane

import (
	"crypto/subtle"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// AdminAuthenticator verifies HTTP Basic credentials against a bcrypt hash that
// lives in Postgres (the shared source of truth, so every CP replica
// authenticates against the same credential). The hash is cached in memory so
// auth does not hit the database per request; production loads it at startup.
type AdminAuthenticator struct {
	username string

	mu   sync.RWMutex
	hash []byte
}

// NewAdminAuthenticator builds an authenticator for username with the given
// bcrypt hash.
func NewAdminAuthenticator(username, hash string) *AdminAuthenticator {
	return &AdminAuthenticator{username: username, hash: []byte(hash)}
}

// Verify reports whether (username, password) are valid. The username is
// compared in constant time; bcrypt's own comparison is constant-time over the
// password. A failed bcrypt compare (wrong password or unparseable hash)
// returns false.
func (a *AdminAuthenticator) Verify(username, password string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(a.username)) == 1
	a.mu.RLock()
	hash := a.hash
	a.mu.RUnlock()
	passOK := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
	return userOK && passOK
}

// SetHash swaps the cached hash, e.g. after a password change is published.
// Safe for concurrent use with Verify.
func (a *AdminAuthenticator) SetHash(hash string) {
	a.mu.Lock()
	a.hash = []byte(hash)
	a.mu.Unlock()
}
