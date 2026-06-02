package controlplane

import (
	"context"
	"errors"
	"strings"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

func providerTestStore() *config.Store {
	keyAlpha := &contractsconfig.APIKey{Secret: "sk_live_alpha", Name: "alpha-key", Configuration: "alpha", Enabled: true} //nolint:gosec // test fixture, not a credential
	keyOff := &contractsconfig.APIKey{Secret: "sk_live_off", Name: "off-key", Configuration: "alpha", Enabled: false}      //nolint:gosec // test fixture, not a credential
	resolved := &config.ResolvedConfigV2{
		Backends: contractsconfig.BackendsConfig{
			"openai": {
				BaseURL:   "https://api.openai.com",
				Protocols: map[string]contractsconfig.BackendProtocol{"chat": {Path: "/v1/chat/completions"}},
			},
		},
		Configurations: map[string]contractsconfig.ConfigurationV2{
			"alpha": {
				Credentials: map[string]string{"openai": "sk-alpha-secret"},
				Bindings:    []contractsconfig.Binding{{Protocol: "chat", Backend: "openai"}},
			},
		},
		APIKeys: contractsconfig.APIKeysConfig{*keyAlpha, *keyOff},
		SecretIndex: map[string]*contractsconfig.APIKey{
			"sk_live_alpha": keyAlpha,
			"sk_live_off":   keyOff,
		},
	}
	return config.NewStore(resolved)
}

func TestStoreConfigProvider_KnownKey(t *testing.T) {
	p := NewStoreConfigProvider(providerTestStore())
	cl, err := p.ClosureForAPIKey(context.Background(), "sk_live_alpha")
	if err != nil {
		t.Fatalf("ClosureForAPIKey: %v", err)
	}
	if cl.Configuration != "alpha" {
		t.Errorf("configuration = %q, want alpha", cl.Configuration)
	}
	if cl.Hash == "" || len(cl.Body) == 0 {
		t.Fatalf("empty closure: hash=%q bodylen=%d", cl.Hash, len(cl.Body))
	}
	if !strings.Contains(string(cl.Body), "openai") {
		t.Errorf("closure body missing backend:\n%s", cl.Body)
	}
}

func TestStoreConfigProvider_UnknownAndDisabled(t *testing.T) {
	p := NewStoreConfigProvider(providerTestStore())
	for _, key := range []string{"sk_live_off", "sk_live_nope"} {
		if _, err := p.ClosureForAPIKey(context.Background(), key); !errors.Is(err, ErrUnknownAPIKey) {
			t.Errorf("key %q: err = %v, want ErrUnknownAPIKey", key, err)
		}
	}
}

func TestStoreConfigProvider_NoConfig(t *testing.T) {
	// nil store inside the provider.
	if _, err := NewStoreConfigProvider(nil).ClosureForAPIKey(context.Background(), "x"); !errors.Is(err, ErrNoConfig) {
		t.Errorf("nil store: err = %v, want ErrNoConfig", err)
	}
	// store present but no snapshot loaded (zero-value Store -> nil snapshot).
	if _, err := NewStoreConfigProvider(&config.Store{}).ClosureForAPIKey(context.Background(), "x"); !errors.Is(err, ErrNoConfig) {
		t.Errorf("nil snapshot: err = %v, want ErrNoConfig", err)
	}
}

type fakeActiveVersion struct {
	v     configdb.Version
	err   error
	calls int
}

func (f *fakeActiveVersion) ActiveVersion(context.Context) (configdb.Version, error) {
	f.calls++
	return f.v, f.err
}

func TestDBConfigProvider(t *testing.T) {
	ctx := context.Background()
	body, err := config.MarshalConfig(providerTestStore().Snapshot())
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	src := &fakeActiveVersion{v: configdb.Version{ID: "v1", Hash: "h1", Body: body}}
	p := NewDBConfigProvider(src)

	// First fetch resolves; second with the same hash hits the read-through
	// cache (no re-resolve) but still consults Postgres for the current hash.
	cl, err := p.ClosureForAPIKey(ctx, "sk_live_alpha")
	if err != nil || cl.Configuration != "alpha" {
		t.Fatalf("first fetch: cl=%+v err=%v", cl, err)
	}
	if _, err := p.ClosureForAPIKey(ctx, "sk_live_alpha"); err != nil {
		t.Fatalf("cached fetch: %v", err)
	}
	if src.calls != 2 {
		t.Errorf("ActiveVersion calls = %d, want 2 (consulted every fetch)", src.calls)
	}

	// A changed hash re-resolves.
	src.v = configdb.Version{ID: "v2", Hash: "h2", Body: body}
	if _, err := p.ClosureForAPIKey(ctx, "sk_live_alpha"); err != nil {
		t.Fatalf("after hash change: %v", err)
	}

	// Unknown key is rejected.
	if _, err := p.ClosureForAPIKey(ctx, "sk_live_nope"); !errors.Is(err, ErrUnknownAPIKey) {
		t.Errorf("unknown key: err = %v, want ErrUnknownAPIKey", err)
	}

	// No active version -> ErrNoConfig.
	src.err = configdb.ErrNoActiveConfig
	if _, err := p.ClosureForAPIKey(ctx, "x"); !errors.Is(err, ErrNoConfig) {
		t.Errorf("no active version: err = %v, want ErrNoConfig", err)
	}

	// A read error propagates.
	src.err = errors.New("db down")
	if _, err := p.ClosureForAPIKey(ctx, "x"); err == nil {
		t.Error("read error: want error, got nil")
	}

	// An unparseable active body surfaces as an error.
	src2 := &fakeActiveVersion{v: configdb.Version{ID: "bad", Hash: "hbad", Body: []byte("{{{not config")}}
	if _, err := NewDBConfigProvider(src2).ClosureForAPIKey(ctx, "x"); err == nil {
		t.Error("unparseable active body: want error, got nil")
	}
}
