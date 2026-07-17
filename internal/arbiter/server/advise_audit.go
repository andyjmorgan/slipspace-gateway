package server

import (
	"net/http"
	"time"

	adminc "github.com/andyjmorgan/slipspace-gateway/contracts/admin"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/advise"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
)

// The advise audit API: GET /api/v1/advise/audit lists routing judgements
// (the payload the judge saw + the verdict), GET /api/v1/advise/audit/savings
// attributes the spend the judge's down-ranking avoided. Both read derived
// control-plane tables (advise_audit, request_events) — never the S3/spool
// Record channel — so the console stays inside invariant #4.

// The wire DTOs live in contracts/admin/advise.go (tygo-generated into the
// SPA); this file only maps store rows onto them.

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
	items := make([]adminc.AdviseAuditItem, 0, len(entries))
	for _, e := range entries {
		items = append(items, adminc.AdviseAuditItem{
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
	writeJSON(w, http.StatusOK, adminc.AdviseAuditPage{Items: items})
}

// handleAdviseSavings serves the savings attribution for switch verdicts
// since ?since= (RFC3339), or over the console-standard ?window= token
// (?since= wins when both are present; default the last 24 hours).
// Counterfactuals are ratio-scaled from the operator's advise.model_rates.
func (s *Server) handleAdviseSavings(w http.ResponseWriter, r *http.Request) {
	since := time.Now().Add(-24 * time.Hour)
	if r.URL.Query().Get("window") != "" {
		_, dur := parseWindow(r)
		since = time.Now().Add(-dur)
	}
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
func attributeSavings(rows []store.AdviseSavingsRow, judgeCostUSD float64, rates map[string]float64) adminc.AdviseSavingsResponse {
	items := make([]adminc.AdviseSavingsItem, 0, len(rows))
	var totals adminc.AdviseSavingsTotals
	for _, r := range rows {
		item := adminc.AdviseSavingsItem{
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
	return adminc.AdviseSavingsResponse{Items: items, Totals: totals}
}
