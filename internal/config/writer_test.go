package config

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// failingMarshaler drives Encode down its error-returning path. A value yaml
// cannot represent at all (a func, say) panics instead, so a MarshalYAML that
// returns an error is the way to exercise the branch appendBlock now
// propagates.
type failingMarshaler struct{}

func (failingMarshaler) MarshalYAML() (any, error) {
	return nil, errors.New("marshal refused")
}

// TestAppendBlock_ReturnsEncodeError is the point of the change: an encode
// failure must surface instead of leaving a zero-valued node under the key,
// which would persist the file with that whole top-level block emptied.
func TestAppendBlock_ReturnsEncodeError(t *testing.T) {
	root := &yaml.Node{Kind: yaml.MappingNode}

	err := appendBlock(root, "rules", failingMarshaler{})
	if err == nil {
		t.Fatal("appendBlock returned nil for an unencodable value")
	}
	if !strings.Contains(err.Error(), "rules") {
		t.Errorf("error should name the failing block, got %v", err)
	}
	if len(root.Content) != 0 {
		t.Errorf("failed encode still appended %d node(s); the key must not be "+
			"attached when its value could not be built", len(root.Content))
	}
}

// TestAppendBlock_SuccessAppendsKeyAndValue pins the happy path so the added
// error return did not change what a successful call produces.
func TestAppendBlock_SuccessAppendsKeyAndValue(t *testing.T) {
	root := &yaml.Node{Kind: yaml.MappingNode}
	if err := appendBlock(root, "tags", map[string]string{"tier": "prod"}); err != nil {
		t.Fatalf("appendBlock: %v", err)
	}
	if len(root.Content) != 2 {
		t.Fatalf("len(root.Content) = %d, want 2 (key + value)", len(root.Content))
	}
	if root.Content[0].Value != "tags" {
		t.Errorf("key node = %q, want tags", root.Content[0].Value)
	}
	out, err := yaml.Marshal(root)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "tier: prod") {
		t.Errorf("value not encoded into the document: %s", out)
	}
}

// TestAppendResolvedBlock_KnownBlocksSucceed walks every block the writer
// serialises, so a newly added block that cannot encode fails here rather
// than silently truncating a config file in production.
func TestAppendResolvedBlock_KnownBlocksSucceed(t *testing.T) {
	r := validBase()
	blocks := []string{
		keyProviders, keyGroups, keyConfigurations, keyAPIKeys,
		keyRules, keyConnectors, keyAdmin, keyTelemetry,
		keyPricing, keyAdvisors,
	}
	for _, b := range blocks {
		t.Run(b, func(t *testing.T) {
			root := &yaml.Node{Kind: yaml.MappingNode}
			if err := appendResolvedBlock(root, b, r); err != nil {
				t.Fatalf("appendResolvedBlock(%q) = %v, want nil", b, err)
			}
		})
	}
}

// TestAppendResolvedBlock_UnknownBlockIsNoOp confirms the default arm still
// returns nil rather than falling through to an error now that the function
// has an error return.
func TestAppendResolvedBlock_UnknownBlockIsNoOp(t *testing.T) {
	root := &yaml.Node{Kind: yaml.MappingNode}
	if err := appendResolvedBlock(root, "not_a_block", validBase()); err != nil {
		t.Fatalf("unknown block = %v, want nil", err)
	}
	if len(root.Content) != 0 {
		t.Errorf("unknown block appended %d node(s), want 0", len(root.Content))
	}
}
