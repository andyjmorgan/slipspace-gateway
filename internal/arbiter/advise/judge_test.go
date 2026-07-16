package advise

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	contractsadvise "github.com/andyjmorgan/slipspace-gateway/contracts/advise"
)

// judgeUpstream captures what the judge sent, guarded for -race (the httptest
// handler runs on the server goroutine).
type judgeUpstream struct {
	mu     sync.Mutex
	method string
	path   string
	auth   string
	ver    string
	body   struct {
		Model    string `json:"model"`
		System   string `json:"system"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
}

// judgeText wraps text in an Anthropic messages response body.
func judgeText(text string) string {
	raw, _ := json.Marshal(text) //nolint:errcheck // string marshal cannot fail
	return fmt.Sprintf(`{"id":"msg_1","type":"message","role":"assistant","model":"judge-model-internal",
		"stop_reason":"end_turn","content":[{"type":"text","text":%s}]}`, raw)
}

// newJudgeServer serves answer as the judge's reply, recording the request.
func newJudgeServer(t *testing.T, answer string) (*httptest.Server, *judgeUpstream) {
	t.Helper()
	up := &judgeUpstream{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up.mu.Lock()
		defer up.mu.Unlock()
		up.method = r.Method
		up.path = r.URL.Path
		up.auth = r.Header.Get("Authorization")
		up.ver = r.Header.Get("anthropic-version")
		if err := json.NewDecoder(r.Body).Decode(&up.body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)
	return srv, up
}

var testCandidates = []string{"cheap-candidate-a", "cheap-candidate-b"}

func newTestJudge(baseURL string) *Judge {
	return NewJudge(baseURL, "sk-advisor-test", "judge-model-internal", "", testCandidates, 5*time.Second)
}

func TestJudge_HappyPath(t *testing.T) {
	srv, up := newJudgeServer(t, judgeText(`{"switch":true,"model":"cheap-candidate-a","reason":"trivial","confidence":0.9}`))
	j := newTestJudge(srv.URL)

	req := contractsadvise.Request{
		AgentFamily:      "claude-code",
		Entrypoint:       "cli",
		IsSubagent:       true,
		Model:            "requested-model-internal",
		SystemPrefix:     "You are a subagent.",
		FirstUserMessage: "List the files in the repo root.",
		ToolNames:        []string{"Read", "Bash"},
	}
	verdict, err := j.Judge(context.Background(), req)
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}

	want := contractsadvise.Verdict{Switch: true, Model: "cheap-candidate-a", Reason: "trivial", Confidence: 0.9}
	if verdict != want {
		t.Errorf("verdict = %+v, want %+v", verdict, want)
	}

	up.mu.Lock()
	defer up.mu.Unlock()
	if up.method != http.MethodPost || up.path != "/v1/messages" {
		t.Errorf("upstream saw %s %s, want POST /v1/messages", up.method, up.path)
	}
	if up.auth != "Bearer sk-advisor-test" {
		t.Errorf("Authorization = %q, want Bearer sk-advisor-test", up.auth)
	}
	if up.ver != "2023-06-01" {
		t.Errorf("anthropic-version = %q, want 2023-06-01", up.ver)
	}
	if up.body.Model != "judge-model-internal" {
		t.Errorf("judge request model = %q, want judge-model-internal", up.body.Model)
	}
	for _, c := range testCandidates {
		if !strings.Contains(up.body.System, c) {
			t.Errorf("system prompt missing candidate %q:\n%s", c, up.body.System)
		}
	}
	if len(up.body.Messages) != 1 || up.body.Messages[0].Role != "user" {
		t.Fatalf("messages = %+v, want one user message", up.body.Messages)
	}
	if !strings.Contains(up.body.Messages[0].Content, req.FirstUserMessage) {
		t.Errorf("user content missing task text:\n%s", up.body.Messages[0].Content)
	}
}

func TestJudge_ProseWrappedAnswerParses(t *testing.T) {
	answer := "Sure — here is my classification:\n```json\n" +
		`{"switch":true,"model":"cheap-candidate-b","reason":"lookup","confidence":0.8}` +
		"\n```\nLet me know if you need more."
	srv, _ := newJudgeServer(t, judgeText(answer))

	verdict, err := newTestJudge(srv.URL).Judge(context.Background(), contractsadvise.Request{})
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if !verdict.Switch || verdict.Model != "cheap-candidate-b" {
		t.Errorf("verdict = %+v, want switch to cheap-candidate-b", verdict)
	}
}

func TestJudge_NonCandidateModelRejected(t *testing.T) {
	srv, _ := newJudgeServer(t, judgeText(`{"switch":true,"model":"nomatch-internal","reason":"trivial","confidence":0.9}`))

	verdict, err := newTestJudge(srv.URL).Judge(context.Background(), contractsadvise.Request{})
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if verdict.Switch {
		t.Errorf("verdict.Switch = true, want false for non-candidate model")
	}
	if !strings.HasPrefix(verdict.Reason, "judge_noncandidate") {
		t.Errorf("verdict.Reason = %q, want judge_noncandidate prefix", verdict.Reason)
	}
}

func TestJudge_MalformedAnswerIsContinueNotError(t *testing.T) {
	srv, _ := newJudgeServer(t, judgeText("I am unable to classify this task."))

	verdict, err := newTestJudge(srv.URL).Judge(context.Background(), contractsadvise.Request{})
	if err != nil {
		t.Fatalf("Judge: %v (malformed answer must not be an error)", err)
	}
	if verdict.Switch {
		t.Errorf("verdict.Switch = true, want false")
	}
	if verdict.Reason != "judge_malformed" {
		t.Errorf("verdict.Reason = %q, want judge_malformed", verdict.Reason)
	}
}

func TestJudge_UpstreamStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	if _, err := newTestJudge(srv.URL).Judge(context.Background(), contractsadvise.Request{}); err == nil {
		t.Fatal("Judge against 500 upstream: want error")
	}
}

func TestJudge_UpstreamUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	if _, err := newTestJudge(url).Judge(context.Background(), contractsadvise.Request{}); err == nil {
		t.Fatal("Judge against closed upstream: want error")
	}
}

func TestJudge_MalformedBaseURL(t *testing.T) {
	// A control byte in the base URL makes http.NewRequestWithContext reject
	// it before any network I/O.
	j := NewJudge("http://\x7f", "sk-advisor-test", "judge-model-internal", "", testCandidates, time.Second)
	if _, err := j.Judge(context.Background(), contractsadvise.Request{}); err == nil {
		t.Fatal("Judge with malformed base URL: want request-build error")
	}
}

func TestJudge_ResponseReadError(t *testing.T) {
	// Declaring a Content-Length larger than the bytes written makes the
	// upstream abort the response mid-body: the judge's read fails after the
	// connection was already accepted.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "4096")
		_, _ = w.Write([]byte(`{"id":"msg_1"`))
	}))
	t.Cleanup(srv.Close)

	if _, err := newTestJudge(srv.URL).Judge(context.Background(), contractsadvise.Request{}); err == nil {
		t.Fatal("Judge with truncated upstream body: want read error")
	}
}

func TestJudge_DefaultRubricApplied(t *testing.T) {
	srv, up := newJudgeServer(t, judgeText(`{"switch":false,"reason":"complex"}`))

	// newTestJudge passes rubric "" — the built-in default must reach the
	// upstream system prompt.
	if _, err := newTestJudge(srv.URL).Judge(context.Background(), contractsadvise.Request{}); err != nil {
		t.Fatalf("Judge: %v", err)
	}

	up.mu.Lock()
	defer up.mu.Unlock()
	if !strings.Contains(up.body.System, "routing judge for an AI gateway") {
		t.Errorf("system prompt missing the default rubric:\n%s", up.body.System)
	}
}

func TestParseVerdict(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		wantOK bool
		want   contractsadvise.Verdict
	}{
		{
			name:   "bare JSON",
			text:   `{"switch":true,"model":"cheap-candidate-a","reason":"trivial","confidence":0.9}`,
			wantOK: true,
			want:   contractsadvise.Verdict{Switch: true, Model: "cheap-candidate-a", Reason: "trivial", Confidence: 0.9},
		},
		{
			name:   "code fenced",
			text:   "```json\n{\"switch\":false,\"reason\":\"complex\"}\n```",
			wantOK: true,
			want:   contractsadvise.Verdict{Switch: false, Reason: "complex"},
		},
		{
			name:   "prose wrapped",
			text:   `My answer: {"switch":true,"model":"cheap-candidate-b"} — hope that helps.`,
			wantOK: true,
			want:   contractsadvise.Verdict{Switch: true, Model: "cheap-candidate-b"},
		},
		{
			name:   "no braces",
			text:   "cannot classify",
			wantOK: false,
		},
		{
			name:   "invalid JSON between braces",
			text:   `{switch: yes}`,
			wantOK: false,
		},
		{
			name:   "empty text",
			text:   "",
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseVerdict(tc.text)
			if ok != tc.wantOK {
				t.Fatalf("ok = %t, want %t", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("verdict = %+v, want %+v", got, tc.want)
			}
		})
	}
}
