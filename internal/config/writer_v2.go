package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// writableBlockOrder is the stable order blocks are emitted within a file, so a
// rewrite is deterministic (same content -> same bytes).
var writableBlockOrder = []string{
	keyBackends,
	keyGroups,
	keyConfigurations,
	keyAPIKeys,
	keyRules,
	keyConnectors,
	keyAdmin,
	keyTelemetry,
}

// editableBlocks is the set of top-level blocks the admin write surface owns.
// A file is only ever rewritten when it holds one of these — a file containing
// nothing but admin/telemetry is left on disk untouched (comments and layout
// preserved). admin/telemetry are still re-emitted when they share a file with
// an editable block, so a rewrite never drops them.
var editableBlocks = map[string]bool{
	keyBackends:       true,
	keyGroups:         true,
	keyConfigurations: true,
	keyAPIKeys:        true,
	keyRules:          true,
	keyConnectors:     true,
}

// defaultBlockFile routes a block with no recorded SourceFiles origin (e.g. one
// first introduced through the admin API) to a canonical file.
var defaultBlockFile = map[string]string{
	keyBackends:       filenameBackends,
	keyGroups:         filenameBackends,
	keyConfigurations: filenamePolicy,
	keyAPIKeys:        filenamePolicy,
	keyRules:          filenamePolicy,
	keyConnectors:     filenamePolicy,
	keyAdmin:          filenameAdmin,
	keyTelemetry:      filenamePolicy,
}

// WriteConfigV2 persists every editable block of a v2 resolved config back to
// the file it was loaded from (ResolvedConfigV2.SourceFiles), falling back to a
// canonical default for a block with no recorded origin. Each affected file is
// rewritten atomically (temp-file + rename) with the full set of blocks that
// belong to it, in a stable order, so the operator's file layout survives the
// round-trip and a co-located block is never dropped. A block that has been
// emptied (its last entry deleted) is cleared from its file.
//
// Used by the admin write path: clone snapshot -> mutate clone ->
// RevalidateAndIndex -> WriteConfigV2 -> Store.Replace. A failure here aborts
// before Replace, so the in-memory snapshot stays consistent with disk. To keep
// the swap close to all-or-nothing, every target file is marshalled up front;
// only once all marshal successfully are the renames performed.
func WriteConfigV2(dir string, resolved *ResolvedConfigV2) error {
	if resolved == nil {
		return fmt.Errorf("config: write v2: nil resolved config")
	}
	if dir == "" {
		return fmt.Errorf("config: write v2: dir is empty")
	}

	present := blockPresence(resolved)

	fileFor := func(block string) string {
		if f := resolved.SourceFiles[block]; f != "" {
			return f
		}
		return defaultBlockFile[block]
	}

	// Files we must rewrite: those holding a present editable block, plus those
	// whose editable block has been removed (so the stale block is cleared).
	filesToWrite := map[string]bool{}
	for block := range editableBlocks {
		if present[block] {
			filesToWrite[fileFor(block)] = true
		}
		if !present[block] {
			if f := resolved.SourceFiles[block]; f != "" {
				filesToWrite[f] = true
			}
		}
	}

	// Collect the blocks each target file should carry — every present block
	// assigned to it, including admin/telemetry that share the file.
	fileBlocks := map[string][]string{}
	for _, block := range writableBlockOrder {
		if !present[block] {
			continue
		}
		f := fileFor(block)
		if filesToWrite[f] {
			fileBlocks[f] = append(fileBlocks[f], block)
		}
	}

	files := make([]string, 0, len(filesToWrite))
	for f := range filesToWrite {
		files = append(files, f)
	}
	sort.Strings(files)

	// Marshal everything before writing anything.
	bodies := make(map[string][]byte, len(files))
	for _, fname := range files {
		root := &yaml.Node{Kind: yaml.MappingNode}
		for _, block := range fileBlocks[fname] {
			appendResolvedBlock(root, block, resolved)
		}
		body, err := yaml.Marshal(root)
		if err != nil {
			return fmt.Errorf("config: marshal %s: %w", fname, err)
		}
		bodies[fname] = body
	}

	for _, fname := range files {
		if err := atomicWriteFile(filepath.Join(dir, fname), bodies[fname]); err != nil {
			return err
		}
	}
	return nil
}

// WritePolicyYAMLV2 is retained for callers that predate the multi-file router.
// It is now a thin alias for WriteConfigV2, which routes every block (not just
// the policy.yaml-owned ones) to its source file.
func WritePolicyYAMLV2(dir string, resolved *ResolvedConfigV2) error {
	return WriteConfigV2(dir, resolved)
}

// blockPresence reports which top-level blocks have content worth emitting.
// telemetry is a value type with no natural "absent" state, so it is treated as
// present only when the operator authored it (it appears in SourceFiles) —
// otherwise the writer would emit a defaults block that was never on disk.
func blockPresence(r *ResolvedConfigV2) map[string]bool {
	_, telemetryAuthored := r.SourceFiles[keyTelemetry]
	return map[string]bool{
		keyBackends:       len(r.Backends) > 0,
		keyGroups:         len(r.Groups) > 0,
		keyConfigurations: len(r.Configurations) > 0,
		keyAPIKeys:        len(r.APIKeys) > 0,
		keyRules:          len(r.Rules) > 0,
		keyConnectors:     len(r.Connectors) > 0,
		keyAdmin:          r.Admin != nil,
		keyTelemetry:      telemetryAuthored,
	}
}

// appendResolvedBlock attaches one named block of resolved onto root.
func appendResolvedBlock(root *yaml.Node, block string, r *ResolvedConfigV2) {
	switch block {
	case keyBackends:
		appendBlock(root, keyBackends, r.Backends)
	case keyGroups:
		appendBlock(root, keyGroups, r.Groups)
	case keyConfigurations:
		appendBlock(root, keyConfigurations, r.Configurations)
	case keyAPIKeys:
		appendBlock(root, keyAPIKeys, r.APIKeys)
	case keyRules:
		appendBlock(root, keyRules, r.Rules)
	case keyConnectors:
		appendBlock(root, keyConnectors, r.Connectors)
	case keyAdmin:
		appendBlock(root, keyAdmin, r.Admin)
	case keyTelemetry:
		appendBlock(root, keyTelemetry, r.Telemetry)
	}
}

// atomicWriteFile writes body to path via a sibling temp file + rename, so a
// reader never observes a half-written config file.
func atomicWriteFile(path string, body []byte) error {
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, body, 0o600); err != nil {
		return fmt.Errorf("config: write %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("config: rename %s -> %s: %w", tmpPath, path, err)
	}
	return nil
}
