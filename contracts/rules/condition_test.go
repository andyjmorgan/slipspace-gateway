package rules_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/contracts/rules"
)

func TestUnmarshalCondition_Provider(t *testing.T) {
	raw := `{"type":"provider","operator":"Equals","expected_provider":"openai"}`
	cond, err := rules.UnmarshalCondition([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	pc, ok := cond.(*rules.ProviderCondition)
	if !ok {
		t.Fatalf("type = %T", cond)
	}
	if pc.ExpectedProvider != "openai" || pc.Operator != rules.EnumEquals {
		t.Errorf("bad fields: %+v", pc)
	}
	if pc.ConditionType() != "provider" {
		t.Errorf("ConditionType: %q", pc.ConditionType())
	}
}

func TestUnmarshalCondition_Endpoint(t *testing.T) {
	raw := `{"type":"protocol","operator":"Equals","expected_protocol":"chat_completions","not":true}`
	cond, err := rules.UnmarshalCondition([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ec, ok := cond.(*rules.ProtocolCondition)
	if !ok {
		t.Fatalf("type = %T", cond)
	}
	if ec.ExpectedProtocol != "chat_completions" || !ec.Not {
		t.Errorf("bad fields: %+v", ec)
	}
	if ec.ConditionType() != "protocol" {
		t.Errorf("ConditionType: %q", ec.ConditionType())
	}
}

func TestUnmarshalCondition_ModelName(t *testing.T) {
	raw := `{"type":"modelName","operator":"StartsWith","expected_model_name":"gpt-4","case_insensitive":true}`
	cond, err := rules.UnmarshalCondition([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mc, ok := cond.(*rules.ModelNameCondition)
	if !ok {
		t.Fatalf("type = %T", cond)
	}
	if mc.ExpectedModelName != "gpt-4" || mc.Operator != rules.StringStartsWith || !mc.CaseInsensitive {
		t.Errorf("bad fields: %+v", mc)
	}
}

func TestUnmarshalCondition_Header(t *testing.T) {
	raw := `{"type":"header","key_operator":"Equals","key_pattern":"X-Tenant","value_operator":"Regex","value_pattern":"^acme-"}`
	cond, err := rules.UnmarshalCondition([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	hc, ok := cond.(*rules.HeaderCondition)
	if !ok {
		t.Fatalf("type = %T", cond)
	}
	if hc.KeyPattern != "X-Tenant" || hc.ValueOperator == nil || *hc.ValueOperator != rules.StringRegex {
		t.Errorf("bad fields: %+v", hc)
	}
}

func TestUnmarshalCondition_RuleGroup_Nested(t *testing.T) {
	raw := `{
        "type":"group",
        "logical_operator":"And",
        "children":[
            {"type":"provider","operator":"Equals","expected_provider":"openai"},
            {
                "type":"group",
                "logical_operator":"Or",
                "children":[
                    {"type":"modelName","operator":"Contains","expected_model_name":"gpt-4"},
                    {"type":"modelName","operator":"Contains","expected_model_name":"o1"}
                ]
            }
        ]
    }`
	cond, err := rules.UnmarshalCondition([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	grp, ok := cond.(*rules.RuleGroup)
	if !ok {
		t.Fatalf("type = %T", cond)
	}
	if grp.LogicalOperator != rules.LogicalAnd {
		t.Errorf("op = %q", grp.LogicalOperator)
	}
	if len(grp.Children) != 2 {
		t.Fatalf("children len = %d", len(grp.Children))
	}
	if _, ok := grp.Children[0].(*rules.ProviderCondition); !ok {
		t.Errorf("child[0] type = %T", grp.Children[0])
	}
	inner, ok := grp.Children[1].(*rules.RuleGroup)
	if !ok {
		t.Fatalf("child[1] type = %T", grp.Children[1])
	}
	if inner.LogicalOperator != rules.LogicalOr || len(inner.Children) != 2 {
		t.Errorf("inner group: %+v", inner)
	}
}

func TestUnmarshalCondition_Unknown_FallsBack(t *testing.T) {
	raw := `{"type":"futureCondition","customField":42}`
	cond, err := rules.UnmarshalCondition([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	uc, ok := cond.(*rules.UnknownCondition)
	if !ok {
		t.Fatalf("type = %T", cond)
	}
	if uc.ConditionType() != "futureCondition" {
		t.Errorf("ConditionType: %q", uc.ConditionType())
	}
	if _, ok := uc.Extra["customField"]; !ok {
		t.Errorf("expected customField in Extra, got %v", uc.Extra)
	}
}

func TestUnmarshalCondition_RoundTrip_UnknownFieldsPreserved(t *testing.T) {
	raw := `{"type":"provider","operator":"Equals","expected_provider":"openai","experimental_flag":"on"}`
	cond, err := rules.UnmarshalCondition([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(cond)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back map[string]json.RawMessage
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if string(back["experimental_flag"]) != `"on"` {
		t.Errorf("experimental_flag not preserved: %s", out)
	}
}

func TestUnmarshalCondition_MissingDiscriminator(t *testing.T) {
	_, err := rules.UnmarshalCondition([]byte(`{"operator":"Equals"}`))
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestUnmarshalCondition_NotObject(t *testing.T) {
	_, err := rules.UnmarshalCondition([]byte(`["not","an","object"]`))
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestRuleGroup_MarshalJSON_PreservesChildOrder(t *testing.T) {
	g := &rules.RuleGroup{
		Type:            "group",
		LogicalOperator: rules.LogicalAnd,
		Children: []rules.Condition{
			&rules.ProviderCondition{Type: "provider", Operator: rules.EnumEquals, ExpectedProvider: "openai"},
			&rules.ProtocolCondition{Type: "protocol", Operator: rules.EnumEquals, ExpectedProtocol: "messages"},
		},
	}
	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe struct {
		Type     string            `json:"type"`
		Children []json.RawMessage `json:"children"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if probe.Type != "group" || len(probe.Children) != 2 {
		t.Fatalf("bad shape: %s", raw)
	}
	first, err := rules.UnmarshalCondition(probe.Children[0])
	if err != nil {
		t.Fatalf("re-decode child 0: %v", err)
	}
	if _, ok := first.(*rules.ProviderCondition); !ok {
		t.Errorf("child 0 type = %T", first)
	}
}

func TestRuleGroup_UnmarshalJSON_ChildError(t *testing.T) {
	raw := `{"type":"group","logical_operator":"And","children":[1, 2, 3]}`
	_, err := rules.UnmarshalCondition([]byte(raw))
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestRuleGroup_UnmarshalJSON_PreservesUnknownSiblings(t *testing.T) {
	raw := `{"type":"group","logical_operator":"And","children":[],"audit_label":"v1"}`
	cond, err := rules.UnmarshalCondition([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	g, ok := cond.(*rules.RuleGroup)
	if !ok {
		t.Fatalf("type = %T", cond)
	}
	if _, ok := g.Extra["audit_label"]; !ok {
		t.Errorf("audit_label not preserved: %v", g.Extra)
	}
}

func TestRuleGroup_UnmarshalJSON_MalformedChildren(t *testing.T) {
	raw := `{"type":"group","logical_operator":"And","children":"not-an-array"}`
	_, err := rules.UnmarshalCondition([]byte(raw))
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestRuleGroup_UnmarshalJSON_NonObject(t *testing.T) {
	g := &rules.RuleGroup{}
	if err := g.UnmarshalJSON([]byte(`"not-an-object"`)); err == nil {
		t.Fatalf("expected error")
	}
}

func TestUnknownCondition_RoundTrip(t *testing.T) {
	raw := `{"type":"futureCondition","custom":"x","nested":{"k":1}}`
	cond, err := rules.UnmarshalCondition([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(cond)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got, want map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("got unmarshal: %v", err)
	}
	if err := json.Unmarshal([]byte(raw), &want); err != nil {
		t.Fatalf("want unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip diverged.\n got=%s\nwant=%s", out, raw)
	}
}

func TestConditionTypes_BlankImpl(t *testing.T) {
	tests := []struct {
		name string
		cond rules.Condition
		want string
	}{
		{"provider", rules.ProviderCondition{}, "provider"},
		{"protocol", rules.ProtocolCondition{}, "protocol"},
		{"modelName", rules.ModelNameCondition{}, "modelName"},
		{"header", rules.HeaderCondition{}, "header"},
		{"tag", rules.TagCondition{}, "tag"},
		{"group", rules.RuleGroup{}, "group"},
		{"unknown", rules.UnknownCondition{Type: "x"}, "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cond.ConditionType(); got != tt.want {
				t.Errorf("ConditionType = %q, want %q", got, tt.want)
			}
		})
	}
}
