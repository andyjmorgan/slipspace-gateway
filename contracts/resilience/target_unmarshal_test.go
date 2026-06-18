package resilience_test

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
	"github.com/andyjmorgan/slipspace-gateway/contracts/rules"
)

func TestResilienceTarget_JSON_ActionsRoundTrip(t *testing.T) {
	t.Parallel()
	raw := `{
		"name":"openai-eastus2",
		"provider":"openai",
		"order":1,
		"actions":[
			{"type":"changeUrl","new_url":"https://eastus2.api.openai.com/v1"},
			{"type":"changeModelName","new_model_name":"gpt-4o-mini"},
			{"type":"setHeader","header_name":"X-Region","header_action":"Set","header_value":"eastus2"}
		]
	}`
	var got resilience.ResilienceTarget
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "openai-eastus2" || got.Provider != "openai" || got.Order != 1 {
		t.Errorf("scalars wrong: %+v", got)
	}
	if len(got.Actions) != 3 {
		t.Fatalf("actions len = %d; want 3", len(got.Actions))
	}

	if _, ok := got.Actions[0].(*rules.ChangeUrlAction); !ok {
		t.Errorf("actions[0] type = %T; want *ChangeUrlAction", got.Actions[0])
	}
	if cm, ok := got.Actions[1].(*rules.ChangeModelNameAction); !ok || cm.NewModelName != "gpt-4o-mini" {
		t.Errorf("actions[1]: %T %+v", got.Actions[1], got.Actions[1])
	}
	if sh, ok := got.Actions[2].(*rules.SetHeaderAction); !ok || sh.HeaderName != "X-Region" {
		t.Errorf("actions[2]: %T %+v", got.Actions[2], got.Actions[2])
	}
}

func TestResilienceTarget_JSON_NoActions(t *testing.T) {
	t.Parallel()
	raw := `{"name":"primary","provider":"openai","order":1}`
	var got resilience.ResilienceTarget
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Actions != nil {
		t.Errorf("Actions = %+v; want nil when omitted", got.Actions)
	}
}

func TestResilienceTarget_JSON_UnknownActionFallsBack(t *testing.T) {
	t.Parallel()
	raw := `{
		"name":"weird",
		"provider":"openai",
		"order":1,
		"actions":[{"type":"customActionNotYetImplemented","payload":"foo"}]
	}`
	var got resilience.ResilienceTarget
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Actions) != 1 {
		t.Fatalf("actions len = %d", len(got.Actions))
	}
	ua, ok := got.Actions[0].(*rules.UnknownAction)
	if !ok {
		t.Fatalf("actions[0] type = %T; want *UnknownAction", got.Actions[0])
	}
	if ua.Type != "customActionNotYetImplemented" {
		t.Errorf("UnknownAction.Type = %q", ua.Type)
	}
}

func TestResilienceTarget_JSON_BadAction(t *testing.T) {
	t.Parallel()
	raw := `{"name":"x","provider":"openai","order":1,"actions":[42]}`
	var got resilience.ResilienceTarget
	if err := json.Unmarshal([]byte(raw), &got); err == nil {
		t.Fatal("expected error decoding malformed action; got nil")
	}
}

func TestResilienceTarget_YAML_ActionsRoundTrip(t *testing.T) {
	t.Parallel()
	in := `
name: openai-eastus2
provider: openai
order: 1
actions:
  - type: changeUrl
    newUrl: https://eastus2.api.openai.com/v1
  - type: changeApiKey
    apiKey: sk-test-redacted
  - type: addTag
    tag: surface:openai-chat
`
	var got resilience.ResilienceTarget
	if err := yaml.Unmarshal([]byte(in), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "openai-eastus2" || got.Provider != "openai" || got.Order != 1 {
		t.Errorf("scalars wrong: %+v", got)
	}
	if len(got.Actions) != 3 {
		t.Fatalf("actions len = %d; want 3", len(got.Actions))
	}
	if cu, ok := got.Actions[0].(*rules.ChangeUrlAction); !ok || cu.NewURL != "https://eastus2.api.openai.com/v1" {
		t.Errorf("actions[0]: %T %+v", got.Actions[0], got.Actions[0])
	}
	if _, ok := got.Actions[1].(*rules.ChangeApiKeyAction); !ok {
		t.Errorf("actions[1] type = %T", got.Actions[1])
	}
	if at, ok := got.Actions[2].(*rules.AddTagAction); !ok || at.Tag != "surface:openai-chat" {
		t.Errorf("actions[2]: %T %+v", got.Actions[2], got.Actions[2])
	}
}

func TestResilienceTarget_YAML_UnknownActionFallsBack(t *testing.T) {
	t.Parallel()
	in := `
name: weird
provider: openai
order: 1
actions:
  - type: customActionNotYetImplemented
    payload: foo
`
	var got resilience.ResilienceTarget
	if err := yaml.Unmarshal([]byte(in), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ua, ok := got.Actions[0].(*rules.UnknownAction)
	if !ok {
		t.Fatalf("actions[0] type = %T", got.Actions[0])
	}
	if ua.Type != "customActionNotYetImplemented" {
		t.Errorf("UnknownAction.Type = %q", ua.Type)
	}
}

func TestResilienceTarget_YAML_NoActionsField(t *testing.T) {
	t.Parallel()
	in := `
name: primary
provider: openai
order: 1
`
	var got resilience.ResilienceTarget
	if err := yaml.Unmarshal([]byte(in), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Actions != nil {
		t.Errorf("Actions = %+v; want nil when omitted", got.Actions)
	}
}

func TestResilienceTarget_YAML_LegacyScalarFieldsPreserved(t *testing.T) {
	t.Parallel()
	// Back-compat: the v1.0 schema (provider + model_rewrite +
	// failure_status_codes scalar fields) must keep working even with
	// the new Actions field on the type.
	in := `
name: legacy
provider: openai
order: 1
model_rewrite: gpt-4o-mini
failure_status_codes: [502, 503]
`
	var got resilience.ResilienceTarget
	if err := yaml.Unmarshal([]byte(in), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ModelRewrite != "gpt-4o-mini" || len(got.FailureStatusCodes) != 2 || got.FailureStatusCodes[0] != 502 {
		t.Errorf("legacy fields wrong: %+v", got)
	}
	if got.Actions != nil {
		t.Errorf("Actions should be nil; got %+v", got.Actions)
	}
}

func TestResilienceTarget_YAML_ActionsNotSequence(t *testing.T) {
	t.Parallel()
	in := `
name: bad
provider: openai
order: 1
actions: "not-a-list"
`
	var got resilience.ResilienceTarget
	if err := yaml.Unmarshal([]byte(in), &got); err == nil {
		t.Fatal("expected error when actions is not a sequence; got nil")
	}
}

func TestResilienceTarget_JSON_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	original := `{
		"name":"openai-eastus2",
		"provider":"openai",
		"order":1,
		"weight":0,
		"actions":[
			{"type":"changeUrl","new_url":"https://eastus2.api.openai.com/v1"},
			{"type":"changeModelName","new_model_name":"gpt-4o-mini"}
		]
	}`
	var tgt resilience.ResilienceTarget
	if err := json.Unmarshal([]byte(original), &tgt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(&tgt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var roundTripped resilience.ResilienceTarget
	if err := json.Unmarshal(out, &roundTripped); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if roundTripped.Name != tgt.Name || roundTripped.Provider != tgt.Provider || roundTripped.Order != tgt.Order {
		t.Errorf("scalars diverged: %+v vs %+v", roundTripped, tgt)
	}
	if len(roundTripped.Actions) != len(tgt.Actions) {
		t.Fatalf("actions length diverged: %d vs %d", len(roundTripped.Actions), len(tgt.Actions))
	}
	if _, ok := roundTripped.Actions[0].(*rules.ChangeUrlAction); !ok {
		t.Errorf("round-trip lost changeUrl type: %T", roundTripped.Actions[0])
	}
	if _, ok := roundTripped.Actions[1].(*rules.ChangeModelNameAction); !ok {
		t.Errorf("round-trip lost changeModelName type: %T", roundTripped.Actions[1])
	}
}
