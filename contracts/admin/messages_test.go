package admin_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
)

func TestMessageEntry_RoundTrip(t *testing.T) {
	t.Parallel()
	in := adminc.MessageEntry{
		EventID:       "evt-1",
		At:            time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC),
		CorrelationID: "corr-1",
		Provider:      "openai",
		Protocol:      "chat_completions",
		Model:         "gpt-4o-mini",
		Configuration: "production",
		StatusCode:    200,
		DurationMs:    42,
		Streaming:     true,
		RulesMatched: []adminc.RuleHit{
			{RuleName: "claude-haiku-redirect", ActionsApplied: []string{"changeProvider"}},
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out adminc.MessageEntry
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.EventID != in.EventID || out.Provider != in.Provider || out.Model != in.Model {
		t.Errorf("scalars mismatch: %+v vs %+v", out, in)
	}
	if !out.At.Equal(in.At) {
		t.Errorf("At = %v want %v", out.At, in.At)
	}
	if len(out.RulesMatched) != 1 || out.RulesMatched[0].RuleName != "claude-haiku-redirect" {
		t.Errorf("RulesMatched = %+v", out.RulesMatched)
	}
}

func TestMessageEntry_OmitsEmptyOptionalFields(t *testing.T) {
	t.Parallel()
	in := adminc.MessageEntry{
		EventID:    "e",
		At:         time.Unix(0, 0).UTC(),
		StatusCode: 503,
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, banned := range []string{`"provider"`, `"protocol"`, `"model"`, `"configuration"`, `"streaming"`, `"upstream_error"`, `"rules_matched"`, `"correlation_id"`} {
		if strings.Contains(s, banned) {
			t.Errorf("unexpected key %s in %s", banned, s)
		}
	}
}

func TestMessagesRecentResponse_AlwaysCarriesEntries(t *testing.T) {
	t.Parallel()
	resp := adminc.MessagesRecentResponse{Capacity: 100}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// `entries` is not omitempty — the SPA expects a key even when the
	// ring is empty so it can render "no entries yet" without
	// branching on null.
	if !strings.Contains(string(b), `"entries"`) {
		t.Errorf("missing entries key: %s", b)
	}
}
