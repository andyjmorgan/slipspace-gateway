package rules

import (
	"errors"
	"net/http"
	"testing"

	contractsrules "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
)

func TestApplyTranslate(t *testing.T) {
	t.Parallel()
	s := NewMutableState("anthropic", "messages", "", nil, http.Header{})

	out, err := applyAction(&contractsrules.TranslateAction{TargetProtocol: "chat"}, s, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out.Terminate {
		t.Error("translate is non-terminating; should not Terminate")
	}
	if s.SourceProtocol != "messages" {
		t.Errorf("SourceProtocol = %q, want messages (the inbound protocol)", s.SourceProtocol)
	}
	if s.Protocol != "chat" {
		t.Errorf("Protocol = %q, want chat (the target)", s.Protocol)
	}
}

func TestApplyTranslate_EmptyTarget(t *testing.T) {
	t.Parallel()
	s := NewMutableState("anthropic", "messages", "", nil, http.Header{})
	if _, err := applyAction(&contractsrules.TranslateAction{TargetProtocol: "  "}, s, nil); !errors.Is(err, errEmptyValue) {
		t.Fatalf("err = %v, want errEmptyValue", err)
	}
}

func TestApplyTranslate_SecondTranslateKeepsOriginalSource(t *testing.T) {
	t.Parallel()
	// A chain that translates messages->chat then chat->responses must still
	// record messages as the source so the response leg translates back to the
	// dialect the client actually spoke.
	s := NewMutableState("anthropic", "messages", "", nil, http.Header{})

	if _, err := applyAction(&contractsrules.TranslateAction{TargetProtocol: "chat"}, s, nil); err != nil {
		t.Fatalf("first translate: %v", err)
	}
	if _, err := applyAction(&contractsrules.TranslateAction{TargetProtocol: "responses"}, s, nil); err != nil {
		t.Fatalf("second translate: %v", err)
	}
	if s.SourceProtocol != "messages" {
		t.Errorf("SourceProtocol = %q, want messages (recorded once, never overwritten)", s.SourceProtocol)
	}
	if s.Protocol != "responses" {
		t.Errorf("Protocol = %q, want responses (latest target)", s.Protocol)
	}
}
