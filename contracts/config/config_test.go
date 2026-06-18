package config_test

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
)

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
