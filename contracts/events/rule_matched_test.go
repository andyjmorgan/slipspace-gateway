package events_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/contracts/events"
)

func TestRuleMatched_MarshalRoundTrip(t *testing.T) {
	t.Parallel()

	in := events.RuleMatched{
		CorrelationID:  "11111111-1111-1111-1111-111111111111",
		RuleID:         "22222222-2222-2222-2222-222222222222",
		RuleName:       "block-mistral",
		Configuration:  "prod",
		MatchedAt:      time.Date(2026, time.May, 15, 12, 0, 0, 0, time.UTC),
		ActionsApplied: []string{"changeProvider", "setHeader"},
		Terminated:     false,
		ErrorMessage:   "",
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out events.RuleMatched
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch:\n  in  = %#v\n  out = %#v", in, out)
	}
}

func TestRuleMatched_OmitemptyOptionals(t *testing.T) {
	t.Parallel()

	ev := events.RuleMatched{
		RuleName:       "x",
		Configuration:  "y",
		MatchedAt:      time.Unix(0, 0).UTC(),
		ActionsApplied: []string{},
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(data)
	for _, banned := range []string{"correlation_id", "rule_id", "terminated", "error_message"} {
		if strings.Contains(got, banned) {
			t.Errorf("expected %q to be omitted on zero value; got: %s", banned, got)
		}
	}
	for _, required := range []string{`"rule_name":"x"`, `"configuration":"y"`, `"matched_at":`, `"actions_applied":[]`} {
		if !strings.Contains(got, required) {
			t.Errorf("expected %s; got: %s", required, got)
		}
	}
}

func TestRuleMatched_TerminatingSerialises(t *testing.T) {
	t.Parallel()

	ev := events.RuleMatched{
		RuleName:       "block-pii",
		Configuration:  "prod",
		MatchedAt:      time.Unix(0, 0).UTC(),
		ActionsApplied: []string{"returnStatusCode"},
		Terminated:     true,
		ErrorMessage:   "boom",
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(data)
	for _, w := range []string{`"terminated":true`, `"error_message":"boom"`, `"actions_applied":["returnStatusCode"]`} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %s in: %s", w, got)
		}
	}
}
