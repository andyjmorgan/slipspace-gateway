package admin

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/config"
)

func TestMergeSecret(t *testing.T) {
	newVal := "fresh"
	empty := ""
	cases := []struct {
		name     string
		incoming *string
		existing string
		want     string
	}{
		{"nil keeps existing", nil, "stored", "stored"},
		{"explicit value overwrites", &newVal, "stored", "fresh"},
		{"explicit empty clears (no-cred provider)", &empty, "stored", ""},
		{"nil keeps empty", nil, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mergeSecret(tc.incoming, tc.existing); got != tc.want {
				t.Errorf("mergeSecret = %q, want %q", got, tc.want)
			}
		})
	}
}

// validSnapshot returns a minimal config that passes RevalidateAndIndex.
func validSnapshot() *config.ResolvedConfig {
	rc := &config.ResolvedConfig{
		Providers: contractsconfig.ProvidersConfig{
			"openai": {
				BaseURL:   "https://api.openai.com",
				Protocols: map[string]contractsconfig.ProviderProtocol{"chat": {Path: "/v1/chat/completions"}},
			},
		},
		Groups: contractsconfig.GroupsConfig{
			"g1": {Mode: "failover", Targets: []contractsconfig.Target{{Provider: "openai"}}},
		},
		Configurations: map[string]contractsconfig.Configuration{
			"prod": {
				Credentials:         map[string]string{"openai": "sk"},
				Bindings:            []contractsconfig.Binding{{Protocol: "chat", Models: []string{"gpt-*"}, Provider: "openai"}},
				PassthroughBindings: nil,
			},
		},
		APIKeys: contractsconfig.APIKeysConfig{
			{Secret: "sk_live_x", Name: "svc", Configuration: "prod", Enabled: true},
		},
	}
	if err := rc.RevalidateAndIndex(); err != nil {
		panic("validSnapshot is not valid: " + err.Error())
	}
	return rc
}

func TestPreviewMutation(t *testing.T) {
	snap := validSnapshot()

	noop := previewMutation(snap, func(*config.ResolvedConfig) {})
	if !noop.Valid || noop.Error != "" {
		t.Errorf("noop preview should be valid, got %+v", noop)
	}

	bad := previewMutation(snap, func(c *config.ResolvedConfig) {
		cfg := c.Configurations["prod"]
		cfg.Bindings = append(cfg.Bindings, contractsconfig.Binding{Protocol: "chat", Models: []string{"x"}, Provider: "ghost"})
		c.Configurations["prod"] = cfg
	})
	if bad.Valid || bad.Error == "" {
		t.Errorf("binding to unknown provider should be invalid, got %+v", bad)
	}

	// previewMutation must not mutate the live snapshot.
	if len(snap.Configurations["prod"].Bindings) != 1 {
		t.Errorf("preview leaked a mutation onto the live snapshot")
	}
}

func TestReferrersToProvider(t *testing.T) {
	snap := validSnapshot()
	got := referrersToProvider(snap, "openai")
	want := []string{"configuration:prod binding", "configuration:prod credentials", "group:g1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("referrersToProvider = %v, want %v", got, want)
	}
	if got := referrersToProvider(snap, "absent"); len(got) != 0 {
		t.Errorf("referrersToProvider(absent) = %v, want []", got)
	}
}

func TestReferrersToGroup(t *testing.T) {
	snap := validSnapshot()
	// g1 is unreferenced by any binding.
	if got := referrersToGroup(snap, "g1"); len(got) != 0 {
		t.Errorf("referrersToGroup(g1) = %v, want []", got)
	}
	// Bind a configuration to the group, then it has a referrer.
	cfg := snap.Configurations["prod"]
	cfg.Bindings = append(cfg.Bindings, contractsconfig.Binding{Protocol: "chat", Models: []string{"o1*"}, Group: "g1"})
	snap.Configurations["prod"] = cfg
	got := referrersToGroup(snap, "g1")
	if !reflect.DeepEqual(got, []string{"configuration:prod binding"}) {
		t.Errorf("referrersToGroup(g1) = %v, want [configuration:prod binding]", got)
	}
}

func TestReferrersToProvider_Passthrough(t *testing.T) {
	snap := validSnapshot()
	cfg := snap.Configurations["prod"]
	cfg.PassthroughBindings = []contractsconfig.PassthroughBinding{{Family: "batches", Provider: "openai"}}
	snap.Configurations["prod"] = cfg

	got := referrersToProvider(snap, "openai")
	found := false
	for _, r := range got {
		if strings.Contains(r, "passthrough") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a passthrough referrer, got %v", got)
	}
}

// TestReferrersToConfiguration_UnnamedKeyNeverLeaksSecret pins the
// inverse of what this test used to assert. It previously required the
// label to BE the raw secret ("api_key:sk_live_y"), which encoded the
// leak as intended behaviour: the 409 body flows into the admin access
// log and any console toast that renders the referrer list.
func TestReferrersToConfiguration_UnnamedKeyNeverLeaksSecret(t *testing.T) {
	snap := validSnapshot()
	snap.APIKeys = append(snap.APIKeys, contractsconfig.APIKey{Secret: "sk_live_y", Configuration: "prod", Enabled: true})

	got := referrersToConfiguration(snap, "prod")
	for _, r := range got {
		if strings.Contains(r, "sk_live_y") {
			t.Fatalf("referrer %q discloses the api-key secret; got %v", r, got)
		}
	}
	// The key must still be identifiable, or the operator cannot act on
	// the conflict. With no name and no ID, that is the positional index.
	found := false
	for _, r := range got {
		if r == "api_key:#1" {
			found = true
		}
	}
	if !found {
		t.Errorf("unnamed key not identified by index; got %v", got)
	}
}

// TestReferrersToConfiguration_UnnamedKeyPrefersID checks the middle rung
// of the fallback chain: name, then ID, then index.
func TestReferrersToConfiguration_UnnamedKeyPrefersID(t *testing.T) {
	snap := validSnapshot()
	id := uuid.MustParse("6f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f")
	snap.APIKeys = append(snap.APIKeys, contractsconfig.APIKey{
		ID: &id, Secret: "sk_live_z", Configuration: "prod", Enabled: true,
	})

	got := referrersToConfiguration(snap, "prod")
	for _, r := range got {
		if strings.Contains(r, "sk_live_z") {
			t.Fatalf("referrer %q discloses the api-key secret; got %v", r, got)
		}
	}
	found := false
	for _, r := range got {
		if r == "api_key:"+id.String() {
			found = true
		}
	}
	if !found {
		t.Errorf("unnamed key with an ID not labelled by ID; got %v", got)
	}
}

func TestReferrersToConfiguration(t *testing.T) {
	snap := validSnapshot()
	got := referrersToConfiguration(snap, "prod")
	if !reflect.DeepEqual(got, []string{"api_key:svc"}) {
		t.Errorf("referrersToConfiguration = %v, want [api_key:svc]", got)
	}
	if got := referrersToConfiguration(snap, "absent"); len(got) != 0 {
		t.Errorf("referrersToConfiguration(absent) = %v, want []", got)
	}
}

func TestReferrersToConnector(t *testing.T) {
	snap := validSnapshot()
	cfg := snap.Configurations["prod"]
	cfg.ConnectorBindings = []contractsconfig.ConnectorBinding{{Connector: "artifacts"}}
	snap.Configurations["prod"] = cfg

	got := referrersToConnector(snap, "artifacts")
	if !reflect.DeepEqual(got, []string{"configuration:prod"}) {
		t.Errorf("referrersToConnector = %v, want [configuration:prod]", got)
	}
	if got := referrersToConnector(snap, "absent"); len(got) != 0 {
		t.Errorf("referrersToConnector(absent) = %v, want []", got)
	}
}
