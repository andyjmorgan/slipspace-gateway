package rules

import (
	"testing"

	contractsrules "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
)

func TestApplyUseResiliencePolicy_SetsPolicyRef(t *testing.T) {
	t.Parallel()
	s := freshState(t)
	if _, err := applyAction(&contractsrules.UseResiliencePolicyAction{PolicyName: "ha"}, s, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if s.PolicyRef != "ha" {
		t.Errorf("PolicyRef = %q; want ha", s.PolicyRef)
	}
}

func TestApplyUseResiliencePolicy_LastWriterWins(t *testing.T) {
	t.Parallel()
	s := freshState(t)
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if _, err := applyAction(&contractsrules.UseResiliencePolicyAction{PolicyName: name}, s, nil); err != nil {
			t.Fatalf("apply %q: %v", name, err)
		}
	}
	if s.PolicyRef != "charlie" {
		t.Errorf("PolicyRef = %q; want charlie (last writer wins)", s.PolicyRef)
	}
}

func TestApplyUseResiliencePolicy_EmptyClears(t *testing.T) {
	t.Parallel()
	s := freshState(t)
	s.PolicyRef = "ha"
	if _, err := applyAction(&contractsrules.UseResiliencePolicyAction{PolicyName: ""}, s, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if s.PolicyRef != "" {
		t.Errorf("PolicyRef = %q; want cleared", s.PolicyRef)
	}
}

func TestApplyUseResiliencePolicy_TrimsWhitespace(t *testing.T) {
	t.Parallel()
	s := freshState(t)
	if _, err := applyAction(&contractsrules.UseResiliencePolicyAction{PolicyName: "  ha  "}, s, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if s.PolicyRef != "ha" {
		t.Errorf("PolicyRef = %q; want ha (trimmed)", s.PolicyRef)
	}
}

func TestApplyUseResiliencePolicy_NonTerminating(t *testing.T) {
	t.Parallel()
	s := freshState(t)
	out, err := applyAction(&contractsrules.UseResiliencePolicyAction{PolicyName: "ha"}, s, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out.Terminate {
		t.Error("useResiliencePolicy must not terminate the rule chain")
	}
	if out.Response != nil {
		t.Errorf("expected no synthesised response, got %+v", out.Response)
	}
}
