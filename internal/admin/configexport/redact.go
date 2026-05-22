package configexport

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// RedactedPlaceholder is the literal string substituted for every declared
// secret-bearing leaf value. Fixed and short so an operator scanning the
// export sees "***" everywhere a credential lived, with no partial reveal.
const RedactedPlaceholder = "***"

// Redact parses content as a YAML document, replaces declared secret-bearing
// leaf values with RedactedPlaceholder, and returns the re-marshalled bytes.
//
// The walker is deny-by-default: only the top-level keys handled in the switch
// below are touched. A new YAML file shape with secrets in a key this function
// does not recognise will pass through unchanged, which is intentional — it
// forces the operator to update this function (and its test) when introducing
// a new secret-bearing field. Regex-over-key-names would silently leak the
// first time someone added a field that did not match the regex.
//
// Comments and key ordering survive because yaml.Node round-trips them; only
// the affected scalar values change.
func Redact(content []byte) ([]byte, error) {
	if len(content) == 0 {
		return content, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("configexport: parse yaml: %w", err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return content, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return content, nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i].Value
		value := root.Content[i+1]
		switch key {
		case "api_keys":
			redactAPIKeysList(value)
		case "configurations":
			redactConfigurations(value)
		case "admin":
			redactAdminBlock(value)
		}
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("configexport: marshal yaml: %w", err)
	}
	return out, nil
}

// redactAPIKeysList walks a sequence of api-key mapping nodes and redacts the
// secret field on each entry. Mirrors contracts/config.APIKey.Secret.
func redactAPIKeysList(n *yaml.Node) {
	if n == nil || n.Kind != yaml.SequenceNode {
		return
	}
	for _, item := range n.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		setMappingScalar(item, "secret", RedactedPlaceholder)
	}
}

// redactConfigurations walks the configurations mapping and redacts every
// value in each configuration's upstream_credentials map. Mirrors
// contracts/config.Configuration.UpstreamCredentials.
func redactConfigurations(n *yaml.Node) {
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	for i := 1; i < len(n.Content); i += 2 {
		cfg := n.Content[i]
		if cfg.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(cfg.Content); j += 2 {
			if cfg.Content[j].Value != "upstream_credentials" {
				continue
			}
			creds := cfg.Content[j+1]
			if creds.Kind != yaml.MappingNode {
				continue
			}
			for k := 1; k < len(creds.Content); k += 2 {
				toRedactedScalar(creds.Content[k])
			}
		}
	}
}

// redactAdminBlock redacts the admin.password scalar. Mirrors
// contracts/admin.Config.Password.
func redactAdminBlock(n *yaml.Node) {
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	setMappingScalar(n, "password", RedactedPlaceholder)
}

// setMappingScalar finds the named key inside the mapping node m and replaces
// its value with a plain scalar carrying value. No-op when the key is absent —
// missing fields are not an error; the redactor only acts on what's present.
func setMappingScalar(m *yaml.Node, key, value string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1].Kind = yaml.ScalarNode
			m.Content[i+1].Tag = "!!str"
			m.Content[i+1].Style = 0
			m.Content[i+1].Value = value
			m.Content[i+1].Content = nil
			return
		}
	}
}

// toRedactedScalar mutates a scalar value node in place into the placeholder.
// Used for map-value redaction where the key node is operator data (e.g. a
// provider name) and only the value carries the secret.
func toRedactedScalar(n *yaml.Node) {
	n.Kind = yaml.ScalarNode
	n.Tag = "!!str"
	n.Style = 0
	n.Value = RedactedPlaceholder
	n.Content = nil
}
