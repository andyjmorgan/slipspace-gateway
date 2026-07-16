// Package advise defines the wire contract between the gateway's agent-aware
// routing client and the Arbiter's advisory endpoint (POST
// /api/v1/advise/route).
//
// The advisory channel is control-plane advice, distinct from both telemetry
// (OTLP) and audit (the Record webhook): the gateway asks once per eligible
// conversation birth, off the request path, and the Arbiter answers with a
// verdict the gateway validates against its own allow-list before pinning.
// The Arbiter recommends; gateway config decides.
package advise

// Request is the material the gateway sends the Arbiter to classify one
// conversation at birth. Prompt excerpts are truncated by the gateway before
// sending (the classifier reads a prefix, never the full body), so a Request
// stays small regardless of the conversation's actual size.
type Request struct {
	// Configuration is the gateway configuration name the request resolved to.
	Configuration string `json:"configuration"`

	// Protocol is the generative protocol of the conversation (e.g. "messages").
	Protocol string `json:"protocol"`

	// Provider is the provider selection resolved for the request pre-pin.
	Provider string `json:"provider"`

	// Model is the client-requested model name, pre-pin.
	Model string `json:"model"`

	// ConversationID is the pin key: the per-agent conversation identifier
	// (e.g. the X-Claude-Code-Agent-Id value).
	ConversationID string `json:"conversation_id"`

	// SessionID is the root session the conversation belongs to.
	SessionID string `json:"session_id,omitempty"`

	// AgentFamily is the tier-1 identity ("claude-code", "codex", ...).
	AgentFamily string `json:"agent_family"`

	// Entrypoint is the family-specific entrypoint ("cli", "sdk-cli").
	Entrypoint string `json:"entrypoint,omitempty"`

	// IsSubagent marks a spawned subagent conversation (the v1 trigger).
	IsSubagent bool `json:"is_subagent"`

	// SystemPrefix is the leading bytes of the system prompt (gateway-truncated).
	SystemPrefix string `json:"system_prefix,omitempty"`

	// FirstUserMessage is the leading bytes of the first user message — for a
	// subagent this is the task description (gateway-truncated).
	FirstUserMessage string `json:"first_user_message,omitempty"`

	// ToolNames is the names of the tools declared on the request.
	ToolNames []string `json:"tool_names,omitempty"`
}

// Verdict is the Arbiter's routing recommendation for one conversation.
// A zero Verdict (Switch false) means "continue as configured" and is also
// what the gateway synthesises on timeout or error — the channel fails open.
type Verdict struct {
	// Switch is true when the Arbiter recommends re-routing the conversation.
	Switch bool `json:"switch"`

	// Model is the recommended model when Switch is true. The gateway discards
	// the verdict unless Model is in the configuration's allow_models list.
	Model string `json:"model,omitempty"`

	// Reason is a short human-readable justification, propagated to telemetry.
	Reason string `json:"reason,omitempty"`

	// Confidence is the judge's self-reported confidence in [0,1].
	Confidence float64 `json:"confidence,omitempty"`
}
