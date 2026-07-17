package advise

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	contractsadvise "github.com/andyjmorgan/slipspace-gateway/contracts/advise"
)

func TestResolveRubric(t *testing.T) {
	t.Parallel()

	t.Run("empty path returns built-in default", func(t *testing.T) {
		t.Parallel()
		got, err := ResolveRubric("")
		if err != nil {
			t.Fatalf("ResolveRubric(\"\") error: %v", err)
		}
		if got != defaultRubric {
			t.Fatalf("ResolveRubric(\"\") = %q, want built-in default", got)
		}
	})

	t.Run("file contents win when set", func(t *testing.T) {
		t.Parallel()
		p := filepath.Join(t.TempDir(), "rubric.md")
		const custom = "Custom rubric: only ever say continue."
		if err := os.WriteFile(p, []byte(custom), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := ResolveRubric(p)
		if err != nil {
			t.Fatalf("ResolveRubric(file) error: %v", err)
		}
		if got != custom {
			t.Fatalf("ResolveRubric(file) = %q, want %q", got, custom)
		}
	})

	t.Run("missing file errors", func(t *testing.T) {
		t.Parallel()
		if _, err := ResolveRubric(filepath.Join(t.TempDir(), "absent.md")); err == nil {
			t.Fatal("ResolveRubric(missing) = nil error, want error")
		}
	})
}

// TestJudge_ConversationLinkHeaders asserts the judge request carries the
// thread-id of the conversation being judged plus the named-agent id, so the
// inference is linked in telemetry — and that an empty conversation id sets
// no thread header rather than an empty one.
func TestJudge_ConversationLinkHeaders(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var gotThread, gotAgent []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotThread = append(gotThread, r.Header.Get("X-Slipspace-Thread-Id"))
		gotAgent = append(gotAgent, r.Header.Get("X-Slipspace-Agent-Id"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": `{"switch":false,"reason":"n/a"}`}},
		})
	}))
	defer upstream.Close()

	j := NewJudge(upstream.URL, "test-key", "judge-model-x", "", []string{"cheap-candidate-a"}, 5*time.Second)

	if _, err := j.Judge(context.Background(), contractsadvise.Request{ConversationID: "conv-42"}); err != nil {
		t.Fatalf("Judge with conversation: %v", err)
	}
	if _, err := j.Judge(context.Background(), contractsadvise.Request{}); err != nil {
		t.Fatalf("Judge without conversation: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(gotThread) != 2 {
		t.Fatalf("upstream saw %d calls, want 2", len(gotThread))
	}
	if gotThread[0] != "conv-42" {
		t.Errorf("call 1 X-Slipspace-Thread-Id = %q, want conv-42", gotThread[0])
	}
	if gotThread[1] != "" {
		t.Errorf("call 2 X-Slipspace-Thread-Id = %q, want unset", gotThread[1])
	}
	for i, a := range gotAgent {
		if a != JudgeAgentID {
			t.Errorf("call %d X-Slipspace-Agent-Id = %q, want %q", i+1, a, JudgeAgentID)
		}
	}
	if !strings.Contains(defaultRubric, "routing judge") {
		t.Error("defaultRubric lost its identifying phrase")
	}
}
