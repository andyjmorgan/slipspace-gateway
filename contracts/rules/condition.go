package rules

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// Condition is the polymorphic interface implemented by every concrete
// condition type plus the UnknownCondition fallback. ConditionType returns the
// "type" discriminator carried on the wire.
type Condition interface {
	ConditionType() string

	isCondition()
}

// ProviderCondition matches when the resolved provider equals ExpectedProvider.
type ProviderCondition struct {
	// Type is the polymorphic discriminator; always "provider".
	Type string `yaml:"type" json:"type"`

	// Operator is the comparison strategy. Only EnumEquals is meaningful.
	Operator EnumOperator `yaml:"operator" json:"operator"`

	// ExpectedProvider is the provider name the condition compares against.
	ExpectedProvider string `yaml:"expectedProvider" json:"expected_provider"`

	// Not inverts the match result.
	Not bool `yaml:"not,omitempty" json:"not,omitempty"`

	models.DynamicProperties
}

// ConditionType returns the "provider" discriminator.
func (ProviderCondition) ConditionType() string { return "provider" }

func (ProviderCondition) isCondition() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *ProviderCondition) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, c)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c ProviderCondition) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// EndpointCondition matches when the resolved endpoint equals ExpectedEndpoint.
type EndpointCondition struct {
	// Type is the polymorphic discriminator; always "endpoint".
	Type string `yaml:"type" json:"type"`

	// Operator is the comparison strategy. Only EnumEquals is meaningful.
	Operator EnumOperator `yaml:"operator" json:"operator"`

	// ExpectedEndpoint is the endpoint identifier the condition compares
	// against (e.g., "openai.chat_completions").
	ExpectedEndpoint string `yaml:"expectedEndpoint" json:"expected_endpoint"`

	// Not inverts the match result.
	Not bool `yaml:"not,omitempty" json:"not,omitempty"`

	models.DynamicProperties
}

// ConditionType returns the "endpoint" discriminator.
func (EndpointCondition) ConditionType() string { return "endpoint" }

func (EndpointCondition) isCondition() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *EndpointCondition) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, c)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c EndpointCondition) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// ModelNameCondition matches the resolved model name with a string operator.
type ModelNameCondition struct {
	// Type is the polymorphic discriminator; always "modelName".
	Type string `yaml:"type" json:"type"`

	// Operator selects the string comparison strategy.
	Operator StringOperator `yaml:"operator" json:"operator"`

	// ExpectedModelName is the literal, prefix, suffix, substring, or regex
	// the model name is compared against — Operator decides which.
	ExpectedModelName string `yaml:"expectedModelName" json:"expected_model_name"`

	// CaseInsensitive folds case before comparing.
	CaseInsensitive bool `yaml:"caseInsensitive,omitempty" json:"case_insensitive,omitempty"`

	// Not inverts the match result.
	Not bool `yaml:"not,omitempty" json:"not,omitempty"`

	models.DynamicProperties
}

// ConditionType returns the "modelName" discriminator.
func (ModelNameCondition) ConditionType() string { return "modelName" }

func (ModelNameCondition) isCondition() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *ModelNameCondition) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, c)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c ModelNameCondition) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// HeaderCondition matches inbound HTTP headers. KeyOperator always applies; if
// ValueOperator is set the value must also match.
type HeaderCondition struct {
	// Type is the polymorphic discriminator; always "header".
	Type string `yaml:"type" json:"type"`

	// KeyOperator selects how to match header names against KeyPattern.
	KeyOperator StringOperator `yaml:"keyOperator" json:"key_operator"`

	// KeyPattern is the header-name pattern; interpretation depends on
	// KeyOperator.
	KeyPattern string `yaml:"keyPattern" json:"key_pattern"`

	// ValueOperator selects how to match header values against ValuePattern.
	// When nil, only KeyOperator applies and any value matches.
	ValueOperator *StringOperator `yaml:"valueOperator,omitempty" json:"value_operator,omitempty"`

	// ValuePattern is the header-value pattern; interpretation depends on
	// ValueOperator.
	ValuePattern string `yaml:"valuePattern,omitempty" json:"value_pattern,omitempty"`

	// CaseInsensitive folds case on both key and value comparisons.
	CaseInsensitive bool `yaml:"caseInsensitive,omitempty" json:"case_insensitive,omitempty"`

	// Not inverts the match result.
	Not bool `yaml:"not,omitempty" json:"not,omitempty"`

	models.DynamicProperties
}

// ConditionType returns the "header" discriminator.
func (HeaderCondition) ConditionType() string { return "header" }

func (HeaderCondition) isCondition() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *HeaderCondition) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, c)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c HeaderCondition) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// TagCondition matches when the request has been tagged by an earlier
// rule's AddTagAction. Tags are set-valued, so the only meaningful
// operator today is EnumEquals — "request set contains ExpectedTag".
// For "any of {a, b, c}", compose three TagConditions under a
// RuleGroup with LogicalOr; for "all of", LogicalAnd.
//
// Rule order matters: this condition only sees tags attached by rules
// that ran earlier in the configuration's RuleNames list.
type TagCondition struct {
	// Type is the polymorphic discriminator; always "tag".
	Type string `yaml:"type" json:"type"`

	// Operator is the comparison strategy. Only EnumEquals is
	// meaningful (set-membership check) — other values evaluate to
	// false rather than error, matching the engine's forward-compat
	// pattern.
	Operator EnumOperator `yaml:"operator" json:"operator"`

	// ExpectedTag is the tag string the condition checks for.
	ExpectedTag string `yaml:"expectedTag" json:"expected_tag"`

	// Not inverts the match result.
	Not bool `yaml:"not,omitempty" json:"not,omitempty"`

	models.DynamicProperties
}

// ConditionType returns the "tag" discriminator.
func (TagCondition) ConditionType() string { return "tag" }

func (TagCondition) isCondition() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *TagCondition) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, c)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c TagCondition) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

// RuleGroup composes Children with LogicalAnd or LogicalOr. Children may be
// any Condition type, including nested RuleGroups, allowing arbitrarily-deep
// logic trees.
type RuleGroup struct {
	// Type is the polymorphic discriminator; always "group".
	Type string `yaml:"type" json:"type"`

	// LogicalOperator combines the Children results.
	LogicalOperator LogicalOperator `yaml:"logicalOperator" json:"logical_operator"`

	// Children are the sub-conditions; may be any Condition type including
	// nested RuleGroups.
	Children []Condition `yaml:"children" json:"children"`

	// Not inverts the group's result after the logical combination.
	Not bool `yaml:"not,omitempty" json:"not,omitempty"`

	models.DynamicProperties
}

// ConditionType returns the "group" discriminator.
func (RuleGroup) ConditionType() string { return "group" }

func (RuleGroup) isCondition() {}

// ruleGroupWire mirrors RuleGroup with Children held as raw JSON so the
// polymorphic Condition registry can dispatch each child by its "type".
type ruleGroupWire struct {
	Type string `json:"type"`

	LogicalOperator LogicalOperator `json:"logical_operator"`

	Children []json.RawMessage `json:"children"`

	Not bool `json:"not,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON walks Children through UnmarshalCondition while routing any
// unknown sibling fields into DynamicProperties.Extra.
func (g *RuleGroup) UnmarshalJSON(data []byte) error {
	type alias struct {
		Type            string            `json:"type"`
		LogicalOperator LogicalOperator   `json:"logical_operator"`
		Children        []json.RawMessage `json:"children"`
		Not             bool              `json:"not,omitempty"`
	}
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return fmt.Errorf("rules: group: %w", err)
	}
	g.Type = a.Type
	g.LogicalOperator = a.LogicalOperator
	g.Not = a.Not
	g.Children = make([]Condition, len(a.Children))
	for i, c := range a.Children {
		cond, err := UnmarshalCondition(c)
		if err != nil {
			return fmt.Errorf("rules: group: children[%d]: %w", i, err)
		}
		g.Children[i] = cond
	}

	raw := make(map[string]json.RawMessage)
	_ = json.Unmarshal(data, &raw)
	delete(raw, "type")
	delete(raw, "logical_operator")
	delete(raw, "children")
	delete(raw, "not")
	if len(raw) > 0 {
		g.DynamicProperties = models.DynamicProperties{Extra: raw}
	} else {
		g.DynamicProperties = models.DynamicProperties{}
	}
	return nil
}

// MarshalJSON emits the group with its Children re-serialised through the
// Condition interface so each child is dispatched to its own MarshalJSON.
func (g RuleGroup) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(g.Extra)+4)
	for k, v := range g.Extra {
		out[k] = v
	}
	out["type"] = g.Type
	out["logical_operator"] = g.LogicalOperator
	if g.Not {
		out["not"] = g.Not
	}
	out["children"] = g.Children
	return json.Marshal(out)
}

// UnmarshalYAML decodes a RuleGroup, dispatching each child through the
// condition factory by inspecting its YAML "type" field.
func (g *RuleGroup) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("rules: group: expected mapping, got %v", value.Kind)
	}
	type alias struct {
		Type            string          `yaml:"type"`
		LogicalOperator LogicalOperator `yaml:"logicalOperator"`
		Not             bool            `yaml:"not,omitempty"`
	}
	var a alias
	if err := value.Decode(&a); err != nil {
		return fmt.Errorf("rules: group: %w", err)
	}
	g.Type = a.Type
	g.LogicalOperator = a.LogicalOperator
	g.Not = a.Not

	for i := 0; i < len(value.Content); i += 2 {
		if value.Content[i].Value != "children" {
			continue
		}
		valNode := value.Content[i+1]
		if valNode.Kind != yaml.SequenceNode {
			return fmt.Errorf("rules: group: children: expected sequence, got %v", valNode.Kind)
		}
		g.Children = make([]Condition, len(valNode.Content))
		for j, child := range valNode.Content {
			cond, err := decodeConditionNode(child)
			if err != nil {
				return fmt.Errorf("rules: group: children[%d]: %w", j, err)
			}
			g.Children[j] = cond
		}
	}
	return nil
}

// UnknownCondition is the catch-all fallback for any discriminator the
// registry does not recognise. Type carries the unknown discriminator value
// and every other JSON field lands in DynamicProperties.Extra so the
// condition round-trips intact.
type UnknownCondition struct {
	// Type holds the unknown discriminator value verbatim so it can be
	// re-emitted on marshal.
	Type string `yaml:"type" json:"type"`

	models.DynamicProperties
}

// ConditionType returns the unknown discriminator value verbatim.
func (c UnknownCondition) ConditionType() string { return c.Type }

func (UnknownCondition) isCondition() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *UnknownCondition) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, c)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c UnknownCondition) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }

var conditionRegistry = models.PolymorphicRegistry[Condition]{
	DiscriminatorField: "type",
	Factories: map[string]func() Condition{
		"provider":  func() Condition { return &ProviderCondition{} },
		"endpoint":  func() Condition { return &EndpointCondition{} },
		"modelName": func() Condition { return &ModelNameCondition{} },
		"header":    func() Condition { return &HeaderCondition{} },
		"tag":       func() Condition { return &TagCondition{} },
		"group":     func() Condition { return &RuleGroup{} },
	},
	Fallback: func(disc string) Condition { return &UnknownCondition{Type: disc} },
}

// UnmarshalCondition decodes a single Condition, dispatching on the "type"
// discriminator and falling back to UnknownCondition for any unrecognised
// value so the payload round-trips intact.
func UnmarshalCondition(data []byte) (Condition, error) {
	return conditionRegistry.UnmarshalOne(data)
}

// decodeConditionNode dispatches a YAML node to the concrete condition type
// by inspecting its "type" key. Unknown discriminators fall back to
// UnknownCondition for parity with the JSON path.
func decodeConditionNode(node *yaml.Node) (Condition, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("rules: condition: expected mapping, got %v", node.Kind)
	}
	disc, err := peekYAMLDiscriminator(node, "type")
	if err != nil {
		return nil, err
	}
	factory, ok := conditionRegistry.Factories[disc]
	if !ok {
		uc := &UnknownCondition{Type: disc}
		if err := decodeUnknownYAMLNode(node, &uc.DynamicProperties); err != nil {
			return nil, err
		}
		return uc, nil
	}
	cond := factory()
	if err := node.Decode(cond); err != nil {
		return nil, fmt.Errorf("rules: condition %q: %w", disc, err)
	}
	return cond, nil
}

func peekYAMLDiscriminator(node *yaml.Node, field string) (string, error) {
	for i := 0; i < len(node.Content); i += 2 {
		k := node.Content[i]
		v := node.Content[i+1]
		if k.Value == field {
			if v.Kind != yaml.ScalarNode {
				return "", fmt.Errorf("rules: discriminator %q must be a scalar", field)
			}
			return v.Value, nil
		}
	}
	return "", fmt.Errorf("rules: discriminator %q: %w", field, models.ErrMissingDiscriminator)
}

// decodeUnknownYAMLNode stashes every non-"type" field from a YAML mapping
// node into DynamicProperties.Extra as JSON bytes. We round-trip via JSON so
// the same Extra map serves both wire formats and re-marshalling does not
// lose fields.
func decodeUnknownYAMLNode(node *yaml.Node, dp *models.DynamicProperties) error {
	if dp.Extra == nil {
		dp.Extra = make(map[string]json.RawMessage)
	}
	for i := 0; i < len(node.Content); i += 2 {
		k := node.Content[i]
		v := node.Content[i+1]
		if k.Value == "type" {
			continue
		}
		var any interface{}
		if err := v.Decode(&any); err != nil {
			return fmt.Errorf("rules: unknown field %q: %w", k.Value, err)
		}
		raw, err := json.Marshal(any)
		if err != nil {
			return fmt.Errorf("rules: unknown field %q: %w", k.Value, err)
		}
		dp.Extra[k.Value] = raw
	}
	return nil
}
