package config_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

// FuzzLoad drives random YAML payloads through Load and asserts the loader
// never panics. Any returned error must be a typed error (the loader has no
// untyped error paths), and successful loads must produce a non-nil result.
func FuzzLoad(f *testing.F) {
	seeds := []string{
		"",
		"providers: {}\nconfigurations: {dev: {}}\n",
		"api_keys:\n  - secret: x\n    configuration: dev\n",
		"::: not yaml :::\n  - [unclosed",
		"- a\n- b\n- c\n",
		"mystery:\n  foo: bar\n",
		sampleProviders,
		sampleConfigurations,
		sampleAPIKeys,
		sampleProviders + sampleConfigurations,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, body string) {
		dir := t.TempDir()
		p := filepath.Join(dir, "fuzz.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}

		resolved, err := config.Load(context.Background(), dir)
		if err != nil {
			if resolved != nil {
				t.Errorf("error returned with non-nil resolved: %v", err)
			}
			return
		}
		if resolved == nil {
			t.Fatal("nil resolved, nil error")
		}
		if resolved.SecretIndex == nil || resolved.ConfigurationIndex == nil || resolved.RouteIndex == nil {
			t.Errorf("indexes not initialized: %+v", resolved)
		}
	})
}
