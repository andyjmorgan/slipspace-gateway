package admin

import (
	"reflect"
	"strings"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
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
		{"explicit empty clears (no-cred backend)", &empty, "stored", ""},
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
func validSnapshot() *config.ResolvedConfigV2 {
	rc := &config.ResolvedConfigV2{
		Backends: contractsconfig.BackendsConfig{
			"openai": {
				BaseURL:   "https://api.openai.com",
				Protocols: map[string]contractsconfig.BackendProtocol{"chat": {Path: "/v1/chat/completions"}},
			},
		},
		Groups: contractsconfig.GroupsConfig{
			"g1": {Mode: "failover", Targets: []contractsconfig.Target{{Backend: "openai"}}},
		},
		Configurations: map[string]contractsconfig.ConfigurationV2{
			"prod": {
				Credentials:         map[string]string{"openai": "sk"},
				Bindings:            []contractsconfig.Binding{{Protocol: "chat", Models: []string{"gpt-*"}, Backend: "openai"}},
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

	noop := previewMutation(snap, func(*config.ResolvedConfigV2) {})
	if !noop.Valid || noop.Error != "" {
		t.Errorf("noop preview should be valid, got %+v", noop)
	}

	bad := previewMutation(snap, func(c *config.ResolvedConfigV2) {
		cfg := c.Configurations["prod"]
		cfg.Bindings = append(cfg.Bindings, contractsconfig.Binding{Protocol: "chat", Models: []string{"x"}, Backend: "ghost"})
		c.Configurations["prod"] = cfg
	})
	if bad.Valid || bad.Error == "" {
		t.Errorf("binding to unknown backend should be invalid, got %+v", bad)
	}

	// previewMutation must not mutate the live snapshot.
	if len(snap.Configurations["prod"].Bindings) != 1 {
		t.Errorf("preview leaked a mutation onto the live snapshot")
	}
}

func TestReferrersToBackend(t *testing.T) {
	snap := validSnapshot()
	got := referrersToBackend(snap, "openai")
	want := []string{"configuration:prod binding", "configuration:prod credentials", "group:g1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("referrersToBackend = %v, want %v", got, want)
	}
	if got := referrersToBackend(snap, "absent"); len(got) != 0 {
		t.Errorf("referrersToBackend(absent) = %v, want []", got)
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

func TestReferrersToBackend_Passthrough(t *testing.T) {
	snap := validSnapshot()
	cfg := snap.Configurations["prod"]
	cfg.PassthroughBindings = []contractsconfig.PassthroughBinding{{Family: "batches", Backend: "openai"}}
	snap.Configurations["prod"] = cfg

	got := referrersToBackend(snap, "openai")
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

func TestReferrersToConfiguration_SecretLabelFallback(t *testing.T) {
	snap := validSnapshot()
	snap.APIKeys = append(snap.APIKeys, contractsconfig.APIKey{Secret: "sk_live_y", Configuration: "prod", Enabled: true})

	got := referrersToConfiguration(snap, "prod")
	hasSecretLabel := false
	for _, r := range got {
		if r == "api_key:sk_live_y" {
			hasSecretLabel = true
		}
	}
	if !hasSecretLabel {
		t.Errorf("expected a secret-labelled referrer for the unnamed key, got %v", got)
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
