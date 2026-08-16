package config

import "github.com/google/uuid"

// APIKeysConfig is the merged `api_keys` block — a flat list of gateway-issued
// keys, each pointing at a named Configuration.
type APIKeysConfig []APIKey

// APIKey is a single gateway-issued credential.
type APIKey struct {
	// ID is the stable identifier the admin write API addresses the key by.
	// Optional in operator-authored YAML — nil is allowed for the local file
	// model — and minted by the admin API on create. Same nilable-UUID
	// pattern as RuleContract.ID / ResilienceConfig.ID.
	ID *uuid.UUID `yaml:"id,omitempty" json:"id,omitempty"`

	// Secret is the bearer token clients present (conventionally prefixed
	// "sk_live_…" for production keys or "sk_dev_…" for development keys).
	//
	// Authentication resolves it by indexed lookup against the snapshot's
	// SecretIndex, not by comparing it against each configured key in turn
	// (internal/middleware/auth/resolver.go). That is the timing-relevant
	// property: a map lookup does not walk the secret byte by byte, so it
	// leaks no prefix information the way a short-circuiting == over a
	// candidate list would, and Go's per-process hash seed keeps a caller
	// from steering bucket collisions. The secret must still be
	// high-entropy — the index is a lookup, not a slow hash, so it is no
	// defence against an offline guess of a weak secret.
	Secret string `yaml:"secret" json:"secret"`

	// Name is a human-readable label surfaced in logs and reporting events;
	// it carries no auth meaning.
	Name string `yaml:"name" json:"name"`

	// Configuration is the name of the Configuration this key resolves to.
	// Many keys may share one Configuration.
	Configuration string `yaml:"configuration" json:"configuration"`

	// Enabled toggles the key without removing it. A disabled key
	// authenticates structurally but is rejected before forwarding.
	Enabled bool `yaml:"enabled" json:"enabled"`
}
