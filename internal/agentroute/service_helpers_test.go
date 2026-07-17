package agentroute

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/contracts/advise"
	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	"github.com/andyjmorgan/slipspace-gateway/protocols/anthropic/messages"
)

func TestSystemPrefix(t *testing.T) {
	long := strings.Repeat("s", maxPrefixBytes)
	overLong, err := json.Marshal(long + "-tail-beyond-the-limit")
	if err != nil {
		t.Fatalf("marshal long system string: %v", err)
	}
	longBlock, err := json.Marshal([]messages.SystemBlock{
		{Type: "text", Text: long},
		{Type: "text", Text: "never-appended"},
	})
	if err != nil {
		t.Fatalf("marshal long system blocks: %v", err)
	}

	tests := []struct {
		name   string
		system json.RawMessage
		want   string
	}{
		{
			name:   "string form",
			system: json.RawMessage(`"You are a subagent"`),
			want:   "You are a subagent",
		},
		{
			name:   "string form truncated to the prefix limit",
			system: overLong,
			want:   long,
		},
		{
			name:   "block array joins with newline",
			system: json.RawMessage(`[{"type":"text","text":"first"},{"type":"text","text":"second"}]`),
			want:   "first\nsecond",
		},
		{
			name:   "block array stops appending past the limit",
			system: longBlock,
			want:   long,
		},
		{
			name:   "empty system",
			system: nil,
			want:   "",
		},
		{
			name:   "neither string nor block array",
			system: json.RawMessage(`{"type":"text","text":"object-form"}`),
			want:   "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &messages.MessagesRequest{System: tc.system}
			if got := systemPrefix(req); got != tc.want {
				t.Errorf("systemPrefix = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFirstUserMessage(t *testing.T) {
	msg := func(role string, content json.RawMessage) messages.Message {
		return messages.Message{Role: role, Content: content}
	}

	tests := []struct {
		name string
		msgs []messages.Message
		want string
	}{
		{
			name: "string content",
			msgs: []messages.Message{msg("user", json.RawMessage(`"summarize the loader"`))},
			want: "summarize the loader",
		},
		{
			name: "block array with a text block",
			msgs: []messages.Message{msg("user", json.RawMessage(`[{"type":"text","text":"the task"}]`))},
			want: "the task",
		},
		{
			name: "block array skips non-text blocks before the text block",
			msgs: []messages.Message{msg("user", json.RawMessage(
				`[{"type":"tool_result","tool_use_id":"t1","content":"ok"},{"type":"text","text":"after tools"}]`))},
			want: "after tools",
		},
		{
			name: "block array with only non-text blocks",
			msgs: []messages.Message{msg("user", json.RawMessage(
				`[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]`))},
			want: "",
		},
		{
			name: "assistant turn is skipped before the user turn",
			msgs: []messages.Message{
				msg("assistant", json.RawMessage(`"prior answer"`)),
				msg("user", json.RawMessage(`"the follow-up"`)),
			},
			want: "the follow-up",
		},
		{
			name: "assistant-only messages",
			msgs: []messages.Message{msg("assistant", json.RawMessage(`"prior answer"`))},
			want: "",
		},
		{
			name: "no messages",
			msgs: nil,
			want: "",
		},
		{
			name: "user content neither string nor block array",
			msgs: []messages.Message{msg("user", json.RawMessage(`{"type":"text","text":"object-form"}`))},
			want: "",
		},
		{
			// Claude Code prepends injected context in <system-reminder> markers;
			// the judge must see the task, not the boilerplate.
			name: "string content strips a leading system-reminder",
			msgs: []messages.Message{msg("user", json.RawMessage(
				`"<system-reminder>\nCLAUDE.md contents here\n</system-reminder>\nrun the echo probe"`))},
			want: "run the echo probe",
		},
		{
			name: "string content strips stacked system-reminders",
			msgs: []messages.Message{msg("user", json.RawMessage(
				`"<system-reminder>one</system-reminder> <system-reminder>two</system-reminder>  the task"`))},
			want: "the task",
		},
		{
			name: "reminder-only text block falls through to the task block",
			msgs: []messages.Message{msg("user", json.RawMessage(
				`[{"type":"text","text":"<system-reminder>injected</system-reminder>"},{"type":"text","text":"summarize the loader"}]`))},
			want: "summarize the loader",
		},
		{
			name: "unterminated reminder yields empty, not boilerplate",
			msgs: []messages.Message{msg("user", json.RawMessage(
				`"<system-reminder>never closed..."`))},
			want: "",
		},
		{
			name: "mid-text reminder is left alone (only leading blocks strip)",
			msgs: []messages.Message{msg("user", json.RawMessage(
				`"do the task <system-reminder>note</system-reminder>"`))},
			want: "do the task <system-reminder>note</system-reminder>",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &messages.MessagesRequest{Messages: tc.msgs}
			if got := firstUserMessage(req); got != tc.want {
				t.Errorf("firstUserMessage = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"under the limit", "short", "short"},
		{"exactly at the limit", strings.Repeat("a", maxPrefixBytes), strings.Repeat("a", maxPrefixBytes)},
		{"over the limit", strings.Repeat("b", maxPrefixBytes+7), strings.Repeat("b", maxPrefixBytes)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.in)
			if got != tc.want {
				t.Errorf("truncate returned %d bytes, want %d", len(got), len(tc.want))
			}
		})
	}
}

func TestToolNames(t *testing.T) {
	tests := []struct {
		name  string
		tools []messages.Tool
		want  []string
	}{
		{"no tools", nil, nil},
		{"multiple tools", []messages.Tool{{Name: "Read"}, {Name: "Bash"}}, []string{"Read", "Bash"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &messages.MessagesRequest{Tools: tc.tools}
			if got := toolNames(req); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("toolNames = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNewService_SkipsAdvisorWithoutSecret(t *testing.T) {
	stub := newAdvisorStub(t, http.StatusOK, advise.Verdict{Switch: true, Model: "cheap-model-a"})
	advisors := contractsconfig.AdvisorsConfig{
		"arbiter": contractsconfig.Advisor{ //nolint:gosec // test fixture, not a credential
			Endpoint:       stub.srv.URL,
			HMACSecretFile: "/dev/null",
			GatewayID:      "gw-test",
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(advisors, map[string][]byte{}, logger) // no secret loaded

	// The advisor was skipped at construction, so an otherwise-eligible
	// request resolves no client: Evaluate returns nil and never calls out.
	ctx := subagentContext("conv-nosecret-1")
	if pin := evaluate(ctx, svc, testAgentRouting(), subagentRequest(t), subagentHeaders()); pin != nil {
		t.Fatalf("Evaluate = %+v, want nil (advisor skipped)", pin)
	}
	time.Sleep(50 * time.Millisecond)
	if n := stub.calls.Load(); n != 0 {
		t.Fatalf("advisor received %d calls, want 0 (client was never built)", n)
	}
}

func TestService_Dispatch_SemaphoreSaturation(t *testing.T) {
	stub := newAdvisorStub(t, http.StatusOK, advise.Verdict{})
	svc := newTestService(stub.srv.URL)
	ar := testAgentRouting()
	ctx := subagentContext("conv-sat-1")
	req := subagentRequest(t)
	h := subagentHeaders()

	// Saturate the in-flight semaphore (same-package access) so dispatch's
	// non-blocking acquire fails.
	for i := 0; i < cap(svc.sem); i++ {
		svc.sem <- struct{}{}
	}

	if pin := evaluate(ctx, svc, ar, req, h); pin != nil {
		t.Fatalf("Evaluate under saturation = %+v, want nil", pin)
	}
	// The attempt was abandoned, never queued: no advisory call fires.
	time.Sleep(50 * time.Millisecond)
	if n := stub.calls.Load(); n != 0 {
		t.Fatalf("advisor received %d calls under saturation, want 0", n)
	}

	// Fail cleared the register entry, so once capacity frees a later request
	// of the same conversation retries classification.
	for i := 0; i < cap(svc.sem); i++ {
		<-svc.sem
	}
	if pin := evaluate(ctx, svc, ar, req, h); pin != nil {
		t.Fatalf("Evaluate after draining = %+v, want nil (verdict is async)", pin)
	}
	if !pollUntil(func() bool { return stub.calls.Load() == 1 }) {
		t.Fatalf("advisor calls = %d, want exactly 1 after draining the semaphore", stub.calls.Load())
	}
}
