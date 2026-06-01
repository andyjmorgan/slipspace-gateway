package controlplane

import (
	"errors"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

// ErrUnknownAPIKey is returned by a ConfigProvider when the presented api-key
// is unknown or disabled.
var ErrUnknownAPIKey = errors.New("controlplane: unknown or disabled api key")

// ErrNoConfig is returned when config distribution is enabled but no snapshot
// is loaded.
var ErrNoConfig = errors.New("controlplane: no configuration loaded")

// Closure is a resolved per-configuration config closure ready to ship to a
// gateway: the configuration name it resolved to, the content hash, and the
// serialized v2 document bytes.
type Closure struct {
	Configuration string
	Hash          string
	Body          []byte
}

// ConfigProvider resolves a Sluice api-key to the per-configuration closure the
// presenting gateway should serve. nil disables FetchConfig (Phase 1).
type ConfigProvider interface {
	ClosureForAPIKey(apiKey string) (Closure, error)
}

// StoreConfigProvider serves closures from a live config.Store, so a future
// admin write to the control plane is reflected on the next FetchConfig without
// a restart. Snapshot-per-call keeps each resolution internally consistent
// (invariant #9).
type StoreConfigProvider struct {
	store *config.Store
}

// NewStoreConfigProvider builds a provider backed by store.
func NewStoreConfigProvider(store *config.Store) *StoreConfigProvider {
	return &StoreConfigProvider{store: store}
}

// ClosureForAPIKey resolves apiKey through the snapshot's SecretIndex to its
// configuration and marshals the scoped closure. Unknown or disabled keys fail
// with ErrUnknownAPIKey so a caller cannot probe configuration names with
// random keys.
func (p *StoreConfigProvider) ClosureForAPIKey(apiKey string) (Closure, error) {
	if p == nil || p.store == nil {
		return Closure{}, ErrNoConfig
	}
	snap := p.store.Snapshot()
	if snap == nil {
		return Closure{}, ErrNoConfig
	}
	key, ok := snap.SecretIndex[apiKey]
	if !ok || key == nil || !key.Enabled {
		return Closure{}, ErrUnknownAPIKey
	}
	body, hash, err := config.MarshalClosure(snap, key.Configuration)
	if err != nil {
		return Closure{}, err
	}
	return Closure{
		Configuration: key.Configuration,
		Hash:          hash,
		Body:          body,
	}, nil
}
