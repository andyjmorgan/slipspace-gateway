package rules

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// TargetScope is the body a rewrite action or body-field condition
// addresses. Only request.body is honoured on the request path;
// response.body is reserved for the response-side rewrite work and
// rejected at config-load today.
type TargetScope string

// Target scopes accepted by the body-rewrite actions and bodyField
// condition.
const (
	ScopeRequestBody TargetScope = "request.body"

	ScopeResponseBody TargetScope = "response.body"
)

// Target is a parsed rewrite/condition target: a scope prefix plus the
// dotted path under it. Path is the gjson/sjson path the engine uses
// directly — segments are identifier-only, so the dotted form maps to
// gjson/sjson syntax without escaping.
type Target struct {
	// Scope is the addressed body (request.body / response.body).
	Scope TargetScope

	// Path is the dotted path under the scope, e.g.
	// "stream_options.include_usage". Empty path is invalid.
	Path string
}

var (
	scopePrefixes = []TargetScope{ScopeRequestBody, ScopeResponseBody}

	identSegment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// ParseTarget parses a strict write-side target — scope prefix plus a
// dotted path of identifier-only segments. It rejects array indices,
// wildcards, and gjson query syntax (the `#`, `*`, `?`, `@`, `[`, `]`
// characters) so operators cannot reach for JSONPath-style mutation;
// see the body-rewrite design note for why predicate writes are out.
func ParseTarget(raw string) (Target, error) {
	scope, rest, err := splitScope(raw)
	if err != nil {
		return Target{}, err
	}
	if rest == "" {
		return Target{}, fmt.Errorf("%w: %q: empty path after scope", ErrInvalidTarget, raw)
	}
	for _, seg := range strings.Split(rest, ".") {
		if !identSegment.MatchString(seg) {
			return Target{}, fmt.Errorf("%w: %q: segment %q is not a bare identifier (no indices, wildcards, or query syntax)", ErrInvalidTarget, raw, seg)
		}
	}
	return Target{Scope: scope, Path: rest}, nil
}

// ParseReadTarget parses a permissive read-side target for the bodyField
// condition. The path after the scope is handed to gjson verbatim, so
// gjson query syntax (e.g. `tools.#(name=="bash")`) is allowed — reads
// are safe, only writes are constrained.
func ParseReadTarget(raw string) (Target, error) {
	scope, rest, err := splitScope(raw)
	if err != nil {
		return Target{}, err
	}
	if rest == "" {
		return Target{}, fmt.Errorf("%w: %q: empty path after scope", ErrInvalidTarget, raw)
	}
	return Target{Scope: scope, Path: rest}, nil
}

func splitScope(raw string) (TargetScope, string, error) {
	raw = strings.TrimSpace(raw)
	for _, scope := range scopePrefixes {
		prefix := string(scope) + "."
		if strings.HasPrefix(raw, prefix) {
			return scope, raw[len(prefix):], nil
		}
	}
	return "", "", fmt.Errorf("%w: %q: must start with %q or %q", ErrInvalidTarget, raw, ScopeRequestBody+".", ScopeResponseBody+".")
}

// RewriteValueKind discriminates how a RewriteValue produces JSON at
// apply time.
type RewriteValueKind int

// RewriteValue kinds.
const (
	// RewriteValueLiteral is a typed JSON scalar (number, bool, or
	// null) emitted verbatim — the wire type follows the source.
	RewriteValueLiteral RewriteValueKind = iota

	// RewriteValueTemplate is a string that may contain `{ref}`
	// substitutions. A bare string with no references resolves to
	// itself; see the body-rewrite design note for the typing rules.
	RewriteValueTemplate

	// RewriteValueStructured is a YAML sequence or mapping emitted as
	// verbatim JSON — used to replace a field with a literal array or
	// object.
	RewriteValueStructured
)

// RewriteValue is the value carried by rewriteField / appendField. It
// distinguishes literal scalars (typed verbatim), template strings
// (string-typed, with `{ref}` substitution), and structured literals
// (arrays/objects emitted verbatim). The discrimination follows the
// wire form: YAML/JSON strings become templates, other scalars become
// literals, sequences and mappings become structured literals.
type RewriteValue struct {
	// Kind selects how Literal/Template are interpreted at apply time.
	Kind RewriteValueKind

	// Literal holds the verbatim JSON bytes for the Literal and
	// Structured kinds. Empty for the Template kind.
	Literal json.RawMessage

	// Template holds the raw string for the Template kind, including
	// any `{ref}` placeholders. Empty for the other kinds.
	Template string
}

// UnmarshalJSON decodes a RewriteValue from a JSON value: strings become
// templates, numbers/bools/null become literals, arrays/objects become
// structured literals. Literal and structured bytes are kept verbatim
// so numeric precision is preserved.
func (v *RewriteValue) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return fmt.Errorf("%w: empty value", ErrInvalidRewriteValue)
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidRewriteValue, err)
		}
		v.Kind = RewriteValueTemplate
		v.Template = s
		v.Literal = nil
	case '[', '{':
		v.Kind = RewriteValueStructured
		v.Literal = append(json.RawMessage(nil), data...)
		v.Template = ""
	default:
		v.Kind = RewriteValueLiteral
		v.Literal = append(json.RawMessage(nil), data...)
		v.Template = ""
	}
	return nil
}

// MarshalJSON re-emits the value in its wire form so rules round-trip
// through the admin console's JSON view.
func (v RewriteValue) MarshalJSON() ([]byte, error) {
	switch v.Kind {
	case RewriteValueTemplate:
		return json.Marshal(v.Template)
	case RewriteValueLiteral, RewriteValueStructured:
		if len(v.Literal) == 0 {
			return []byte("null"), nil
		}
		return v.Literal, nil
	default:
		return nil, fmt.Errorf("%w: unknown kind %d", ErrInvalidRewriteValue, v.Kind)
	}
}

// UnmarshalYAML decodes a RewriteValue from a YAML node. A `!!str`
// scalar (quoted or plain) becomes a template; other scalars become
// typed literals; sequences and mappings become structured literals.
func (v *RewriteValue) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Tag == "!!str" {
			v.Kind = RewriteValueTemplate
			v.Template = node.Value
			v.Literal = nil
			return nil
		}
		var decoded any
		if err := node.Decode(&decoded); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidRewriteValue, err)
		}
		raw, err := json.Marshal(decoded)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidRewriteValue, err)
		}
		v.Kind = RewriteValueLiteral
		v.Literal = raw
		v.Template = ""
		return nil
	case yaml.SequenceNode, yaml.MappingNode:
		var decoded any
		if err := node.Decode(&decoded); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidRewriteValue, err)
		}
		raw, err := json.Marshal(decoded)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidRewriteValue, err)
		}
		v.Kind = RewriteValueStructured
		v.Literal = raw
		v.Template = ""
		return nil
	default:
		return fmt.Errorf("%w: unsupported yaml node kind %v", ErrInvalidRewriteValue, node.Kind)
	}
}

// RewriteFieldAction sets a body field to a value, creating any missing
// intermediate objects. Emitted bytes follow the value's type — see
// RewriteValue.
type RewriteFieldAction struct {
	// Type is the polymorphic discriminator; always "rewriteField".
	Type string `yaml:"type" json:"type"`

	// Target is the dotted body path to set (request.body.*).
	Target string `yaml:"target" json:"target"`

	// Value is the value written at the target.
	Value RewriteValue `yaml:"value" json:"value"`

	models.DynamicProperties
}

// ActionType returns the "rewriteField" discriminator.
func (RewriteFieldAction) ActionType() string { return "rewriteField" }

func (RewriteFieldAction) isAction() {}

func (a *RewriteFieldAction) validate() error { return validateWriteTarget("rewriteField", a.Target) }

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (a *RewriteFieldAction) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, a)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (a RewriteFieldAction) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// RemoveFieldAction deletes a body field entirely — no key is emitted,
// distinct from rewriteField with a null value (which emits the key
// with a null value).
type RemoveFieldAction struct {
	// Type is the polymorphic discriminator; always "removeField".
	Type string `yaml:"type" json:"type"`

	// Target is the dotted body path to delete (request.body.*).
	Target string `yaml:"target" json:"target"`

	models.DynamicProperties
}

// ActionType returns the "removeField" discriminator.
func (RemoveFieldAction) ActionType() string { return "removeField" }

func (RemoveFieldAction) isAction() {}

func (a *RemoveFieldAction) validate() error { return validateWriteTarget("removeField", a.Target) }

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (a *RemoveFieldAction) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, a)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (a RemoveFieldAction) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// AppendFieldAction pushes a value onto an array body field, creating
// the array if it is absent. The target addresses the array container,
// not an element — there is no positional index syntax.
type AppendFieldAction struct {
	// Type is the polymorphic discriminator; always "appendField".
	Type string `yaml:"type" json:"type"`

	// Target is the dotted body path of the array to append to
	// (request.body.*).
	Target string `yaml:"target" json:"target"`

	// Value is the element appended to the array.
	Value RewriteValue `yaml:"value" json:"value"`

	models.DynamicProperties
}

// ActionType returns the "appendField" discriminator.
func (AppendFieldAction) ActionType() string { return "appendField" }

func (AppendFieldAction) isAction() {}

func (a *AppendFieldAction) validate() error { return validateWriteTarget("appendField", a.Target) }

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (a *AppendFieldAction) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, a)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (a AppendFieldAction) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// validateWriteTarget parses target strictly. Both request.body and
// response.body scopes are accepted for write actions — the rules
// engine partitions them by phase. (The bodyField condition still
// rejects response.body, since conditions evaluate at request time.)
func validateWriteTarget(action, raw string) error {
	if _, err := ParseTarget(raw); err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	return nil
}

// BodyFieldOperator selects how a BodyFieldCondition compares the value
// read at its target.
type BodyFieldOperator string

// Body-field operators.
const (
	// BodyFieldEquals matches when the read value equals Value (typed
	// comparison via the parsed JSON).
	BodyFieldEquals BodyFieldOperator = "Equals"

	// BodyFieldContains matches when the read string value contains
	// Value as a substring.
	BodyFieldContains BodyFieldOperator = "Contains"

	// BodyFieldMatches matches when the read string value matches the
	// Value regular expression (RE2 — linear time, no backtracking).
	BodyFieldMatches BodyFieldOperator = "Matches"

	// BodyFieldExists matches when the target resolves to a present
	// value. Value is ignored.
	BodyFieldExists BodyFieldOperator = "Exists"

	// BodyFieldIsType matches when the read value is of the JSON type
	// named by Value (string, number, bool, array, object, null).
	BodyFieldIsType BodyFieldOperator = "Is"
)

// BodyFieldCondition matches against a value read from the request body
// by gjson. The target supports gjson query syntax (e.g.
// `request.body.tools.#(name=="bash")`) because reads are unconstrained
// — only writes are limited to bare paths.
type BodyFieldCondition struct {
	// Type is the polymorphic discriminator; always "bodyField".
	Type string `yaml:"type" json:"type"`

	// Target is the body path to read (request.body.*), gjson syntax.
	Target string `yaml:"target" json:"target"`

	// Operator selects the comparison strategy.
	Operator BodyFieldOperator `yaml:"operator" json:"operator"`

	// Value is the comparison operand. Interpretation depends on
	// Operator; ignored for Exists.
	Value string `yaml:"value,omitempty" json:"value,omitempty"`

	// Not inverts the match result.
	Not bool `yaml:"not,omitempty" json:"not,omitempty"`

	models.DynamicProperties
}

// ConditionType returns the "bodyField" discriminator.
func (BodyFieldCondition) ConditionType() string { return "bodyField" }

func (BodyFieldCondition) isCondition() {}

func (c *BodyFieldCondition) validate() error {
	t, err := ParseReadTarget(c.Target)
	if err != nil {
		return fmt.Errorf("bodyField: %w", err)
	}
	if t.Scope == ScopeResponseBody {
		return fmt.Errorf("bodyField: %w", ErrResponseScopeUnsupported)
	}
	switch c.Operator {
	case BodyFieldEquals, BodyFieldContains, BodyFieldMatches, BodyFieldExists, BodyFieldIsType:
	default:
		return fmt.Errorf("bodyField: %w: %q", ErrUnknownBodyFieldOperator, c.Operator)
	}
	if c.Operator == BodyFieldMatches {
		if _, err := regexp.Compile(c.Value); err != nil {
			return fmt.Errorf("bodyField: %w: %w", ErrInvalidBodyFieldRegex, err)
		}
	}
	return nil
}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (c *BodyFieldCondition) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, c)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (c BodyFieldCondition) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(c) }
