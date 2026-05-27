package rules

import (
	"encoding/json"
	"errors"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseTarget(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantScope TargetScope
		wantPath  string
		wantErr   bool
	}{
		{name: "request body single", raw: "request.body.user", wantScope: ScopeRequestBody, wantPath: "user"},
		{name: "request body nested", raw: "request.body.stream_options.include_usage", wantScope: ScopeRequestBody, wantPath: "stream_options.include_usage"},
		{name: "response body", raw: "response.body.results_url", wantScope: ScopeResponseBody, wantPath: "results_url"},
		{name: "leading underscore segment", raw: "request.body._x", wantScope: ScopeRequestBody, wantPath: "_x"},
		{name: "missing scope", raw: "foo.bar", wantErr: true},
		{name: "empty path", raw: "request.body.", wantErr: true},
		{name: "array index rejected", raw: "request.body.messages.0", wantErr: true},
		{name: "bracket rejected", raw: "request.body.messages[0]", wantErr: true},
		{name: "gjson predicate rejected", raw: `request.body.tools.#(name=="x")`, wantErr: true},
		{name: "wildcard rejected", raw: "request.body.*", wantErr: true},
		{name: "segment starting with digit rejected", raw: "request.body.1abc", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTarget(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.raw)
				}
				if !errors.Is(err, ErrInvalidTarget) {
					t.Errorf("error = %v, want ErrInvalidTarget", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Scope != tt.wantScope || got.Path != tt.wantPath {
				t.Errorf("got {%q %q}, want {%q %q}", got.Scope, got.Path, tt.wantScope, tt.wantPath)
			}
		})
	}
}

func TestParseReadTarget(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantPath string
		wantErr  bool
	}{
		{name: "plain path", raw: "request.body.stream", wantPath: "stream"},
		{name: "gjson predicate allowed", raw: `request.body.tools.#(name=="bash")`, wantPath: `tools.#(name=="bash")`},
		{name: "missing scope", raw: "stream", wantErr: true},
		{name: "empty path", raw: "request.body.", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseReadTarget(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Path != tt.wantPath {
				t.Errorf("path = %q, want %q", got.Path, tt.wantPath)
			}
		})
	}
}

func TestRewriteValue_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantKind RewriteValueKind
		wantLit  string
		wantTmpl string
		wantErr  bool
	}{
		{name: "string is template", in: `"hello"`, wantKind: RewriteValueTemplate, wantTmpl: "hello"},
		{name: "template with ref", in: `"{request.body.x}"`, wantKind: RewriteValueTemplate, wantTmpl: "{request.body.x}"},
		{name: "number literal", in: `1024`, wantKind: RewriteValueLiteral, wantLit: "1024"},
		{name: "float literal", in: `0.7`, wantKind: RewriteValueLiteral, wantLit: "0.7"},
		{name: "bool literal", in: `true`, wantKind: RewriteValueLiteral, wantLit: "true"},
		{name: "null literal", in: `null`, wantKind: RewriteValueLiteral, wantLit: "null"},
		{name: "array structured", in: `[1,2]`, wantKind: RewriteValueStructured, wantLit: "[1,2]"},
		{name: "object structured", in: `{"a":1}`, wantKind: RewriteValueStructured, wantLit: `{"a":1}`},
		{name: "empty errors", in: `   `, wantErr: true},
		{name: "malformed string errors", in: `"\x"`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v RewriteValue
			err := v.UnmarshalJSON([]byte(tt.in))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.Kind != tt.wantKind {
				t.Errorf("kind = %d, want %d", v.Kind, tt.wantKind)
			}
			if tt.wantTmpl != "" && v.Template != tt.wantTmpl {
				t.Errorf("template = %q, want %q", v.Template, tt.wantTmpl)
			}
			if tt.wantLit != "" && string(v.Literal) != tt.wantLit {
				t.Errorf("literal = %q, want %q", v.Literal, tt.wantLit)
			}
		})
	}
}

func TestRewriteValue_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantKind RewriteValueKind
		wantLit  string
		wantTmpl string
	}{
		{name: "plain string template", in: `hello`, wantKind: RewriteValueTemplate, wantTmpl: "hello"},
		{name: "quoted string template", in: `"1024"`, wantKind: RewriteValueTemplate, wantTmpl: "1024"},
		{name: "int literal", in: `1024`, wantKind: RewriteValueLiteral, wantLit: "1024"},
		{name: "bool literal", in: `true`, wantKind: RewriteValueLiteral, wantLit: "true"},
		// yaml.v3 does not invoke a custom unmarshaler for a bare null
		// node, so RewriteValue stays zero-valued (Kind=Literal, nil
		// Literal). resolveValue emits JSON null for an empty literal,
		// so `value: null` still produces null on the wire.
		{name: "null literal", in: `null`, wantKind: RewriteValueLiteral, wantLit: ""},
		{name: "sequence structured", in: "- a\n- b", wantKind: RewriteValueStructured, wantLit: `["a","b"]`},
		{name: "mapping structured", in: "k: v", wantKind: RewriteValueStructured, wantLit: `{"k":"v"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v RewriteValue
			if err := yaml.Unmarshal([]byte(tt.in), &v); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if v.Kind != tt.wantKind {
				t.Errorf("kind = %d, want %d", v.Kind, tt.wantKind)
			}
			if tt.wantTmpl != "" && v.Template != tt.wantTmpl {
				t.Errorf("template = %q, want %q", v.Template, tt.wantTmpl)
			}
			if tt.wantLit != "" && string(v.Literal) != tt.wantLit {
				t.Errorf("literal = %q, want %q", v.Literal, tt.wantLit)
			}
		})
	}
}

func TestRewriteValue_UnmarshalYAML_UnsupportedNode(t *testing.T) {
	// An alias node reaches the decoder as an unresolved kind the value
	// decoder does not handle, exercising the default branch.
	var v RewriteValue
	err := v.UnmarshalYAML(&yaml.Node{Kind: yaml.AliasNode})
	if err == nil {
		t.Fatal("expected error for unsupported node kind")
	}
	if !errors.Is(err, ErrInvalidRewriteValue) {
		t.Errorf("error = %v, want ErrInvalidRewriteValue", err)
	}
}

func TestRewriteValue_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		v       RewriteValue
		want    string
		wantErr bool
	}{
		{name: "template", v: RewriteValue{Kind: RewriteValueTemplate, Template: "hi"}, want: `"hi"`},
		{name: "literal", v: RewriteValue{Kind: RewriteValueLiteral, Literal: json.RawMessage("1024")}, want: "1024"},
		{name: "structured", v: RewriteValue{Kind: RewriteValueStructured, Literal: json.RawMessage(`[1]`)}, want: "[1]"},
		{name: "empty literal becomes null", v: RewriteValue{Kind: RewriteValueLiteral}, want: "null"},
		{name: "unknown kind errors", v: RewriteValue{Kind: 99}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.v.MarshalJSON()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRewriteActions_Validate(t *testing.T) {
	tests := []struct {
		name    string
		action  Action
		wantErr error
	}{
		{name: "rewriteField valid", action: &RewriteFieldAction{Target: "request.body.temperature"}},
		{name: "rewriteField response scope", action: &RewriteFieldAction{Target: "response.body.x"}, wantErr: ErrResponseScopeUnsupported},
		{name: "rewriteField bad target", action: &RewriteFieldAction{Target: "nope"}, wantErr: ErrInvalidTarget},
		{name: "removeField valid", action: &RemoveFieldAction{Target: "request.body.user"}},
		{name: "removeField response scope", action: &RemoveFieldAction{Target: "response.body.x"}, wantErr: ErrResponseScopeUnsupported},
		{name: "appendField valid", action: &AppendFieldAction{Target: "request.body.messages"}},
		{name: "appendField bad target", action: &AppendFieldAction{Target: "request.body.a[0]"}, wantErr: ErrInvalidTarget},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, ok := tt.action.(interface{ validate() error })
			if !ok {
				t.Fatal("action does not implement validate()")
			}
			err := v.validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestBodyFieldCondition_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cond    BodyFieldCondition
		wantErr error
	}{
		{name: "equals valid", cond: BodyFieldCondition{Target: "request.body.stream", Operator: BodyFieldEquals, Value: "true"}},
		{name: "exists valid", cond: BodyFieldCondition{Target: "request.body.tools", Operator: BodyFieldExists}},
		{name: "matches valid", cond: BodyFieldCondition{Target: "request.body.system", Operator: BodyFieldMatches, Value: "Claude.*"}},
		{name: "is valid", cond: BodyFieldCondition{Target: "request.body.system", Operator: BodyFieldIsType, Value: "array"}},
		{name: "predicate target valid", cond: BodyFieldCondition{Target: `request.body.tools.#(name=="bash")`, Operator: BodyFieldExists}},
		{name: "bad target", cond: BodyFieldCondition{Target: "stream", Operator: BodyFieldExists}, wantErr: ErrInvalidTarget},
		{name: "response scope", cond: BodyFieldCondition{Target: "response.body.x", Operator: BodyFieldExists}, wantErr: ErrResponseScopeUnsupported},
		{name: "unknown operator", cond: BodyFieldCondition{Target: "request.body.x", Operator: "Nope"}, wantErr: ErrUnknownBodyFieldOperator},
		{name: "bad regex", cond: BodyFieldCondition{Target: "request.body.x", Operator: BodyFieldMatches, Value: "("}, wantErr: ErrInvalidBodyFieldRegex},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cond.validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestRewriteRule_RoundTrip_YAML(t *testing.T) {
	const doc = `
name: body-rewrite-rule
condition:
  type: bodyField
  target: request.body.stream
  operator: Equals
  value: "true"
actions:
  - type: rewriteField
    target: request.body.stream_options.include_usage
    value: true
  - type: removeField
    target: request.body.user
  - type: appendField
    target: request.body.messages
    value:
      role: developer
      content: "be good"
`
	var r RuleContract
	if err := yaml.Unmarshal([]byte(doc), &r); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(r.Actions) != 3 {
		t.Fatalf("want 3 actions, got %d", len(r.Actions))
	}
	if _, ok := r.Condition.(*BodyFieldCondition); !ok {
		t.Errorf("condition type = %T, want *BodyFieldCondition", r.Condition)
	}
	rw, ok := r.Actions[0].(*RewriteFieldAction)
	if !ok {
		t.Fatalf("action[0] type = %T", r.Actions[0])
	}
	if rw.Value.Kind != RewriteValueLiteral || string(rw.Value.Literal) != "true" {
		t.Errorf("rewriteField value = %+v", rw.Value)
	}
	ap, ok := r.Actions[2].(*AppendFieldAction)
	if !ok {
		t.Fatalf("action[2] type = %T", r.Actions[2])
	}
	if ap.Value.Kind != RewriteValueStructured {
		t.Errorf("appendField value kind = %d, want structured", ap.Value.Kind)
	}

	// JSON round-trip through the marshaller.
	out, err := json.Marshal(r.Actions[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !json.Valid(out) {
		t.Errorf("marshalled action is not valid JSON: %s", out)
	}
}

func TestRewriteRule_RoundTrip_JSON(t *testing.T) {
	const doc = `{
		"name": "r",
		"condition": {"type": "bodyField", "target": "request.body.stream", "operator": "Equals", "value": "true"},
		"actions": [
			{"type": "rewriteField", "target": "request.body.temperature", "value": 0},
			{"type": "removeField", "target": "request.body.user"},
			{"type": "appendField", "target": "request.body.system", "value": {"type": "text", "text": "hi"}}
		]
	}`
	var r RuleContract
	if err := json.Unmarshal([]byte(doc), &r); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	rw := r.Actions[0].(*RewriteFieldAction)
	if rw.Value.Kind != RewriteValueLiteral || string(rw.Value.Literal) != "0" {
		t.Errorf("value = %+v", rw.Value)
	}
}

func TestRewriteRule_Validate_NestedBodyFieldInGroup(t *testing.T) {
	r := RuleContract{
		Name: "g",
		Condition: &RuleGroup{
			Type:            "group",
			LogicalOperator: LogicalAnd,
			Children: []Condition{
				&BodyFieldCondition{Target: "bad-target", Operator: BodyFieldExists},
			},
		},
		Actions: []Action{&RemoveFieldAction{Target: "request.body.user"}},
	}
	if err := r.Validate(); !errors.Is(err, ErrInvalidTarget) {
		t.Errorf("expected ErrInvalidTarget from nested bodyField, got %v", err)
	}
}
