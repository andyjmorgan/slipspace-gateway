package resilience

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

// targetWire mirrors ResilienceTarget for JSON decode, holding the
// scalar fields verbatim and capturing Actions as raw JSON so they
// can be dispatched through the rules registry one at a time.
type targetWire struct {
	Name string `json:"name"`

	Provider string `json:"provider"`

	Order int `json:"order,omitempty"`

	Weight int `json:"weight,omitempty"`

	TimeoutSeconds int `json:"timeout_seconds,omitempty"`

	ModelRewrite string `json:"model_rewrite,omitempty"`

	FailureStatusCodes []int `json:"failure_status_codes,omitempty"`

	CircuitBreaker *CircuitBreakerConfig `json:"circuit_breaker,omitempty"`

	Actions []json.RawMessage `json:"actions,omitempty"`
}

// UnmarshalJSON decodes a ResilienceTarget, dispatching Actions through
// rules.UnmarshalAction so the polymorphic Action interface round-trips
// through the standard json.Decoder. Scalar fields decode straight off
// the wire shape.
func (t *ResilienceTarget) UnmarshalJSON(data []byte) error {
	var w targetWire
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("resilience: target: %w", err)
	}
	t.Name = w.Name
	t.Provider = w.Provider
	t.Order = w.Order
	t.Weight = w.Weight
	t.TimeoutSeconds = w.TimeoutSeconds
	t.ModelRewrite = w.ModelRewrite
	t.FailureStatusCodes = w.FailureStatusCodes
	t.CircuitBreaker = w.CircuitBreaker

	if len(w.Actions) > 0 {
		t.Actions = make([]rules.Action, len(w.Actions))
		for i, raw := range w.Actions {
			act, err := rules.UnmarshalAction(raw)
			if err != nil {
				return fmt.Errorf("resilience: target %q: actions[%d]: %w", t.Name, i, err)
			}
			t.Actions[i] = act
		}
	}
	return nil
}

// UnmarshalYAML walks the YAML mapping for a ResilienceTarget, handling
// the polymorphic Actions sequence node-by-node via the rules package's
// exported per-node decoder. Scalar fields are decoded by mirroring
// them into a struct alias so the yaml library handles the leaf
// values.
func (t *ResilienceTarget) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("resilience: target: expected mapping, got %v", value.Kind)
	}

	type scalarAlias struct {
		Name               string                `yaml:"name"`
		Provider           string                `yaml:"provider"`
		Order              int                   `yaml:"order,omitempty"`
		Weight             int                   `yaml:"weight,omitempty"`
		TimeoutSeconds     int                   `yaml:"timeout_seconds,omitempty"`
		ModelRewrite       string                `yaml:"model_rewrite,omitempty"`
		FailureStatusCodes []int                 `yaml:"failure_status_codes,omitempty"`
		CircuitBreaker     *CircuitBreakerConfig `yaml:"circuit_breaker,omitempty"`
	}
	var s scalarAlias
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf("resilience: target: %w", err)
	}
	t.Name = s.Name
	t.Provider = s.Provider
	t.Order = s.Order
	t.Weight = s.Weight
	t.TimeoutSeconds = s.TimeoutSeconds
	t.ModelRewrite = s.ModelRewrite
	t.FailureStatusCodes = s.FailureStatusCodes
	t.CircuitBreaker = s.CircuitBreaker

	for i := 0; i < len(value.Content); i += 2 {
		keyNode := value.Content[i]
		valNode := value.Content[i+1]
		if keyNode.Value != "actions" {
			continue
		}
		if valNode.Kind != yaml.SequenceNode {
			return fmt.Errorf("resilience: target %q: actions: expected sequence, got %v", t.Name, valNode.Kind)
		}
		t.Actions = make([]rules.Action, len(valNode.Content))
		for j, n := range valNode.Content {
			act, err := rules.DecodeActionYAMLNode(n)
			if err != nil {
				return fmt.Errorf("resilience: target %q: actions[%d]: %w", t.Name, j, err)
			}
			t.Actions[j] = act
		}
	}
	return nil
}
