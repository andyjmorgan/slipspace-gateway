// Package agentroute implements agent-aware model routing: identify the
// calling agent from request headers (tier 1, deterministic, no body parse),
// ask an advisory endpoint to classify eligible conversations at their first
// request, and pin the verdict's model for the conversation's lifetime.
//
// The advisory call is asynchronous and fails open: the first request of a
// conversation always routes as configured, the verdict lands in the register
// out of band, and requests 2..N apply the pin. Nothing in this package ever
// blocks the request path (the spool invariant's spirit applies here too).
package agentroute

import (
	"net/http"
	"strings"
)

// Agent family names produced by Identify.
const (
	// FamilyClaudeCode is the Claude Code CLI / Claude Agent SDK family
	// (User-Agent "claude-cli/...").
	FamilyClaudeCode = "claude-code"

	// FamilyCodex is the OpenAI Codex family (Originator / codex User-Agent).
	FamilyCodex = "codex"
)

// Headers carrying agent identity. Presence of the Claude Code agent-id
// header is the subagent discriminator — verified against live capture
// (1,817/1,817 agreement with the billing-header cc_is_subagent flag); the
// system-prompt sentinel is deliberately not parsed here.
const (
	headerClaudeAgentID   = "X-Claude-Code-Agent-Id"
	headerCodexOriginator = "Originator"
	headerCodexSessionID  = "Session-Id"
	headerCodexThreadID   = "Thread-Id"
)

// Identity is the tier-1 agent identification for one request, derived
// entirely from headers.
type Identity struct {
	// Family is the agent product family (FamilyX constant), or empty when
	// the caller is not a recognised agent.
	Family string

	// Entrypoint is the family-specific entrypoint ("cli", "sdk-cli", or a
	// Codex originator value).
	Entrypoint string

	// IsSubagent marks a spawned subagent conversation — the population whose
	// task is fully stated in its first request, making birth classification
	// sound.
	IsSubagent bool
}

// Identify derives the tier-1 identity from request headers. It never reads
// the body: User-Agent names the family, the family's own headers mark
// subagent-ness (Claude Code: agent-id header present; Codex: Thread-Id
// differs from Session-Id).
func Identify(h http.Header) Identity {
	ua := h.Get("User-Agent")

	if strings.HasPrefix(ua, "claude-cli/") {
		return Identity{
			Family:     FamilyClaudeCode,
			Entrypoint: claudeEntrypoint(ua),
			IsSubagent: h.Get(headerClaudeAgentID) != "",
		}
	}

	if orig := h.Get(headerCodexOriginator); orig != "" || strings.HasPrefix(ua, "codex") {
		session := h.Get(headerCodexSessionID)
		thread := h.Get(headerCodexThreadID)
		return Identity{
			Family:     FamilyCodex,
			Entrypoint: orig,
			IsSubagent: session != "" && thread != "" && session != thread,
		}
	}

	return Identity{}
}

// claudeEntrypoint extracts the entrypoint token from a claude-cli User-Agent
// of the shape "claude-cli/2.1.209 (external, sdk-cli)". Empty when the
// parenthetical is absent or malformed.
func claudeEntrypoint(ua string) string {
	open := strings.IndexByte(ua, '(')
	if open < 0 {
		return ""
	}
	close := strings.IndexByte(ua[open:], ')')
	if close < 0 {
		return ""
	}
	inner := ua[open+1 : open+close]
	parts := strings.Split(inner, ",")
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-1])
}
