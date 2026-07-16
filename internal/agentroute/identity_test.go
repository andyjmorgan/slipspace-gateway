package agentroute

import (
	"net/http"
	"testing"
)

func TestIdentify(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    Identity
	}{
		{
			name: "claude-cli sdk-cli with agent-id is a subagent",
			headers: map[string]string{
				"User-Agent":             "claude-cli/2.1.209 (external, sdk-cli)",
				"X-Claude-Code-Agent-Id": "a1b2c3",
			},
			want: Identity{Family: FamilyClaudeCode, Entrypoint: "sdk-cli", IsSubagent: true},
		},
		{
			name: "claude-cli cli without agent-id is not a subagent",
			headers: map[string]string{
				"User-Agent": "claude-cli/2.1.206 (external, cli)",
			},
			want: Identity{Family: FamilyClaudeCode, Entrypoint: "cli", IsSubagent: false},
		},
		{
			name: "claude-cli malformed UA without parenthetical keeps family, empty entrypoint",
			headers: map[string]string{
				"User-Agent": "claude-cli/2.1.209",
			},
			want: Identity{Family: FamilyClaudeCode, Entrypoint: "", IsSubagent: false},
		},
		{
			name: "claude-cli unterminated parenthetical yields empty entrypoint",
			headers: map[string]string{
				"User-Agent": "claude-cli/2.1.209 (external, sdk-cli",
			},
			want: Identity{Family: FamilyClaudeCode, Entrypoint: "", IsSubagent: false},
		},
		{
			name: "claude-cli single-token parenthetical yields empty entrypoint",
			headers: map[string]string{
				"User-Agent": "claude-cli/2.1.209 (external)",
			},
			want: Identity{Family: FamilyClaudeCode, Entrypoint: "", IsSubagent: false},
		},
		{
			name: "codex main agent: session equals thread",
			headers: map[string]string{
				"Originator": "codex-tui",
				"Session-Id": "s-123",
				"Thread-Id":  "s-123",
			},
			want: Identity{Family: FamilyCodex, Entrypoint: "codex-tui", IsSubagent: false},
		},
		{
			name: "codex subagent: thread differs from session",
			headers: map[string]string{
				"Originator": "codex-tui",
				"Session-Id": "s-123",
				"Thread-Id":  "t-456",
			},
			want: Identity{Family: FamilyCodex, Entrypoint: "codex-tui", IsSubagent: true},
		},
		{
			name: "codex by UA prefix without Originator",
			headers: map[string]string{
				"User-Agent": "codex/1.2.3",
			},
			want: Identity{Family: FamilyCodex, Entrypoint: "", IsSubagent: false},
		},
		{
			name: "codex missing thread-id is not a subagent",
			headers: map[string]string{
				"Originator": "codex-tui",
				"Session-Id": "s-123",
			},
			want: Identity{Family: FamilyCodex, Entrypoint: "codex-tui", IsSubagent: false},
		},
		{
			name: "unknown UA yields zero identity",
			headers: map[string]string{
				"User-Agent": "curl/8.5.0",
			},
			want: Identity{},
		},
		{
			name:    "no headers at all yields zero identity",
			headers: nil,
			want:    Identity{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			for k, v := range tc.headers {
				h.Set(k, v)
			}
			got := Identify(h)
			if got != tc.want {
				t.Fatalf("Identify() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
