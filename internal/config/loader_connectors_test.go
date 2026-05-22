package config_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

const sampleConnectorsBlock = `
connectors:
  - name: refinement-s3
    type: s3
    bucket: sluice-refinement
    region: eu-west-2
    auth:
      mode: workload_identity
  - name: customer-acme
    type: webhook
    url: https://hooks.example/sluice
    secret_ref: env:HOOK_SECRET
    timeout_ms: 5000
  - name: backup-azure
    type: azure_blob
    account: myaccount
    container: sluice
    auth:
      mode: account_key
      account_key_ref: env:AZURE_KEY
`

const sampleConfigurationsWithBindings = `
configurations:
  dev:
    upstream_credentials:
      openai: sk-dev-mock
    rule_names:
      - redact-emails
    resilience_name: ha
    connector_bindings:
      - connector: refinement-s3
      - connector: customer-acme
        sampling: 0.1
        sampling_key: correlation_id
        max_body_bytes: 1048576
        filter:
          providers: [openai]
          endpoints: [chat_completions]
          status_min: 200
          status_max: 599
`

func loadWithConnectors(t *testing.T, dir, configurations, connectors string) (*config.ResolvedConfig, error) {
	t.Helper()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	policy := configurations + sampleAPIKeys + sampleRulesLibrary + sampleResiliencePoliciesLibrary + connectors
	writeFile(t, dir, "policy.yaml", policy)
	return config.Load(context.Background(), dir)
}

func TestLoad_ConnectorsHappyPath(t *testing.T) {
	dir := t.TempDir()
	r, err := loadWithConnectors(t, dir, sampleConfigurationsWithBindings, sampleConnectorsBlock)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(r.Connectors) != 3 {
		t.Errorf("Connectors len = %d, want 3", len(r.Connectors))
	}
	for _, name := range []string{"refinement-s3", "customer-acme", "backup-azure"} {
		if r.ConnectorIndex[name] == nil {
			t.Errorf("ConnectorIndex missing %q", name)
		}
	}
	dev := r.ConfigurationIndex["dev"]
	if dev == nil {
		t.Fatal("ConfigurationIndex missing dev")
	}
	if len(dev.ConnectorBindings) != 2 {
		t.Errorf("dev.ConnectorBindings len = %d, want 2", len(dev.ConnectorBindings))
	}
	if dev.ConnectorBindings[1].Sampling != 0.1 {
		t.Errorf("acme sampling = %v, want 0.1", dev.ConnectorBindings[1].Sampling)
	}
}

func TestLoad_DuplicateConnectorName(t *testing.T) {
	dir := t.TempDir()
	connectors := `
connectors:
  - name: dup
    type: s3
    bucket: a
    region: r
  - name: dup
    type: webhook
    url: https://x
    secret_ref: env:X
    timeout_ms: 1000
`
	_, err := loadWithConnectors(t, dir, sampleConfigurations, connectors)
	if !errors.Is(err, config.ErrDuplicateConnectorName) {
		t.Errorf("expected config.ErrDuplicateConnectorName, got %v", err)
	}
}

func TestLoad_UnknownConnectorReference(t *testing.T) {
	dir := t.TempDir()
	configurations := `
configurations:
  dev:
    upstream_credentials:
      openai: sk-x
    rule_names: [redact-emails]
    resilience_name: ha
    connector_bindings:
      - connector: not-defined
`
	_, err := loadWithConnectors(t, dir, configurations, sampleConnectorsBlock)
	if !errors.Is(err, config.ErrUnknownConnectorReference) {
		t.Errorf("expected config.ErrUnknownConnectorReference, got %v", err)
	}
}

func TestLoad_ConnectorValidationFailureSurfaces(t *testing.T) {
	dir := t.TempDir()
	connectors := `
connectors:
  - name: bad
    type: s3
    region: r
    # missing bucket
`
	_, err := loadWithConnectors(t, dir, sampleConfigurations, connectors)
	if err == nil || !strings.Contains(err.Error(), "bucket is required") {
		t.Errorf("expected bucket-required error, got %v", err)
	}
}

func TestLoad_BindingValidationFailureSurfaces(t *testing.T) {
	dir := t.TempDir()
	configurations := `
configurations:
  dev:
    upstream_credentials:
      openai: sk-x
    rule_names: [redact-emails]
    resilience_name: ha
    connector_bindings:
      - connector: refinement-s3
        sampling: 2.5
`
	_, err := loadWithConnectors(t, dir, configurations, sampleConnectorsBlock)
	if err == nil || !strings.Contains(err.Error(), "sampling") {
		t.Errorf("expected sampling range error, got %v", err)
	}
}

func TestLoad_NoConnectorsBlockIsLegal(t *testing.T) {
	// A policy.yaml without `connectors:` should still load; the
	// resulting ResolvedConfig has zero connectors and any
	// connector_bindings on a configuration would have errored.
	dir := t.TempDir()
	r, err := loadWithConnectors(t, dir, sampleConfigurations, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(r.Connectors) != 0 {
		t.Errorf("Connectors = %v, want empty", r.Connectors)
	}
	if r.ConnectorIndex == nil {
		t.Error("ConnectorIndex should be initialised (empty map)")
	}
}

func TestLoad_OrphanedConnectorIsLegal(t *testing.T) {
	// Defining a connector that no configuration binds is legal — the
	// operator might be preparing to wire it.
	dir := t.TempDir()
	connectors := `
connectors:
  - name: nobody-uses-me
    type: webhook
    url: https://x
    secret_ref: env:X
    timeout_ms: 1000
`
	r, err := loadWithConnectors(t, dir, sampleConfigurations, connectors)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.ConnectorIndex["nobody-uses-me"] == nil {
		t.Error("orphan connector should still be indexed")
	}
}

func TestLoad_ConnectorsInWrongFileRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders+`
connectors:
  - name: misplaced
    type: webhook
    url: https://x
    secret_ref: env:X
    timeout_ms: 1000
`)
	writeFile(t, dir, "policy.yaml", sampleConfigurations+sampleAPIKeys+sampleRulesLibrary+sampleResiliencePoliciesLibrary)
	_, err := config.Load(context.Background(), dir)
	if !errors.Is(err, config.ErrWrongFileForKey) {
		t.Errorf("expected config.ErrWrongFileForKey, got %v", err)
	}
}

func TestLoad_ConnectorIndexPointersIntoSlice(t *testing.T) {
	dir := t.TempDir()
	r, err := loadWithConnectors(t, dir, sampleConfigurationsWithBindings, sampleConnectorsBlock)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Ensure the index pointers reference entries in the Connectors
	// slice — mutating the index entry must be visible in the slice
	// (and vice versa) so callers can't get stale views via the index.
	target := "refinement-s3"
	idx := r.ConnectorIndex[target]
	if idx == nil {
		t.Fatal("ConnectorIndex missing refinement-s3")
	}
	var slicePtr *contractsconfig.Connector
	for i := range r.Connectors {
		if r.Connectors[i].Name == target {
			slicePtr = &r.Connectors[i]
			break
		}
	}
	if slicePtr == nil || slicePtr != idx {
		t.Errorf("ConnectorIndex[%q]=%p, slice ptr=%p — should be the same address", target, idx, slicePtr)
	}
}
