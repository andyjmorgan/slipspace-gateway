package server

import (
	"net/http"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/advise"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
)

// The advise audit API: GET /api/v1/advise/audit lists routing judgements
// (the payload the judge saw + the verdict), GET /api/v1/advise/audit/savings
// attributes the spend the judge's down-ranking avoided. Both read derived
// control-plane tables (advise_audit, request_events) — never the S3/spool
// Record channel — so the console stays inside invariant #4.

// adviseAuditItem is one judgement row on the wire, mirroring
// store.AdviseAuditEntry in snake_case.
type adviseAuditItem struct {
	ReceivedAt        time.Time `json:"received_at"`
	GatewayID         string    `json:"gateway_id"`
	ConversationID    string    `json:"conversation_id"`
	SessionID         string    `json:"session_id,omitempty"`
	Configuration     string    `json:"configuration,omitempty"`
	Protocol          string    `json:"protocol,omitempty"`
	Provider          string    `json:"provider,omitempty"`
	RequestedModel    string    `json:"requested_model,omitempty"`
	AgentFamily       string    `json:"agent_family,omitempty"`
	Entrypoint        string    `json:"entrypoint,omitempty"`
	IsSubagent        bool      `json:"is_subagent"`
	ToolNames         []string  `json:"tool_names,omitempty"`
	SystemPrefix      string    `json:"system_prefix,omitempty"`
	FirstUserMessage  string    `json:"first_user_message,omitempty"`
	VerdictSwitch     bool      `json:"verdict_switch"`
	VerdictModel      string    `json:"verdict_model,omitempty"`
	VerdictReason     string    `json:"verdict_reason,omitempty"`
	VerdictConfidence float64   `json:"verdict_confidence"`
	CacheHit          bool      `json:"cache_hit"`
	JudgeLatencyMs    int       `json:"judge_latency_ms"`
	Error             string    `json:"error,omitempty"`
}

// adviseSavingsItem is one down-ranked conversation's savings attribution.
// CounterfactualUSD and SavedUSD are null when the operator has not
// configured a rate for either model involved — the API never guesses.
type adviseSavingsItem struct {
	ConversationID    string   `json:"conversation_id"`
	RequestedModel    string   `json:"requested_model"`
	PinnedModel       string   `json:"pinned_model"`
	PinnedRequests    int64    `json:"pinned_requests"`
	ActualUSD         float64  `json:"actual_usd"`
	CounterfactualUSD *float64 `json:"counterfactual_usd"`
	SavedUSD          *float64 `json:"saved_usd"`
}

// adviseSavingsTotals sums the attribution. CounterfactualUSD and SavedUSD
// sum only the priced rows; NetSavedUSD is SavedUSD minus the judge's own
// spend in the window, so the routing layer's overhead is always on the bill.
type adviseSavingsTotals struct {
	ActualUSD         float64 `json:"actual_usd"`
	CounterfactualUSD float64 `json:"counterfactual_usd"`
	SavedUSD          float64 `json:"saved_usd"`
	JudgeCostUSD      float64 `json:"judge_cost_usd"`
	NetSavedUSD       float64 `json:"net_saved_usd"`
}

// adviseSavingsResponse is the GET /api/v1/advise/audit/savings body.
type adviseSavingsResponse struct {
	Since  time.Time           `json:"since"`
	Items  []adviseSavingsItem `json:"items"`
	Totals adviseSavingsTotals `json:"totals"`
}

// handleAdviseAudit serves the judgement log, newest first. ?limit= caps the
// page (store-clamped to 200); ?before= (RFC3339) is the keyset cursor —
// pass the last row's received_at to fetch the next page.
func (s *Server) handleAdviseAudit(w http.ResponseWriter, r *http.Request) {
	var before time.Time
	if v := r.URL.Query().Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid before")
			return
		}
		before = t
	}
	entries, err := s.queries.ListAdviseAudit(r.Context(), limitParam(r, 0), before)
	if err != nil {
		s.queryError(w, "advise audit", err)
		return
	}
	items := make([]adviseAuditItem, 0, len(entries))
	for _, e := range entries {
		items = append(items, adviseAuditItem{
			ReceivedAt:        e.ReceivedAt,
			GatewayID:         e.GatewayID,
			ConversationID:    e.ConversationID,
			SessionID:         e.SessionID,
			Configuration:     e.Configuration,
			Protocol:          e.Protocol,
			Provider:          e.Provider,
			RequestedModel:    e.RequestedModel,
			AgentFamily:       e.AgentFamily,
			Entrypoint:        e.Entrypoint,
			IsSubagent:        e.IsSubagent,
			ToolNames:         e.ToolNames,
			SystemPrefix:      e.SystemPrefix,
			FirstUserMessage:  e.FirstUserMessage,
			VerdictSwitch:     e.VerdictSwitch,
			VerdictModel:      e.VerdictModel,
			VerdictReason:     e.VerdictReason,
			VerdictConfidence: e.VerdictConfidence,
			CacheHit:          e.CacheHit,
			JudgeLatencyMs:    e.JudgeLatencyMs,
			Error:             e.Error,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleAdviseSavings serves the savings attribution for switch verdicts
// since ?since= (RFC3339; default the last 24 hours). Counterfactuals are
// ratio-scaled from the operator's advise.model_rates.
func (s *Server) handleAdviseSavings(w http.ResponseWriter, r *http.Request) {
	since := time.Now().Add(-24 * time.Hour)
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid since")
			return
		}
		since = t
	}
	rows, judgeCost, err := s.queries.AdviseSavings(r.Context(), since, advise.JudgeAgentID)
	if err != nil {
		s.queryError(w, "advise savings", err)
		return
	}
	var rates map[string]float64
	if s.appliedConfig != nil {
		rates = s.appliedConfig.Advise.ModelRates
	}
	out := attributeSavings(rows, judgeCost, rates)
	out.Since = since
	writeJSON(w, http.StatusOK, out)
}

// attributeSavings scales each down-ranked conversation's measured spend into
// its would-have-cost counterfactual: actual x rate[requested]/rate[pinned].
// Every price component the gateway's pricing engine sums (input, output,
// cache reads/writes) scales linearly with the model's base rate, so the
// ratio applies to the priced total. A row whose requested or pinned model
// has no configured rate keeps null counterfactual/saved and is excluded
// from those totals — never guessed. NetSavedUSD charges the judge's own
// spend against the saving.
func attributeSavings(rows []store.AdviseSavingsRow, judgeCostUSD float64, rates map[string]float64) adviseSavingsResponse {
	items := make([]adviseSavingsItem, 0, len(rows))
	var totals adviseSavingsTotals
	for _, r := range rows {
		item := adviseSavingsItem{
			ConversationID: r.ConversationID,
			RequestedModel: r.RequestedModel,
			PinnedModel:    r.PinnedModel,
			PinnedRequests: r.PinnedRequests,
			ActualUSD:      r.ActualUSD,
		}
		totals.ActualUSD += r.ActualUSD
		reqRate, pinRate := rates[r.RequestedModel], rates[r.PinnedModel]
		if reqRate > 0 && pinRate > 0 {
			cf := r.ActualUSD * reqRate / pinRate
			saved := cf - r.ActualUSD
			item.CounterfactualUSD = &cf
			item.SavedUSD = &saved
			totals.CounterfactualUSD += cf
			totals.SavedUSD += saved
		}
		items = append(items, item)
	}
	totals.JudgeCostUSD = judgeCostUSD
	totals.NetSavedUSD = totals.SavedUSD - judgeCostUSD
	return adviseSavingsResponse{Items: items, Totals: totals}
}
