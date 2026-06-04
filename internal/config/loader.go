package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Top-level block keys the v2 writer emits. Kept here (rather than in the
// writer) because they double as the canonical block-name vocabulary — the same
// strings ResolvedConfig.SourceFiles is keyed by.
const (
	keyProviders      = "providers"
	keyGroups         = "groups"
	keyConfigurations = "configurations"
	keyAPIKeys        = "api_keys"
	keyRules          = "rules"
	keyConnectors     = "connectors"
	keyAdmin          = "admin"
	keyTelemetry      = "telemetry"
)

// Canonical filenames the writer falls back to for a block with no recorded
// SourceFiles origin (e.g. a block first introduced through the admin API).
const (
	filenameProviders = "providers.yaml"
	filenamePolicy    = "policy.yaml"
	filenameAdmin     = "admin.yaml"
)

// ListConfigFiles enumerates the *.yaml files in dir and returns a map of
// filename to absolute path. Under the v2 model the loader merges every *.yaml
// by top-level key, so there is no fixed-filename allowlist; this surfaces the
// same file set to the admin config-export bundler. Subdirectories and
// non-yaml entries are skipped.
func ListConfigFiles(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("config: read dir %q: %w", dir, err)
	}
	out := make(map[string]string, len(entries))
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		out[name] = filepath.Join(dir, name)
	}
	return out, nil
}
