package controlplane

import (
	"errors"
	"strings"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

func providerTestStore() *config.Store {
	keyAlpha := &contractsconfig.APIKey{Secret: "sk_live_alpha", Name: "alpha-key", Configuration: "alpha", Enabled: true} //nolint:gosec // test fixture, not a credential
	keyOff := &contractsconfig.APIKey{Secret: "sk_live_off", Name: "off-key", Configuration: "alpha", Enabled: false}      //nolint:gosec // test fixture, not a credential
	resolved := &config.ResolvedConfigV2{
		Backends: contractsconfig.BackendsConfig{
			"openai": {BaseURL: "https://api.openai.com"},
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
	cl, err := p.ClosureForAPIKey("sk_live_alpha")
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
		if _, err := p.ClosureForAPIKey(key); !errors.Is(err, ErrUnknownAPIKey) {
			t.Errorf("key %q: err = %v, want ErrUnknownAPIKey", key, err)
		}
	}
}

func TestStoreConfigProvider_NoConfig(t *testing.T) {
	// nil store inside the provider.
	if _, err := NewStoreConfigProvider(nil).ClosureForAPIKey("x"); !errors.Is(err, ErrNoConfig) {
		t.Errorf("nil store: err = %v, want ErrNoConfig", err)
	}
	// store present but no snapshot loaded (zero-value Store -> nil snapshot).
	if _, err := NewStoreConfigProvider(&config.Store{}).ClosureForAPIKey("x"); !errors.Is(err, ErrNoConfig) {
		t.Errorf("nil snapshot: err = %v, want ErrNoConfig", err)
	}
}
