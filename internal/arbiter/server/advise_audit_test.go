package server

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/andyjmorgan/slipspace-gateway/contracts/admin"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/advise"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
)

func TestHandleAdviseAudit(t *testing.T) {
	received := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	q := &fakeQueries{adviseAudit: []store.AdviseAuditEntry{{
		ReceivedAt: received, GatewayID: "gw", ConversationID: "conv-1",
		RequestedModel: "big-model", AgentFamily: "claude-code", IsSubagent: true,
		ToolNames: []string{"Read", "Grep"}, SystemPrefix: "You are...",
		FirstUserMessage: "list the files", VerdictSwitch: true,
		VerdictModel: "small-model", VerdictReason: "trivial",
		VerdictConfidence: 0.9, JudgeLatencyMs: 2100,
	}}}
	resp := get(t, newQueryServer(t, q), "/api/v1/advise/audit?limit=5", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	if q.lastAdviseLimit != 5 {
		t.Errorf("limit plumbed = %d, want 5", q.lastAdviseLimit)
	}
	var body struct {
		Items []admin.AdviseAuditItem `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(body.Items))
	}
	it := body.Items[0]
	if !it.ReceivedAt.Equal(received) || it.ConversationID != "conv-1" ||
		!it.VerdictSwitch || it.VerdictModel != "small-model" ||
		it.JudgeLatencyMs != 2100 || len(it.ToolNames) != 2 {
		t.Errorf("item = %+v", it)
	}

	// ?before= is parsed and plumbed as the keyset cursor.
	before := "2026-07-17T06:00:00Z"
	if rec := get(t, newQueryServer(t, q), "/api/v1/advise/audit?before="+before, true); rec.Code != http.StatusOK {
		t.Fatalf("before status = %d", rec.Code)
	}
	want, _ := time.Parse(time.RFC3339, before)
	if !q.lastAdviseBefore.Equal(want) {
		t.Errorf("before plumbed = %v, want %v", q.lastAdviseBefore, want)
	}

	// Bad cursor, store error, and missing auth.
	if rec := get(t, newQueryServer(t, q), "/api/v1/advise/audit?before=yesterday", true); rec.Code != http.StatusBadRequest {
		t.Errorf("bad before status = %d, want 400", rec.Code)
	}
	if rec := get(t, newQueryServer(t, &fakeQueries{adviseAuditErr: errors.New("db")}), "/api/v1/advise/audit", true); rec.Code != http.StatusInternalServerError {
		t.Errorf("store error status = %d, want 500", rec.Code)
	}
	if rec := get(t, newQueryServer(t, q), "/api/v1/advise/audit", false); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauth status = %d, want 401", rec.Code)
	}
}

// newAdviseServer is newQueryServer with an applied-config snapshot carrying
// advise.model_rates, so the savings handler can price counterfactuals.
func newAdviseServer(t *testing.T, q Queries, rates map[string]float64) http.Handler {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("hunter2"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	console := config.Console{Username: "admin", PasswordHash: string(hash)}
	cfg := config.Config{}
	cfg.Advise.ModelRates = rates
	return New(console, stubPinger{}, q, nil, config.DefaultSpanFieldMaxBytes, discardLogger()).
		WithAppliedConfig(cfg).Handler()
}

func TestHandleAdviseSavings(t *testing.T) {
	q := &fakeQueries{
		adviseSavings: []store.AdviseSavingsRow{
			{ConversationID: "c1", RequestedModel: "big-model", PinnedModel: "small-model", PinnedRequests: 20, ActualUSD: 2.0},
			{ConversationID: "c2", RequestedModel: "unpriced-model", PinnedModel: "small-model", PinnedRequests: 3, ActualUSD: 1.0},
		},
		adviseJudgeCost: 0.5,
	}
	rates := map[string]float64{"big-model": 5, "small-model": 1}
	resp := get(t, newAdviseServer(t, q, rates), "/api/v1/advise/audit/savings", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	if q.lastSavingsJudgeID != advise.JudgeAgentID {
		t.Errorf("judge agent id plumbed = %q, want %q", q.lastSavingsJudgeID, advise.JudgeAgentID)
	}
	var body admin.AdviseSavingsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(body.Items))
	}
	// c1: both models priced -> counterfactual 2.0 * 5/1 = 10, saved 8.
	c1 := body.Items[0]
	if c1.CounterfactualUSD == nil || math.Abs(*c1.CounterfactualUSD-10) > 1e-9 ||
		c1.SavedUSD == nil || math.Abs(*c1.SavedUSD-8) > 1e-9 {
		t.Errorf("c1 = %+v", c1)
	}
	// c2: requested model unpriced -> null, never guessed.
	if body.Items[1].CounterfactualUSD != nil || body.Items[1].SavedUSD != nil {
		t.Errorf("c2 should have null counterfactual, got %+v", body.Items[1])
	}
	// Totals: actual sums all rows; counterfactual/saved only priced rows;
	// net charges the judge.
	tt := body.Totals
	if math.Abs(tt.ActualUSD-3) > 1e-9 || math.Abs(tt.CounterfactualUSD-10) > 1e-9 ||
		math.Abs(tt.SavedUSD-8) > 1e-9 || math.Abs(tt.JudgeCostUSD-0.5) > 1e-9 ||
		math.Abs(tt.NetSavedUSD-7.5) > 1e-9 {
		t.Errorf("totals = %+v", tt)
	}

	// ?since= is parsed and plumbed; default is ~24h back.
	since := "2026-07-16T00:00:00Z"
	if rec := get(t, newAdviseServer(t, q, rates), "/api/v1/advise/audit/savings?since="+since, true); rec.Code != http.StatusOK {
		t.Fatalf("since status = %d", rec.Code)
	}
	want, _ := time.Parse(time.RFC3339, since)
	if !q.lastSavingsSince.Equal(want) {
		t.Errorf("since plumbed = %v, want %v", q.lastSavingsSince, want)
	}
	if rec := get(t, newAdviseServer(t, q, rates), "/api/v1/advise/audit/savings?since=lastweek", true); rec.Code != http.StatusBadRequest {
		t.Errorf("bad since status = %d, want 400", rec.Code)
	}

	// No applied config (rates unknown): 200 with null counterfactuals.
	resp = get(t, newQueryServer(t, q), "/api/v1/advise/audit/savings", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("no-config status = %d", resp.Code)
	}
	body = admin.AdviseSavingsResponse{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Items[0].CounterfactualUSD != nil || body.Totals.SavedUSD != 0 {
		t.Errorf("no-config should not price: %+v", body)
	}

	// Store error and missing auth.
	if rec := get(t, newAdviseServer(t, &fakeQueries{adviseSavingsErr: errors.New("db")}, rates), "/api/v1/advise/audit/savings", true); rec.Code != http.StatusInternalServerError {
		t.Errorf("store error status = %d, want 500", rec.Code)
	}
	if rec := get(t, newAdviseServer(t, q, rates), "/api/v1/advise/audit/savings", false); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauth status = %d, want 401", rec.Code)
	}
}

func TestAttributeSavings_EmptyAndJudgeOnly(t *testing.T) {
	// No down-ranked conversations: totals are zero except the judge's own
	// spend, which shows as negative net (the layer cost money, saved none).
	out := attributeSavings(nil, 0.25, map[string]float64{"m": 1})
	if len(out.Items) != 0 {
		t.Fatalf("items = %d, want 0", len(out.Items))
	}
	if out.Totals.NetSavedUSD != -0.25 || out.Totals.JudgeCostUSD != 0.25 {
		t.Errorf("totals = %+v", out.Totals)
	}
}
