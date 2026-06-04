package config_test

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
)

func TestConfigurationsConfig_LibraryReferences(t *testing.T) {
	body := `
dev:
  rule_names: [redact-emails, block-pii]
  resilience_name: high-availability
  tags:
    tier: dev
`
	var c contractsconfig.ConfigurationsConfig
	if err := yaml.Unmarshal([]byte(body), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	dev := c["dev"]
	if len(dev.RuleNames) != 2 || dev.RuleNames[0] != "redact-emails" {
		t.Errorf("rule_names = %v", dev.RuleNames)
	}
	if dev.ResilienceName == nil || *dev.ResilienceName != "high-availability" {
		t.Errorf("resilience_name = %v", dev.ResilienceName)
	}
	if dev.Tags["tier"] != "dev" {
		t.Errorf("tags = %v", dev.Tags)
	}
	out, err := yaml.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("empty output")
	}
}

func TestAPIKey_JSONRoundTrip(t *testing.T) {
	//nolint:gosec // test fixture credentials
	in := contractsconfig.APIKey{
		Secret:        "sk_dev_xxx",
		Name:          "test",
		Configuration: "dev",
		Enabled:       true,
	}
	//nolint:gosec // serializing a test fixture
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back contractsconfig.APIKey
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != in {
		t.Errorf("round-trip: %+v != %+v", back, in)
	}
}

func TestAPIKeysConfig_ListShape(t *testing.T) {
	body := `
- secret: sk_dev_a
  name: a
  configuration: dev
  enabled: true
- secret: sk_dev_b
  name: b
  configuration: dev
  enabled: false
`
	var keys contractsconfig.APIKeysConfig
	if err := yaml.Unmarshal([]byte(body), &keys); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("len = %d", len(keys))
	}
	if keys[0].Secret != "sk_dev_a" || keys[1].Enabled {
		t.Errorf("decoded wrong: %+v", keys)
	}
}
