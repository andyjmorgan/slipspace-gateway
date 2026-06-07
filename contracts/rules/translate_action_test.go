package rules_test

import (
	"encoding/json"
	"errors"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

func TestTranslateAction_UnmarshalDispatch(t *testing.T) {
	raw := `{"type":"translate","target_protocol":"chat"}`
	act, err := rules.UnmarshalAction([]byte(raw))
	if err != nil {
		t.Fatalf("UnmarshalAction: %v", err)
	}
	ta, ok := act.(*rules.TranslateAction)
	if !ok {
		t.Fatalf("got %T, want *rules.TranslateAction", act)
	}
	if ta.ActionType() != "translate" {
		t.Errorf("ActionType = %q, want translate", ta.ActionType())
	}
	if ta.TargetProtocol != "chat" {
		t.Errorf("TargetProtocol = %q, want chat", ta.TargetProtocol)
	}
}

func TestTranslateAction_RoundTripPreservesUnknownFields(t *testing.T) {
	raw := `{"type":"translate","target_protocol":"messages","future_knob":true}`
	act, err := rules.UnmarshalAction([]byte(raw))
	if err != nil {
		t.Fatalf("UnmarshalAction: %v", err)
	}
	out, err := json.Marshal(act)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got, want map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal out: %v", err)
	}
	if err := json.Unmarshal([]byte(raw), &want); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if _, ok := got["future_knob"]; !ok {
		t.Errorf("unknown field future_knob dropped on round-trip: %s", out)
	}
}

func TestTranslateAction_Validate(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr error
	}{
		{
			name: "valid target",
			yaml: "name: t\ncondition:\n  type: protocol\n  operator: equals\n  expectedProtocol: messages\nactions:\n  - type: translate\n    targetProtocol: chat\n",
		},
		{
			name:    "empty target",
			yaml:    "name: t\ncondition:\n  type: protocol\n  operator: equals\n  expectedProtocol: messages\nactions:\n  - type: translate\n    targetProtocol: \"\"\n",
			wantErr: rules.ErrEmptyTranslateTarget,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rc rules.RuleContract
			if err := yaml.Unmarshal([]byte(tt.yaml), &rc); err != nil {
				t.Fatalf("unmarshal yaml: %v", err)
			}
			err := rc.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate = %v, want errors.Is %v", err, tt.wantErr)
			}
		})
	}
}
