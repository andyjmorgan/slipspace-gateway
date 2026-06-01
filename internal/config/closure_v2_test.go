package config_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	rulescontract "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

func closureTestConfig() *config.ResolvedConfigV2 {
	ruleA := rulescontract.RuleContract{Name: "tag-a"}
	ruleB := rulescontract.RuleContract{Name: "tag-b"}
	return &config.ResolvedConfigV2{
		Backends: contractsconfig.BackendsConfig{
			"openai":    {BaseURL: "https://api.openai.com"},
			"anthropic": {BaseURL: "https://api.anthropic.com"},
			"qwen-a":    {BaseURL: "http://qwen-a:11434"},
			"qwen-b":    {BaseURL: "http://qwen-b:11434"},
		},
		Groups: contractsconfig.GroupsConfig{
			"qwen-lb": {Targets: []contractsconfig.Target{{Backend: "qwen-a"}, {Backend: "qwen-b"}}},
		},
		Connectors: contractsconfig.ConnectorsConfig{
			{Name: "artifacts"},
			{Name: "unused"},
		},
		Configurations: map[string]contractsconfig.ConfigurationV2{
			"alpha": {
				Credentials:       map[string]string{"openai": "sk-alpha-secret"},
				Bindings:          []contractsconfig.Binding{{Protocol: "chat", Models: []string{"gpt-*"}, Backend: "openai"}},
				RuleNames:         []string{"tag-a"},
				ConnectorBindings: []contractsconfig.ConnectorBinding{{Connector: "artifacts"}},
			},
			"beta": {
				Credentials: map[string]string{"anthropic": "sk-beta-secret"},
				Bindings:    []contractsconfig.Binding{{Protocol: "messages", Models: []string{"claude-*"}, Backend: "anthropic"}},
				RuleNames:   []string{"tag-b"},
			},
			"gamma": {
				Credentials: map[string]string{"qwen-a": "", "qwen-b": ""},
				Bindings:    []contractsconfig.Binding{{Protocol: "chat", Models: []string{"qwen*"}, Group: "qwen-lb"}},
			},
		},
		APIKeys: contractsconfig.APIKeysConfig{
			{Secret: "sk_live_alpha", Name: "alpha-key", Configuration: "alpha", Enabled: true}, //nolint:gosec // test fixture, not a credential
			{Secret: "sk_live_beta", Name: "beta-key", Configuration: "beta", Enabled: true},    //nolint:gosec // test fixture, not a credential
		},
		PerConfigurationRules: map[string][]*rulescontract.RuleContract{
			"alpha": {&ruleA},
			"beta":  {&ruleB},
		},
	}
}

func TestMarshalClosure_ScopesToConfiguration(t *testing.T) {
	resolved := closureTestConfig()

	body, hash, err := config.MarshalClosure(resolved, "alpha")
	if err != nil {
		t.Fatalf("MarshalClosure: %v", err)
	}
	if hash == "" {
		t.Fatal("empty hash")
	}
	s := string(body)

	// alpha's own dependencies are present.
	for _, want := range []string{"openai", "sk-alpha-secret", "tag-a", "alpha-key", "artifacts"} {
		if !strings.Contains(s, want) {
			t.Errorf("closure missing %q:\n%s", want, s)
		}
	}
	// beta's secrets and deps must NOT leak into alpha's closure (CP-3).
	for _, leak := range []string{"anthropic", "sk-beta-secret", "tag-b", "beta-key", "sk_live_beta", "unused"} {
		if strings.Contains(s, leak) {
			t.Errorf("closure leaked %q from another configuration:\n%s", leak, s)
		}
	}
}

func TestMarshalClosure_GroupPullsTargetBackends(t *testing.T) {
	body, _, err := config.MarshalClosure(closureTestConfig(), "gamma")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{"qwen-lb", "qwen-a", "qwen-b"} {
		if !strings.Contains(s, want) {
			t.Errorf("group closure missing %q:\n%s", want, s)
		}
	}
	// gamma must not carry alpha/beta backends.
	if strings.Contains(s, "openai") || strings.Contains(s, "anthropic") {
		t.Errorf("group closure leaked unrelated backend:\n%s", s)
	}
}

func TestMarshalClosure_Deterministic(t *testing.T) {
	resolved := closureTestConfig()
	_, h1, err := config.MarshalClosure(resolved, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	_, h2, err := config.MarshalClosure(resolved, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("hash not deterministic: %s != %s", h1, h2)
	}
}

func TestMarshalClosure_UnknownConfiguration(t *testing.T) {
	_, _, err := config.MarshalClosure(closureTestConfig(), "ghost")
	if !errors.Is(err, config.ErrUnknownConfiguration) {
		t.Fatalf("err = %v, want ErrUnknownConfiguration", err)
	}
}

func TestMarshalClosure_NilResolved(t *testing.T) {
	if _, _, err := config.MarshalClosure(nil, "x"); err == nil {
		t.Fatal("want error for nil resolved config")
	}
}

func TestResolveClosure_RoundTripFromConfigDev(t *testing.T) {
	resolved, err := config.LoadV2(context.Background(), "../../config-dev")
	if err != nil {
		t.Skipf("config-dev not loadable from this path: %v", err)
	}
	if len(resolved.APIKeys) == 0 {
		t.Skip("config-dev has no api keys")
	}
	// A configuration that has at least one api-key, so its closure is non-trivial.
	name := resolved.APIKeys[0].Configuration

	body, hash, err := config.MarshalClosure(resolved, name)
	if err != nil {
		t.Fatalf("MarshalClosure(%q): %v", name, err)
	}
	if hash == "" {
		t.Fatal("empty hash")
	}

	rc, err := config.ResolveClosure(body)
	if err != nil {
		t.Fatalf("ResolveClosure: %v", err)
	}
	if _, ok := rc.Configurations[name]; !ok {
		t.Fatalf("round-tripped closure missing configuration %q", name)
	}
	if len(rc.SecretIndex) == 0 {
		t.Error("SecretIndex empty after ResolveClosure — api-keys not rebuilt")
	}
}

func TestResolveClosure_MalformedBytes(t *testing.T) {
	if _, err := config.ResolveClosure([]byte("{{{ not yaml")); err == nil {
		t.Fatal("want a parse error")
	}
}

func TestResolveClosure_ValidationFailure(t *testing.T) {
	// A binding referencing a backend that does not exist must fail Validate,
	// so a bad closure never reaches a live snapshot.
	bad := []byte("configurations:\n  x:\n    bindings:\n      - {protocol: chat, models: [\"a\"], backend: ghost}\n")
	if _, err := config.ResolveClosure(bad); err == nil {
		t.Fatal("want a validation error for the dangling backend reference")
	}
}
