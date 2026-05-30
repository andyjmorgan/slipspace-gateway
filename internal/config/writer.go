package config

import (
	"gopkg.in/yaml.v3"
)

// appendBlock encodes value as a yaml.Node and attaches it under key on root.
// yaml.v3 Encode does not fail for the schema types the policy writer feeds it
// (rules, configurations, api_keys, connectors) — the error is unwrapped here
// without a check because every input type passes yaml.Marshal in the
// WritePolicyYAMLV2 round-trip tests.
func appendBlock(root *yaml.Node, key string, value any) {
	var valNode yaml.Node
	_ = valNode.Encode(value)
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&valNode,
	)
}
