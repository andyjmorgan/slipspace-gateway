package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
)

const (
	fxProviders = `providers:
  openai:
    base_url: https://api.openai.com
    protocols:
      chat:
        path: /v1/chat/completions
groups:
  g1:
    mode: failover
    targets:
      - provider: openai
`
	fxPolicy = `configurations:
  prod:
    credentials:
      openai: sk-test
    bindings:
      - protocol: chat
        models: ["gpt-*"]
        provider: openai
api_keys:
  - secret: sk_live_x
    name: k1
    configuration: prod
    enabled: true
`
	fxAdmin = `admin:
  enabled: true
  bind_addr: ":8081"
`
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func readFile(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // test reads a file under t.TempDir()
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return b
}

func loadDir(t *testing.T, dir string) *ResolvedConfig {
	t.Helper()
	rc, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return rc
}

func TestLoad_SourceFiles(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"providers.yaml": fxProviders,
		"policy.yaml":    fxPolicy,
		"admin.yaml":     fxAdmin,
	})
	rc := loadDir(t, dir)

	want := map[string]string{
		"providers":      "providers.yaml",
		"groups":         "providers.yaml",
		"configurations": "policy.yaml",
		"api_keys":       "policy.yaml",
		"admin":          "admin.yaml",
	}
	for block, file := range want {
		if got := rc.SourceFiles[block]; got != file {
			t.Errorf("SourceFiles[%q] = %q, want %q", block, got, file)
		}
	}
	if _, ok := rc.SourceFiles["telemetry"]; ok {
		t.Errorf("telemetry should not be recorded when absent from config")
	}
}

func TestWriteConfig_RoundTripInPlace(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"providers.yaml": fxProviders,
		"policy.yaml":    fxPolicy,
		"admin.yaml":     fxAdmin,
	})
	adminBefore := readFile(t, dir, "admin.yaml")

	rc := loadDir(t, dir)
	if err := WriteConfig(dir, rc); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	got := loadDir(t, dir)
	if got.Providers["openai"].BaseURL != "https://api.openai.com" {
		t.Errorf("provider base_url lost on round-trip: %q", got.Providers["openai"].BaseURL)
	}
	if len(got.Groups) != 1 {
		t.Errorf("groups = %d, want 1", len(got.Groups))
	}
	if got.Configurations["prod"].Credentials["openai"] != "sk-test" {
		t.Errorf("credential lost on round-trip")
	}
	if len(got.APIKeys) != 1 || got.APIKeys[0].Secret != "sk_live_x" {
		t.Errorf("api_keys lost on round-trip: %+v", got.APIKeys)
	}
	if got.Admin == nil || !got.Admin.Enabled {
		t.Errorf("admin block lost on round-trip")
	}

	// A standalone admin.yaml (no editable block) must not be rewritten.
	adminAfter := readFile(t, dir, "admin.yaml")
	if string(adminBefore) != string(adminAfter) {
		t.Errorf("standalone admin.yaml was rewritten:\nbefore=%q\nafter=%q", adminBefore, adminAfter)
	}
}

func TestWriteConfig_ProviderEditLandsInProvidersFile(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"providers.yaml": fxProviders,
		"policy.yaml":    fxPolicy,
	})
	rc := loadDir(t, dir)

	clone := rc.Clone()
	be := clone.Providers["openai"]
	be.BaseURL = "https://moved.example.com"
	clone.Providers["openai"] = be
	if err := clone.RevalidateAndIndex(); err != nil {
		t.Fatalf("revalidate: %v", err)
	}
	if err := WriteConfig(dir, clone); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	providersBytes := readFile(t, dir, "providers.yaml")
	policyBytes := readFile(t, dir, "policy.yaml")
	if !strings.Contains(string(providersBytes), "moved.example.com") {
		t.Errorf("edited provider did not land in providers.yaml")
	}
	if strings.Contains(string(policyBytes), "moved.example.com") {
		t.Errorf("provider leaked into policy.yaml")
	}
	if got := loadDir(t, dir).Providers["openai"].BaseURL; got != "https://moved.example.com" {
		t.Errorf("reloaded base_url = %q, want moved", got)
	}
}

func TestWriteConfig_ClearRemovedBlock(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"providers.yaml": fxProviders,
		"policy.yaml":    fxPolicy,
	})
	rc := loadDir(t, dir)

	clone := rc.Clone()
	clone.Groups = nil // delete the last group
	if err := clone.RevalidateAndIndex(); err != nil {
		t.Fatalf("revalidate: %v", err)
	}
	if err := WriteConfig(dir, clone); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	got := loadDir(t, dir)
	if len(got.Groups) != 0 {
		t.Errorf("groups not cleared: %d", len(got.Groups))
	}
	if got.Providers["openai"].BaseURL == "" {
		t.Errorf("providers dropped when clearing groups from the same file")
	}
}

func TestWriteConfig_NewBlockDefaultFile(t *testing.T) {
	dir := t.TempDir()
	// providers.yaml carries only providers — no groups block, so "groups" has
	// no recorded source file.
	writeFiles(t, dir, map[string]string{
		"providers.yaml": `providers:
  openai:
    base_url: https://api.openai.com
    protocols:
      chat:
        path: /v1/chat/completions
`,
		"policy.yaml": fxPolicy,
	})
	rc := loadDir(t, dir)
	if _, ok := rc.SourceFiles["groups"]; ok {
		t.Fatalf("groups unexpectedly has a source file")
	}

	clone := rc.Clone()
	clone.Groups = contractsconfig.GroupsConfig{
		"g2": {Mode: "failover", Targets: []contractsconfig.Target{{Provider: "openai"}}},
	}
	if err := clone.RevalidateAndIndex(); err != nil {
		t.Fatalf("revalidate: %v", err)
	}
	if err := WriteConfig(dir, clone); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	// groups had no origin -> defaults to providers.yaml.
	providersBytes := readFile(t, dir, "providers.yaml")
	if !strings.Contains(string(providersBytes), "g2") {
		t.Errorf("new group did not default into providers.yaml")
	}
	if len(loadDir(t, dir).Groups) != 1 {
		t.Errorf("new group did not reload")
	}
}
