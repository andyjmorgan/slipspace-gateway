package admin

import (
	"encoding/json"
	"time"
)

// ToolCallEntry is the wire shape of one indexed tool call — the Tool-Call
// Index audit surface served at GET /api/v1/tool-calls (in a
// {tool_calls, next_cursor} envelope), GET /api/v1/tool-calls/{id}, and rendered
// in the console's Tool Calls page. A single invocation is joined across its
// call span (name + arguments, an assistant response) and its result span
// (result, the next request) by ToolCallID; Status is "resolved" once the result
// half is in, else "pending".
type ToolCallEntry struct {
	// ToolCallID is the provider-assigned id (toolu_… / call_…) the two halves
	// join on.
	ToolCallID string `json:"tool_call_id"`

	// ToolName is the function name the model called — the audit search target.
	ToolName string `json:"tool_name"`

	// SessionID is the session bundle root both halves share.
	SessionID string `json:"session_id"`

	// Status is "pending" (no result yet) or "resolved".
	Status string `json:"status"`

	// Executor marks who runs the tool ("server" for a provider-hosted tool,
	// "client" for a caller-executed one); empty when unknown.
	Executor string `json:"executor,omitempty"`

	// Provider / Configuration / Protocol / Model are the call span's routing
	// facts, carried so the audit list filters without a join.
	Provider      string `json:"provider,omitempty"`
	Configuration string `json:"configuration,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
	Model         string `json:"model,omitempty"`

	// Arguments is the model's argument object as raw JSON (omitted when the call
	// had none). Bounded only by the whole-content ingest cap, never partially
	// truncated.
	Arguments json.RawMessage `json:"arguments,omitempty"`

	// ArgumentsChars is the true (uncapped) size of the arguments, in code points.
	ArgumentsChars int `json:"arguments_chars"`

	// Result is the tool output text, capped at ingest; ResultChars carries the
	// true size so the renderer can flag truncation.
	Result string `json:"result,omitempty"`

	// ResultChars is the true (uncapped) size of the result, in code points.
	ResultChars int `json:"result_chars"`

	// CallCorrelationID / ResponseCorrelationID are the two source spans the call
	// and result halves came from.
	CallCorrelationID     string `json:"call_correlation_id,omitempty"`
	ResponseCorrelationID string `json:"response_correlation_id,omitempty"`

	// CalledAt / RespondedAt are the two halves' span times; nil until each half
	// has been ingested (a nil RespondedAt is the "pending" status).
	CalledAt    *time.Time `json:"called_at"`
	RespondedAt *time.Time `json:"responded_at"`

	// ObservedAt is the first-seen time (set on first insert, never updated) — the
	// row's stable ordering/keyset axis.
	ObservedAt time.Time `json:"observed_at"`
}
