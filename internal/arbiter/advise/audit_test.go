package advise

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	contractsadvise "github.com/andyjmorgan/slipspace-gateway/contracts/advise"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
)

// stubAuditor is a hand-rolled auditor that records inserted rows.
type stubAuditor struct {
	entries []store.AdviseAuditEntry
	err     error
}

func (a *stubAuditor) InsertAdviseAudit(_ context.Context, e store.AdviseAuditEntry) error {
	a.entries = append(a.entries, e)
	return a.err
}

func auditRequest() contractsadvise.Request {
	return contractsadvise.Request{
		Configuration:    "production",
		Protocol:         "messages",
		Provider:         "anthropic",
		Model:            "big-model",
		ConversationID:   "conv-1",
		SessionID:        "sess-1",
		AgentFamily:      "claude-code",
		Entrypoint:       "cli",
		IsSubagent:       true,
		SystemPrefix:     "You are a subagent.",
		FirstUserMessage: "list the files",
		ToolNames:        []string{"Read", "Grep"},
	}
}

func TestAudit_FreshJudgement(t *testing.T) {
	verdict := contractsadvise.Verdict{Switch: true, Model: "small-model", Reason: "trivial", Confidence: 0.9}
	j := &stubJudge{verdict: verdict}
	a := &stubAuditor{}
	h := NewHandler(&stubVerifier{}, j, time.Minute, discardLogger()).WithAuditor(a)
	// Deterministic clock: each now() call advances 100ms, so the fresh
	// judgement's recorded latency is exactly the judge call's bracket.
	base := time.Now()
	var ticks int
	h.now = func() time.Time { ticks++; return base.Add(time.Duration(ticks) * 100 * time.Millisecond) }

	if rr := post(t, h, trustedHeaders(), requestBody(t, auditRequest())); rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if len(a.entries) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(a.entries))
	}
	e := a.entries[0]
	if e.GatewayID != "gw-test" || e.ConversationID != "conv-1" || e.SessionID != "sess-1" ||
		e.Configuration != "production" || e.RequestedModel != "big-model" ||
		e.AgentFamily != "claude-code" || !e.IsSubagent ||
		e.SystemPrefix != "You are a subagent." || e.FirstUserMessage != "list the files" ||
		len(e.ToolNames) != 2 {
		t.Errorf("payload fields = %+v", e)
	}
	if !e.VerdictSwitch || e.VerdictModel != "small-model" || e.VerdictReason != "trivial" ||
		e.VerdictConfidence != 0.9 || e.CacheHit || e.Error != "" {
		t.Errorf("verdict fields = %+v", e)
	}
	if e.JudgeLatencyMs != 100 {
		t.Errorf("judge latency = %dms, want 100", e.JudgeLatencyMs)
	}
}

func TestAudit_CacheHit(t *testing.T) {
	verdict := contractsadvise.Verdict{Switch: true, Model: "small-model", Reason: "trivial", Confidence: 0.9}
	j := &stubJudge{verdict: verdict}
	a := &stubAuditor{}
	h := NewHandler(&stubVerifier{}, j, time.Minute, discardLogger()).WithAuditor(a)

	body := requestBody(t, auditRequest())
	post(t, h, trustedHeaders(), body) // fresh: judge called, row 1
	post(t, h, trustedHeaders(), body) // cached: no judge call, row 2
	if j.calls != 1 {
		t.Fatalf("judge calls = %d, want 1", j.calls)
	}
	if len(a.entries) != 2 {
		t.Fatalf("audit rows = %d, want 2", len(a.entries))
	}
	cached := a.entries[1]
	if !cached.CacheHit || cached.JudgeLatencyMs != 0 || cached.VerdictModel != "small-model" {
		t.Errorf("cache-hit row = %+v", cached)
	}
	if a.entries[0].CacheHit {
		t.Error("fresh row must not be marked cache_hit")
	}
}

func TestAudit_JudgeFailure(t *testing.T) {
	j := &stubJudge{err: errors.New("upstream down")}
	a := &stubAuditor{}
	h := NewHandler(&stubVerifier{}, j, time.Minute, discardLogger()).WithAuditor(a)

	if rr := post(t, h, trustedHeaders(), requestBody(t, auditRequest())); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	if len(a.entries) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(a.entries))
	}
	e := a.entries[0]
	if e.Error != "upstream down" || e.VerdictSwitch || e.VerdictModel != "" || e.CacheHit {
		t.Errorf("failure row = %+v", e)
	}
}

func TestAudit_TruncatesPromptExcerpts(t *testing.T) {
	req := auditRequest()
	req.SystemPrefix = strings.Repeat("s", auditFieldCap+100)
	req.FirstUserMessage = strings.Repeat("u", auditFieldCap+100)
	a := &stubAuditor{}
	h := NewHandler(&stubVerifier{}, &stubJudge{}, time.Minute, discardLogger()).WithAuditor(a)

	post(t, h, trustedHeaders(), requestBody(t, req))
	if len(a.entries) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(a.entries))
	}
	if n := len(a.entries[0].SystemPrefix); n != auditFieldCap {
		t.Errorf("system_prefix = %d bytes, want %d", n, auditFieldCap)
	}
	if n := len(a.entries[0].FirstUserMessage); n != auditFieldCap {
		t.Errorf("first_user_message = %d bytes, want %d", n, auditFieldCap)
	}
}

func TestAudit_InsertFailureAndNilAuditor(t *testing.T) {
	// A failing insert is only logged — the advisory response is unaffected.
	a := &stubAuditor{err: errors.New("db down")}
	h := NewHandler(&stubVerifier{}, &stubJudge{}, time.Minute, discardLogger()).WithAuditor(a)
	if rr := post(t, h, trustedHeaders(), requestBody(t, auditRequest())); rr.Code != http.StatusOK {
		t.Fatalf("status with failing auditor = %d, want 200", rr.Code)
	}

	// No auditor configured: every path still serves normally.
	h = NewHandler(&stubVerifier{}, &stubJudge{}, time.Minute, discardLogger())
	if rr := post(t, h, trustedHeaders(), requestBody(t, auditRequest())); rr.Code != http.StatusOK {
		t.Fatalf("status without auditor = %d, want 200", rr.Code)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("abc", 2); got != "ab" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate("abc", 3); got != "abc" {
		t.Errorf("truncate exact = %q", got)
	}
	if got := truncate("", 4); got != "" {
		t.Errorf("truncate empty = %q", got)
	}
}
