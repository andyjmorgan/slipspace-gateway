package config

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
)

// TestValidate_DuplicateAPIKeyID covers the api-key id-uniqueness branch added
// for the read-write surface: two keys sharing a non-nil ID must be rejected.
func TestValidate_DuplicateAPIKeyID(t *testing.T) {
	id := uuid.New()
	rc := &ResolvedConfigV2{
		Backends: contractsconfig.BackendsConfig{
			"openai": {
				BaseURL:   "https://api.openai.com",
				Protocols: map[string]contractsconfig.BackendProtocol{"chat": {Path: "/v1/chat/completions"}},
			},
		},
		Configurations: map[string]contractsconfig.ConfigurationV2{
			"prod": {
				Credentials: map[string]string{"openai": "sk"},
				Bindings:    []contractsconfig.Binding{{Protocol: "chat", Models: []string{"gpt-*"}, Backend: "openai"}},
			},
		},
		APIKeys: contractsconfig.APIKeysConfig{
			{ID: &id, Secret: "sk_live_a", Name: "k1", Configuration: "prod", Enabled: true},
			{ID: &id, Secret: "sk_live_b", Name: "k2", Configuration: "prod", Enabled: true},
		},
	}
	err := rc.Validate()
	if !errors.Is(err, ErrV2Validation) {
		t.Fatalf("duplicate api-key id: got %v, want ErrV2Validation", err)
	}
}

// TestValidate_DistinctAPIKeyIDsOK confirms distinct (and nil) ids validate.
func TestValidate_DistinctAPIKeyIDsOK(t *testing.T) {
	id := uuid.New()
	rc := &ResolvedConfigV2{
		Backends: contractsconfig.BackendsConfig{
			"openai": {
				BaseURL:   "https://api.openai.com",
				Protocols: map[string]contractsconfig.BackendProtocol{"chat": {Path: "/v1/chat/completions"}},
			},
		},
		Configurations: map[string]contractsconfig.ConfigurationV2{
			"prod": {
				Credentials: map[string]string{"openai": "sk"},
				Bindings:    []contractsconfig.Binding{{Protocol: "chat", Models: []string{"gpt-*"}, Backend: "openai"}},
			},
		},
		APIKeys: contractsconfig.APIKeysConfig{
			{ID: &id, Secret: "sk_live_a", Name: "k1", Configuration: "prod", Enabled: true},
			{Secret: "sk_live_b", Name: "k2", Configuration: "prod", Enabled: true}, // nil id
		},
	}
	if err := rc.Validate(); err != nil {
		t.Fatalf("distinct/nil ids should validate: %v", err)
	}
}
