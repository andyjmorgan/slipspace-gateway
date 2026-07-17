package admin

import "time"

// AdviseAuditItem is one routing judgement on the wire — the payload the
// judge saw plus the verdict it returned — for GET /api/v1/advise/audit.
// Mirrors the arbiter's advise_audit row (store.AdviseAuditEntry) in
// snake_case; the SPA decodes the generated TypeScript shape without
// translation.
type AdviseAuditItem struct {
	// ReceivedAt is when the advisory request arrived — the keyset cursor
	// for paging (?before=).
	ReceivedAt time.Time `json:"received_at"`

	// GatewayID is the HMAC-authenticated gateway that asked.
	GatewayID string `json:"gateway_id"`

	// ConversationID is the subagent conversation the verdict decides for.
	ConversationID string `json:"conversation_id"`

	// SessionID is the parent session, when the gateway resolved one.
	SessionID string `json:"session_id,omitempty"`

	// Configuration is the gateway configuration the conversation runs under.
	Configuration string `json:"configuration,omitempty"`

	// Protocol is the wire protocol of the judged conversation.
	Protocol string `json:"protocol,omitempty"`

	// Provider is the pre-pin upstream provider.
	Provider string `json:"provider,omitempty"`

	// RequestedModel is the model the conversation asked for before any pin.
	RequestedModel string `json:"requested_model,omitempty"`

	// AgentFamily is the identified client family (e.g. claude-code).
	AgentFamily string `json:"agent_family,omitempty"`

	// Entrypoint is the client entrypoint parsed from its User-Agent.
	Entrypoint string `json:"entrypoint,omitempty"`

	// IsSubagent reports the tier-1 identification that gated eligibility.
	IsSubagent bool `json:"is_subagent"`

	// ToolNames is the tool list the judge saw.
	ToolNames []string `json:"tool_names,omitempty"`

	// SystemPrefix is the truncated system-prompt excerpt the judge saw.
	SystemPrefix string `json:"system_prefix,omitempty"`

	// FirstUserMessage is the truncated task excerpt the judge saw.
	FirstUserMessage string `json:"first_user_message,omitempty"`

	// VerdictSwitch reports whether the judge recommended switching model.
	// Consumers MUST key on this field: a declined verdict can still carry
	// a non-empty VerdictModel (structured outputs cannot conditionally
	// force it empty), and the gateway ignores the model unless switching.
	VerdictSwitch bool `json:"verdict_switch"`

	// VerdictModel is the recommended model when switching.
	VerdictModel string `json:"verdict_model,omitempty"`

	// VerdictReason is the judge's short justification.
	VerdictReason string `json:"verdict_reason,omitempty"`

	// VerdictConfidence is the judge's self-reported confidence (0..1).
	VerdictConfidence float64 `json:"verdict_confidence"`

	// CacheHit reports the verdict came from the template cache (no judge
	// inference ran for this call).
	CacheHit bool `json:"cache_hit"`

	// JudgeLatencyMs is the judge inference latency; 0 on cache hits.
	JudgeLatencyMs int `json:"judge_latency_ms"`

	// Error is the judge failure text when no verdict was produced (the
	// gateway failed open and the conversation ran as configured).
	Error string `json:"error,omitempty"`
}

// AdviseAuditPage is the response shape for GET /api/v1/advise/audit:
// judgements newest-first, keyset-paged by received_at (?before=).
type AdviseAuditPage struct {
	// Items is one page of judgements, newest first. Pass the last item's
	// received_at as ?before= to fetch the next page.
	Items []AdviseAuditItem `json:"items"`
}

// AdviseSavingsItem is one down-ranked conversation's spend attribution for
// GET /api/v1/advise/audit/savings.
type AdviseSavingsItem struct {
	// ConversationID is the pinned conversation.
	ConversationID string `json:"conversation_id"`

	// RequestedModel is what the conversation asked for.
	RequestedModel string `json:"requested_model"`

	// PinnedModel is what the judge routed it to.
	PinnedModel string `json:"pinned_model"`

	// PinnedRequests is how many requests actually ran on the pin.
	PinnedRequests int64 `json:"pinned_requests"`

	// ActualUSD is the measured spend of the pinned requests.
	ActualUSD float64 `json:"actual_usd"`

	// CounterfactualUSD is the would-have-cost on the requested model,
	// ratio-scaled from advise.model_rates. Null when either model has no
	// configured rate — never guessed.
	CounterfactualUSD *float64 `json:"counterfactual_usd"`

	// SavedUSD is CounterfactualUSD - ActualUSD; null with it.
	SavedUSD *float64 `json:"saved_usd"`
}

// AdviseSavingsTotals aggregates the attribution across conversations and
// charges the judge's own inference spend against the saving.
type AdviseSavingsTotals struct {
	// ActualUSD is the summed measured spend of all pinned requests.
	ActualUSD float64 `json:"actual_usd"`

	// CounterfactualUSD sums only rows with a configured rate pair.
	CounterfactualUSD float64 `json:"counterfactual_usd"`

	// SavedUSD is the summed per-row saving (rate-covered rows only).
	SavedUSD float64 `json:"saved_usd"`

	// JudgeCostUSD is the judge's own inference spend in the window.
	JudgeCostUSD float64 `json:"judge_cost_usd"`

	// NetSavedUSD is SavedUSD - JudgeCostUSD.
	NetSavedUSD float64 `json:"net_saved_usd"`
}

// AdviseSavingsResponse is the response shape for
// GET /api/v1/advise/audit/savings.
type AdviseSavingsResponse struct {
	// Since is the window start the attribution covers.
	Since time.Time `json:"since"`

	// Items is the per-conversation attribution, largest actual spend first.
	Items []AdviseSavingsItem `json:"items"`

	// Totals is the aggregate including the judge-cost offset.
	Totals AdviseSavingsTotals `json:"totals"`
}
