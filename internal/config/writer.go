package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// appendBlock encodes value as a yaml.Node and attaches it under key on root.
//
// The error is returned rather than dropped. yaml.v3's Encode may well be
// infallible for the schema types the writer feeds it today, but discarding
// it made the failure mode catastrophic and silent: a zero-valued yaml.Node
// is appended under the key, so the rewritten file loses that entire
// top-level block while the running process keeps the correct in-memory
// snapshot from store.Replace. The divergence stays invisible until the next
// restart, at which point the block — every api_key, say, or every connector
// — is simply gone, with no error, no log line, and no metric.
//
// Persisting before store.Replace exists precisely so on-disk and in-memory
// states cannot diverge (invariant #9); a persist step that cannot complete
// has to fail loudly for that ordering to mean anything.
func appendBlock(root *yaml.Node, key string, value any) error {
	var valNode yaml.Node
	if err := valNode.Encode(value); err != nil {
		return fmt.Errorf("config: encode block %q: %w", key, err)
	}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&valNode,
	)
	return nil
}
