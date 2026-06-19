package server

import (
	"encoding/json"
	"errors"
	"net/http"

	adminc "github.com/andyjmorgan/slipspace-gateway/contracts/admin"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
)

// This file serves the Tool-Call Index audit surface: the searchable per-call
// list (GET /api/v1/tool-calls), a single call (…/{id}), and the distinct
// tool-name facet (…/facets) that drives the audit page's tool dropdown. Each
// row is a tool invocation joined across its call + response spans; see the
// Tool-Call Index design note and internal/arbiter/store/toolcalls.go. The wire
// shape is contracts/admin.ToolCallEntry (generated to TS for the SPA).

// toolCallEntryFrom maps a store row to its wire entry, deriving status.
func toolCallEntryFrom(t store.ToolCall) adminc.ToolCallEntry {
	status := "pending"
	if t.Resolved() {
		status = "resolved"
	}
	return adminc.ToolCallEntry{
		ToolCallID:            t.ToolCallID,
		ToolName:              t.ToolName,
		SessionID:             t.SessionID,
		Status:                status,
		Executor:              t.Executor,
		Provider:              t.Provider,
		Configuration:         t.Configuration,
		Protocol:              t.Protocol,
		Model:                 t.Model,
		Arguments:             json.RawMessage(t.Arguments),
		ArgumentsChars:        t.ArgumentsChars,
		Result:                t.Result,
		ResultChars:           t.ResultChars,
		CallCorrelationID:     t.CallCorrelationID,
		ResponseCorrelationID: t.ResponseCorrelationID,
		CalledAt:              t.CalledAt,
		RespondedAt:           t.RespondedAt,
		ObservedAt:            t.ObservedAt,
	}
}

// handleToolCalls serves the filtered, keyset-paged tool-call list as
// {tool_calls, next_cursor}. Filters: ?name (tool name), ?session_id,
// ?provider/?configuration/?protocol/?model, ?status (pending|resolved), and
// an optional ?from/?to window — the same idioms as the event list.
func (s *Server) handleToolCalls(w http.ResponseWriter, r *http.Request) {
	from, to, bad := parseWindowBounds(r)
	if bad != "" {
		writeError(w, http.StatusBadRequest, "invalid "+bad)
		return
	}
	q := r.URL.Query()
	calls, next, err := s.queries.ListToolCalls(r.Context(), store.ToolCallListParams{
		From:          from,
		To:            to,
		Name:          q.Get("name"),
		SessionID:     q.Get("session_id"),
		Provider:      q.Get("provider"),
		Configuration: q.Get("configuration"),
		Protocol:      q.Get("protocol"),
		Model:         q.Get("model"),
		Status:        q.Get("status"),
		Cursor:        q.Get("cursor"),
		Limit:         limitParam(r, 0),
	})
	if err != nil {
		if errors.Is(err, store.ErrInvalidCursor) {
			writeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		s.queryError(w, "list tool calls", err)
		return
	}
	entries := make([]adminc.ToolCallEntry, len(calls))
	for i, c := range calls {
		entries[i] = toolCallEntryFrom(c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tool_calls": entries, "next_cursor": next})
}

// handleToolCall serves one tool call by id (full bounded arguments + result),
// 404 when the id is unknown.
func (s *Server) handleToolCall(w http.ResponseWriter, r *http.Request) {
	t, err := s.queries.GetToolCall(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrToolCallNotFound) {
			writeError(w, http.StatusNotFound, "tool call not found")
			return
		}
		s.queryError(w, "get tool call", err)
		return
	}
	writeJSON(w, http.StatusOK, toolCallEntryFrom(t))
}

// handleToolNames serves the distinct tool-name facet as {tool_names} for the
// audit page's tool dropdown.
func (s *Server) handleToolNames(w http.ResponseWriter, r *http.Request) {
	names, err := s.queries.ToolNames(r.Context())
	if err != nil {
		s.queryError(w, "tool names", err)
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tool_names": names})
}
