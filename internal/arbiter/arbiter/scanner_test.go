package arbiter

import (
	"encoding/json"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/arbiter/config"
	"github.com/andyjmorgan/sluice-gateway/internal/arbiter/store"
)

// spanWith builds a request event whose span_event carries the given gen_ai
// content, matching the stored {gen_ai_content:{input_messages,output_messages}}
// shape BuildUnits decodes.
func spanWith(correlationID string, gc map[string]any) store.RequestEvent {
	blob, _ := json.Marshal(map[string]any{"gen_ai_content": gc})
	return store.RequestEvent{CorrelationID: correlationID, SpanEvent: blob}
}

func msg(role string, parts ...map[string]any) map[string]any {
	return map[string]any{"role": role, "parts": parts}
}
func textPart(s string) map[string]any { return map[string]any{"type": "text", "content": s} }
func toolResult(s string) map[string]any {
	return map[string]any{"type": "tool_call_response", "id": "t1", "result": s}
}

func TestBuildUnits(t *testing.T) {
	e := spanWith("c1", map[string]any{
		"input_messages": []map[string]any{
			msg("user", textPart("hello"), toolResult("tool output")),
			msg("system", textPart("you are helpful")),
		},
		"output_messages": []map[string]any{
			msg("assistant", textPart("hi there")),
		},
	})
	units := BuildUnits(e)
	if len(units) != 4 {
		t.Fatalf("units = %d, want 4: %+v", len(units), units)
	}
	// IDs are section:msg:part and stable.
	got := map[string]Unit{}
	for _, u := range units {
		got[u.ID] = u
	}
	if u := got["in:0:0"]; u.Kind != kindText || u.Role != "user" || u.Text != "hello" {
		t.Errorf("in:0:0 = %+v", u)
	}
	if u := got["in:0:1"]; u.Kind != kindToolCallResponse || u.Text != "tool output" {
		t.Errorf("in:0:1 = %+v", u)
	}
	if u := got["out:0:0"]; u.Kind != kindText || u.Role != "assistant" || u.Text != "hi there" {
		t.Errorf("out:0:0 = %+v", u)
	}
}

func TestBuildUnits_EmptyAndResidual(t *testing.T) {
	// Empty text parts are dropped; an unmodeled type is residual-captured as opaque.
	e := spanWith("c2", map[string]any{
		"input_messages": []map[string]any{
			msg("user", textPart(""), map[string]any{"type": "image", "content": "alt text here"}),
		},
	})
	units := BuildUnits(e)
	if len(units) != 1 {
		t.Fatalf("units = %d, want 1 (empty dropped, opaque kept)", len(units))
	}
	if units[0].Kind != kindOpaque || units[0].Text != "alt text here" {
		t.Errorf("residual unit = %+v, want opaque/alt text here", units[0])
	}
}

func TestBuildUnits_NoContent(t *testing.T) {
	if u := BuildUnits(store.RequestEvent{CorrelationID: "x", SpanEvent: []byte(`{}`)}); u != nil {
		t.Errorf("no content => want nil units, got %+v", u)
	}
}

func TestAppliesTo(t *testing.T) {
	cases := []struct {
		check string
		unit  Unit
		want  bool
	}{
		{CheckInjection, Unit{Kind: kindToolCallResponse}, true},       // untrusted tool output
		{CheckInjection, Unit{Kind: kindText, Role: "user"}, true},     // user text
		{CheckInjection, Unit{Kind: kindText, Role: "system"}, false},  // trusted system prompt
		{CheckInjection, Unit{Kind: kindOpaque}, true},                 // residual safety net
		{CheckToxicity, Unit{Kind: kindText, Role: "assistant"}, true}, // assistant text
		{CheckToxicity, Unit{Kind: kindToolCallResponse}, false},       // not text
		{CheckPII, Unit{Kind: kindToolCall, Role: "assistant"}, true},  // tool args
		{CheckPII, Unit{Kind: kindReasoning}, false},                   // reasoning not scanned for PII
		{"unknown", Unit{Kind: kindText, Role: "user"}, false},         // unconfigured check
	}
	for _, c := range cases {
		if got := appliesTo(c.check, c.unit); got != c.want {
			t.Errorf("appliesTo(%s, %s/%s) = %v, want %v", c.check, c.unit.Kind, c.unit.Role, got, c.want)
		}
	}
}

func TestExplode(t *testing.T) {
	s := &Scanner{checkTypes: []string{CheckInjection, CheckToxicity}}
	e := spanWith("c3", map[string]any{
		"input_messages": []map[string]any{
			msg("user", textPart("scan me"), toolResult("from a tool")),
		},
	})
	tasks := s.Explode(e)
	// user text -> injection + toxicity (2); tool_call_response -> injection only (1) => 3 tasks.
	if len(tasks) != 3 {
		t.Fatalf("tasks = %d, want 3: %+v", len(tasks), tasks)
	}
	seen := map[string]bool{}
	for _, tk := range tasks {
		if tk.CorrelationID != "c3" {
			t.Errorf("task corr = %q", tk.CorrelationID)
		}
		if len(tk.UnitLocator) == 0 {
			t.Errorf("task %s/%s missing locator", tk.UnitID, tk.CheckType)
		}
		seen[tk.UnitID+"/"+tk.CheckType] = true
	}
	for _, want := range []string{"in:0:0/injection", "in:0:0/toxicity", "in:0:1/injection"} {
		if !seen[want] {
			t.Errorf("missing task %s", want)
		}
	}
	if seen["in:0:1/toxicity"] {
		t.Error("tool_call_response should not get a toxicity task")
	}
}

func TestExplode_NothingToScan(t *testing.T) {
	s := &Scanner{checkTypes: []string{CheckInjection}}
	if tasks := s.Explode(store.RequestEvent{CorrelationID: "x", SpanEvent: []byte(`{}`)}); tasks != nil {
		t.Errorf("want nil tasks, got %+v", tasks)
	}
}

func TestExplode_TagSelection(t *testing.T) {
	content := map[string]any{"input_messages": []map[string]any{msg("user", textPart("scan me"))}}
	cases := []struct {
		name     string
		selector tagSelector
		tags     []string
		wantScan bool
	}{
		{"no selector scans", tagSelector{}, []string{"tier:gold"}, true},
		{"include match scans", tagSelector{include: []string{"tier:*"}}, []string{"tier:gold"}, true},
		{"include miss skips", tagSelector{include: []string{"tier:*"}}, []string{"env:prod"}, false},
		{"exclude skips", tagSelector{exclude: []string{"env:internal"}}, []string{"env:internal"}, false},
		{"untagged with include skips", tagSelector{include: []string{"tier:*"}}, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Scanner{checkTypes: []string{CheckInjection}, tagSelector: c.selector}
			e := spanWith("c", content)
			e.Tags = c.tags
			tasks := s.Explode(e)
			if got := len(tasks) > 0; got != c.wantScan {
				t.Errorf("scanned=%v (tasks=%d), want %v", got, len(tasks), c.wantScan)
			}
		})
	}
}

func TestReduceVerdict(t *testing.T) {
	cases := []struct {
		name         string
		findings     []store.Finding
		inconclusive []string
		wantState    string
		wantScore    float32
		wantCategory string
	}{
		{"clean", nil, nil, StateClean, 0, ""},
		{"partial", nil, []string{"toxicity"}, StatePartial, 0, ""},
		{
			"flagged dominates inconclusive",
			[]store.Finding{{Category: "pii.email", Score: 0.7}, {Category: "injection.jailbreak", Score: 0.95}},
			[]string{"toxicity"},
			StateFlagged, 0.95, "injection.jailbreak",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := reduceVerdict("corr", c.findings, c.inconclusive)
			if v.State != c.wantState {
				t.Errorf("state = %q, want %q", v.State, c.wantState)
			}
			if v.MaxScore != c.wantScore {
				t.Errorf("max_score = %v, want %v", v.MaxScore, c.wantScore)
			}
			if v.TopCategory != c.wantCategory {
				t.Errorf("top_category = %q, want %q", v.TopCategory, c.wantCategory)
			}
			if v.FindingCount != len(c.findings) {
				t.Errorf("finding_count = %d, want %d", v.FindingCount, len(c.findings))
			}
		})
	}
}

func TestReduceVerdict_Severity(t *testing.T) {
	// Roll-up takes the worst severity across findings (info < warning < error).
	findings := []store.Finding{
		{Category: "pii.url", Score: 0.6, Severity: config.SeverityInfo},
		{Category: "pii.ssn", Score: 0.8, Severity: config.SeverityWarning},
	}
	if v := reduceVerdict("corr", findings, nil); v.Severity != config.SeverityWarning {
		t.Errorf("verdict severity = %q, want warning", v.Severity)
	}
	// A clean/partial span (no findings) carries no severity.
	if v := reduceVerdict("c2", nil, []string{"pii"}); v.Severity != "" {
		t.Errorf("no-finding severity = %q, want empty", v.Severity)
	}
}
