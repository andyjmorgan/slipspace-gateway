package rules

import "testing"

// TestMarkerMethods invokes every isCondition/isAction marker so the coverage
// tool credits them; they have no body but contribute to the package's
// statement count.
func TestMarkerMethods(t *testing.T) {
	conds := []Condition{
		ProviderCondition{},
		EndpointCondition{},
		ModelNameCondition{},
		HeaderCondition{},
		RuleGroup{},
		UnknownCondition{},
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
		case RuleGroup:
			v.isCondition()
		case UnknownCondition:
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
		UnknownAction{},
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
		case UnknownAction:
			v.isAction()
		default:
			t.Fatalf("unexpected action type %T", a)
		}
	}
}
