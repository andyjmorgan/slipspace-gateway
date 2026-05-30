package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// WritePolicyYAMLV2 serialises the policy.yaml-owned blocks of a v2 resolved
// config and writes them to dir/policy.yaml via the temp-file + rename atomic
// pattern. Only the blocks the admin write path may mutate are emitted —
// configurations, api_keys, rules, connectors. Backends and groups are
// authored in separate files and are not editable through the rules API, so
// they are deliberately left untouched on disk.
//
// Used by the admin rules-write path: clone snapshot → mutate clone →
// RevalidateAndIndex → WritePolicyYAMLV2 → Store.Replace. A failure here aborts
// before Replace, so the in-memory snapshot stays consistent with disk.
func WritePolicyYAMLV2(dir string, resolved *ResolvedConfigV2) error {
	if resolved == nil {
		return fmt.Errorf("config: write policy.yaml: nil resolved config")
	}
	if dir == "" {
		return fmt.Errorf("config: write policy.yaml: dir is empty")
	}

	doc := buildPolicyDocumentV2(resolved)
	body, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("config: marshal policy.yaml: %w", err)
	}

	target := filepath.Join(dir, filenamePolicy)
	tmpPath := target + ".tmp"
	if err := os.WriteFile(tmpPath, body, 0o600); err != nil {
		return fmt.Errorf("config: write %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return fmt.Errorf("config: rename %s -> %s: %w", tmpPath, target, err)
	}
	return nil
}

// buildPolicyDocumentV2 lays out the v2 policy.yaml top-level blocks in a
// stable emitted order. Empty blocks are omitted.
func buildPolicyDocumentV2(resolved *ResolvedConfigV2) *yaml.Node {
	root := &yaml.Node{Kind: yaml.MappingNode}

	if len(resolved.Configurations) > 0 {
		appendBlock(root, keyConfigurations, resolved.Configurations)
	}
	if len(resolved.APIKeys) > 0 {
		appendBlock(root, keyAPIKeys, resolved.APIKeys)
	}
	if len(resolved.Rules) > 0 {
		appendBlock(root, keyRules, resolved.Rules)
	}
	if len(resolved.Connectors) > 0 {
		appendBlock(root, keyConnectors, resolved.Connectors)
	}

	return root
}
