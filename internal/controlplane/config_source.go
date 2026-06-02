package controlplane

import (
	"context"
	"errors"
	"sync"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// ErrUnknownAPIKey is returned by a ConfigProvider when the presented api-key
// is unknown or disabled.
var ErrUnknownAPIKey = errors.New("controlplane: unknown or disabled api key")

// ErrNoConfig is returned when distribution is enabled but no config version is
// active yet (nothing published).
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
// presenting gateway should serve.
type ConfigProvider interface {
	ClosureForAPIKey(ctx context.Context, apiKey string) (Closure, error)
}

// closureForAPIKey resolves apiKey through snap's SecretIndex to its
// configuration and marshals the scoped closure. Unknown or disabled keys fail
// with ErrUnknownAPIKey so a caller cannot probe configuration names with
// random keys.
func closureForAPIKey(snap *config.ResolvedConfigV2, apiKey string) (Closure, error) {
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

// activeVersionReader reads the currently-active published config version.
// Satisfied by *configdb.DB.
type activeVersionReader interface {
	ActiveVersion(ctx context.Context) (configdb.Version, error)
}

// DBConfigProvider serves closures straight from Postgres, the shared source of
// truth, so every CP replica resolves against the same active config and a
// publish on ANY replica is served by ALL without per-replica cache coherence.
// Each call reads the active version's content hash (a cheap indexed query) and
// only re-resolves the closure document when that hash changes — a read-through
// cache, not a background poller.
type DBConfigProvider struct {
	src activeVersionReader

	mu         sync.RWMutex
	cachedHash string
	cached     *config.ResolvedConfigV2
}

// NewDBConfigProvider builds a Postgres-backed provider over src.
func NewDBConfigProvider(src activeVersionReader) *DBConfigProvider {
	return &DBConfigProvider{src: src}
}

// ClosureForAPIKey reads the active config from Postgres and resolves apiKey.
func (p *DBConfigProvider) ClosureForAPIKey(ctx context.Context, apiKey string) (Closure, error) {
	snap, err := p.current(ctx)
	if err != nil {
		return Closure{}, err
	}
	return closureForAPIKey(snap, apiKey)
}

func (p *DBConfigProvider) current(ctx context.Context) (*config.ResolvedConfigV2, error) {
	active, err := p.src.ActiveVersion(ctx)
	if errors.Is(err, configdb.ErrNoActiveConfig) {
		return nil, ErrNoConfig
	}
	if err != nil {
		return nil, err
	}

	p.mu.RLock()
	if p.cached != nil && p.cachedHash == active.Hash {
		cached := p.cached
		p.mu.RUnlock()
		return cached, nil
	}
	p.mu.RUnlock()

	resolved, err := config.ResolveClosure(active.Body)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.cachedHash = active.Hash
	p.cached = resolved
	p.mu.Unlock()
	return resolved, nil
}

// StoreConfigProvider serves closures from an in-memory config.Store. Retained
// as a lightweight provider for tests (the gRPC service and reconciler suites);
// production uses DBConfigProvider so there is one Postgres-backed source of
// truth across replicas.
type StoreConfigProvider struct {
	store *config.Store
}

// NewStoreConfigProvider builds a provider backed by store.
func NewStoreConfigProvider(store *config.Store) *StoreConfigProvider {
	return &StoreConfigProvider{store: store}
}

// ClosureForAPIKey resolves apiKey against the store's current snapshot.
func (p *StoreConfigProvider) ClosureForAPIKey(_ context.Context, apiKey string) (Closure, error) {
	if p == nil || p.store == nil {
		return Closure{}, ErrNoConfig
	}
	return closureForAPIKey(p.store.Snapshot(), apiKey)
}
