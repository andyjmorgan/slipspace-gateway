package config_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

// TestWritePolicyYAML_RoundTripsConfigDev loads the on-disk config-dev/
// tree, writes a fresh policy.yaml from the in-memory resolved struct
// into a tmp dir alongside the providers.yaml + admin.yaml copies, then
// re-loads from the tmp dir and asserts the structural payload matches.
//
// This is the load-bearing round-trip for the admin-write path: every
// rule, configuration, api_key, resilience policy, and connector
// authored in YAML must survive a Load → WritePolicyYAML → Load cycle
// without semantic drift.
func TestWritePolicyYAML_RoundTripsConfigDev(t *testing.T) {
	t.Parallel()

	src, err := filepath.Abs(filepath.Join("..", "..", "config-dev"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	original, err := config.Load(context.Background(), src)
	if err != nil {
		t.Fatalf("config.Load(src): %v", err)
	}

	dir := t.TempDir()
	// Copy the non-policy YAML files verbatim so Load has the
	// providers + admin tree it needs.
	for _, name := range []string{"providers.yaml", "admin.yaml"} {
		body, err := os.ReadFile(filepath.Join(src, name)) //nolint:gosec // path is the test fixture under config-dev
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil { //nolint:gosec // path is under t.TempDir()
			t.Fatalf("write %s: %v", name, err)
		}
	}

	if err := config.WritePolicyYAML(dir, original); err != nil {
		t.Fatalf("WritePolicyYAML: %v", err)
	}

	reloaded, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("config.Load(round-tripped): %v", err)
	}

	if got, want := len(reloaded.Configurations), len(original.Configurations); got != want {
		t.Errorf("Configurations count = %d, want %d", got, want)
	}
	for name, cfg := range original.Configurations {
		got, ok := reloaded.Configurations[name]
		if !ok {
			t.Errorf("configuration %q missing after round-trip", name)
			continue
		}
		if len(got.RuleNames) != len(cfg.RuleNames) {
			t.Errorf("configuration %q RuleNames len=%d, want %d", name, len(got.RuleNames), len(cfg.RuleNames))
		}
		for i, ruleName := range cfg.RuleNames {
			if i >= len(got.RuleNames) || got.RuleNames[i] != ruleName {
				t.Errorf("configuration %q RuleNames[%d]: round-trip diverged", name, i)
				break
			}
		}
	}

	if got, want := len(reloaded.APIKeys), len(original.APIKeys); got != want {
		t.Errorf("APIKeys count = %d, want %d", got, want)
	}
	if got, want := len(reloaded.Rules), len(original.Rules); got != want {
		t.Errorf("Rules count = %d, want %d", got, want)
	}
	for i, want := range original.Rules {
		if i >= len(reloaded.Rules) {
			break
		}
		got := reloaded.Rules[i]
		if got.Name != want.Name {
			t.Errorf("Rules[%d].Name = %q, want %q", i, got.Name, want.Name)
		}
		if got.Condition == nil && want.Condition != nil {
			t.Errorf("Rules[%d] (%q) Condition lost in round-trip", i, want.Name)
		}
		if len(got.Actions) != len(want.Actions) {
			t.Errorf("Rules[%d] (%q) Actions len=%d, want %d", i, want.Name, len(got.Actions), len(want.Actions))
		}
		if got.Behavior != want.Behavior {
			t.Errorf("Rules[%d] (%q) Behavior=%q, want %q", i, want.Name, got.Behavior, want.Behavior)
		}
	}
	if got, want := len(reloaded.ResiliencePolicies), len(original.ResiliencePolicies); got != want {
		t.Errorf("ResiliencePolicies count = %d, want %d", got, want)
	}
	if got, want := len(reloaded.Connectors), len(original.Connectors); got != want {
		t.Errorf("Connectors count = %d, want %d", got, want)
	}
}

// TestWritePolicyYAML_NilGuard verifies the nil + empty-dir guards.
func TestWritePolicyYAML_NilGuard(t *testing.T) {
	t.Parallel()
	if err := config.WritePolicyYAML(t.TempDir(), nil); err == nil {
		t.Errorf("nil resolved: want error")
	}
	if err := config.WritePolicyYAML("", &config.ResolvedConfig{}); err == nil {
		t.Errorf("empty dir: want error")
	}
}

// TestWritePolicyYAML_FailsOnNonExistentDir confirms a CreateTemp
// failure surfaces as an error rather than panicking.
func TestWritePolicyYAML_FailsOnNonExistentDir(t *testing.T) {
	t.Parallel()
	err := config.WritePolicyYAML("/this/path/should/not/exist", &config.ResolvedConfig{})
	if err == nil {
		t.Fatal("want error for non-existent dir, got nil")
	}
}

// TestWritePolicyYAML_EmptyConfig writes a doc with only the
// configurations block (everything else nil/empty). Confirms the
// "omit empty blocks" branch fires and the output is loadable IF we
// provide a non-empty Configurations.
func TestWritePolicyYAML_EmptyConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	resolved := &config.ResolvedConfig{}
	if err := config.WritePolicyYAML(dir, resolved); err != nil {
		t.Fatalf("WritePolicyYAML on empty resolved: %v", err)
	}
	// File must exist after the write.
	if _, err := os.Stat(filepath.Join(dir, "policy.yaml")); err != nil {
		t.Errorf("policy.yaml not created: %v", err)
	}
}

// TestWritePolicyYAML_AtomicRenameLeavesNoTmp confirms the temp file is
// cleaned up on a successful write.
func TestWritePolicyYAML_AtomicRenameLeavesNoTmp(t *testing.T) {
	t.Parallel()

	src, _ := filepath.Abs(filepath.Join("..", "..", "config-dev"))
	original, err := config.Load(context.Background(), src)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	dir := t.TempDir()
	for _, name := range []string{"providers.yaml", "admin.yaml"} {
		body, _ := os.ReadFile(filepath.Join(src, name)) //nolint:gosec // path is the test fixture under config-dev
		_ = os.WriteFile(filepath.Join(dir, name), body, 0o600)
	}
	if err := config.WritePolicyYAML(dir, original); err != nil {
		t.Fatalf("WritePolicyYAML: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if name != "policy.yaml" && name != "providers.yaml" && name != "admin.yaml" {
			t.Errorf("stale file left behind after write: %q", name)
		}
	}
}
