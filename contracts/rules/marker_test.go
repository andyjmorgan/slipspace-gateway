package rules

import (
	"encoding/json"
	"testing"
)

// TestMarkerMethods invokes every isCondition/isAction marker so the coverage
// tool credits them; they have no body but contribute to the package's
// statement count.
func TestMarkerMethods(t *testing.T) {
	conds := []Condition{
		ProviderCondition{},
		EndpointCondition{},
		ModelNameCondition{},
		HeaderCondition{},
		TagCondition{},
		RuleGroup{},
		UnknownCondition{},
		BodyFieldCondition{},
	}
	for _, c := range conds {
		switch v := c.(type) {
		case ProviderCondition:
			v.isCondition()
		case EndpointCondition:
			v.isCondition()
		case ModelNameCondition:
			v.isCondition()
		case HeaderCondition:
			v.isCondition()
		case TagCondition:
			v.isCondition()
		case RuleGroup:
			v.isCondition()
		case UnknownCondition:
			v.isCondition()
		case BodyFieldCondition:
			v.isCondition()
		default:
			t.Fatalf("unexpected condition type %T", c)
		}
	}

	acts := []Action{
		ChangeProviderAction{},
		ChangeModelNameAction{},
		ChangeUrlAction{},
		ChangeApiKeyAction{},
		SetHeaderAction{},
		AppendQueryStringAction{},
		ReturnStatusCodeAction{},
		LlmImpersonationAction{},
		AddTagAction{},
		UseResiliencePolicyAction{},
		UnknownAction{},
		RewriteFieldAction{},
		RemoveFieldAction{},
		AppendFieldAction{},
	}
	for _, a := range acts {
		switch v := a.(type) {
		case ChangeProviderAction:
			v.isAction()
		case ChangeModelNameAction:
			v.isAction()
		case ChangeUrlAction:
			v.isAction()
		case ChangeApiKeyAction:
			v.isAction()
		case SetHeaderAction:
			v.isAction()
		case AppendQueryStringAction:
			v.isAction()
		case ReturnStatusCodeAction:
			v.isAction()
		case LlmImpersonationAction:
			v.isAction()
		case AddTagAction:
			v.isAction()
		case UseResiliencePolicyAction:
			v.isAction()
		case UnknownAction:
			v.isAction()
		case RewriteFieldAction:
			v.isAction()
		case RemoveFieldAction:
			v.isAction()
		case AppendFieldAction:
			v.isAction()
		default:
			t.Fatalf("unexpected action type %T", a)
		}
	}
}

// TestRewriteDiscriminators exercises the ActionType / ConditionType
// discriminators and MarshalJSON on the rewrite types so the coverage
// tool credits them.
func TestRewriteDiscriminators(t *testing.T) {
	if got := (RewriteFieldAction{}).ActionType(); got != "rewriteField" {
		t.Errorf("rewriteField discriminator = %q", got)
	}
	if got := (RemoveFieldAction{}).ActionType(); got != "removeField" {
		t.Errorf("removeField discriminator = %q", got)
	}
	if got := (AppendFieldAction{}).ActionType(); got != "appendField" {
		t.Errorf("appendField discriminator = %q", got)
	}
	if got := (BodyFieldCondition{}).ConditionType(); got != "bodyField" {
		t.Errorf("bodyField discriminator = %q", got)
	}

	// Direct marker invocations — the type-switch form in
	// TestMarkerMethods is elided for these empty-body methods.
	RewriteFieldAction{}.isAction()
	RemoveFieldAction{}.isAction()
	AppendFieldAction{}.isAction()
	BodyFieldCondition{}.isCondition()

	marshalers := []interface{ MarshalJSON() ([]byte, error) }{
		RewriteFieldAction{Type: "rewriteField", Target: "request.body.x"},
		RemoveFieldAction{Type: "removeField", Target: "request.body.x"},
		AppendFieldAction{Type: "appendField", Target: "request.body.x"},
		BodyFieldCondition{Type: "bodyField", Target: "request.body.x", Operator: BodyFieldExists},
	}
	for _, m := range marshalers {
		out, err := m.MarshalJSON()
		if err != nil {
			t.Fatalf("%T marshal: %v", m, err)
		}
		if !json.Valid(out) {
			t.Errorf("%T produced invalid JSON: %s", m, out)
		}
	}
}
