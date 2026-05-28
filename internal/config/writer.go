package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// WritePolicyYAML serialises every policy.yaml-owned top-level block on
// resolved into a fresh YAML document and writes it to dir/policy.yaml
// via the standard temp-file + rename atomic pattern. The temp file
// lives alongside the target so the rename is guaranteed atomic on the
// same filesystem.
//
// Block ordering on disk mirrors policy.yaml's documented authoring
// order (configurations → api_keys → rules → resilience_policies →
// connectors). Operator-authored comments and field ordering inside
// a block are NOT preserved — the write path is a "full re-marshal of
// policy.yaml" per the Phase 2 design decision; an admin who relies
// on comments needs to layer them back in after the next write.
// Empty top-level blocks are omitted from the output.
//
// Used by the admin-write path: clone snapshot → mutate clone →
// Validate clone → WritePolicyYAML → Store.Replace. Failures here
// abort the write before Replace runs, so the in-memory snapshot
// stays consistent with disk.
func WritePolicyYAML(dir string, resolved *ResolvedConfig) error {
	if resolved == nil {
		return fmt.Errorf("config: write policy.yaml: nil resolved config")
	}
	if dir == "" {
		return fmt.Errorf("config: write policy.yaml: dir is empty")
	}

	doc := buildPolicyDocument(resolved)
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
		// Leave the temp file behind so an operator can diagnose; the
		// next successful write will overwrite it.
		return fmt.Errorf("config: rename %s -> %s: %w", tmpPath, target, err)
	}
	return nil
}

// buildPolicyDocument lays out the policy.yaml top-level blocks in the
// emitted order. Uses yaml.Node so the order is stable across writes
// — a plain map[string]any would be re-ordered by yaml.v3's emitter
// based on map iteration, which Go intentionally randomises.
func buildPolicyDocument(resolved *ResolvedConfig) *yaml.Node {
	root := &yaml.Node{Kind: yaml.MappingNode}

	appendBlock(root, keyConfigurations, resolved.Configurations)
	if len(resolved.APIKeys) > 0 {
		appendBlock(root, keyAPIKeys, resolved.APIKeys)
	}
	if len(resolved.Rules) > 0 {
		appendBlock(root, keyRules, resolved.Rules)
	}
	if len(resolved.ResiliencePolicies) > 0 {
		appendBlock(root, keyResiliencePolicies, resolved.ResiliencePolicies)
	}
	if len(resolved.Connectors) > 0 {
		appendBlock(root, keyConnectors, resolved.Connectors)
	}

	return root
}

// appendBlock encodes value as a yaml.Node and attaches it under key
// on root. yaml.v3 Encode does not fail for the schema types this
// writer feeds it (rules, configurations, api_keys, etc.) — the
// error is unwrapped here without a check because every input type
// passes yaml.Marshal in the WritePolicyYAML round-trip tests.
func appendBlock(root *yaml.Node, key string, value any) {
	var valNode yaml.Node
	_ = valNode.Encode(value)
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&valNode,
	)
}
