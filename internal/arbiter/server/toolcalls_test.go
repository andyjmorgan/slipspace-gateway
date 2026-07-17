package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	adminc "github.com/andyjmorgan/slipspace-gateway/contracts/admin"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
)

func TestToolCalls_List(t *testing.T) {
	called := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	q := &fakeQueries{
		toolCalls: []store.ToolCall{{
			ToolCallID: "toolu_1", ToolName: "Skill", SessionID: "s1",
			Arguments: []byte(`{"skill":"ship"}`), ArgumentsChars: 16,
			Result: "done", ResultChars: 4, CalledAt: &called, RespondedAt: &called,
		}},
		toolCallsNext: "cur2",
	}
	h := newQueryServer(t, q)
	resp := get(t, h, "/api/v1/tool-calls?name=Skill&status=resolved&limit=10", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	var body struct {
		ToolCalls  []adminc.ToolCallEntry `json:"tool_calls"`
		NextCursor string                 `json:"next_cursor"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.ToolCalls) != 1 || body.NextCursor != "cur2" {
		t.Fatalf("body = %+v", body)
	}
	e := body.ToolCalls[0]
	if e.ToolName != "Skill" || e.Status != "resolved" || string(e.Arguments) != `{"skill":"ship"}` {
		t.Errorf("entry = %+v", e)
	}
	// The handler plumbed every filter through to the store params, including
	// the plural slice the query builder reads.
	if p := q.lastToolParams; p.Name != "Skill" || p.Status != "resolved" || p.Limit != 10 ||
		len(p.Names) != 1 || p.Names[0] != "Skill" {
		t.Errorf("params = %+v", p)
	}
}

func TestToolCalls_RepeatedCategoricalParams(t *testing.T) {
	// The categorical dimensions are repeatable — every value reaches the plural
	// store params (OR within the dimension).
	q := &fakeQueries{}
	h := newQueryServer(t, q)
	resp := get(t, h, "/api/v1/tool-calls?name=Read&name=Write&provider=openai&provider=anthropic"+
		"&configuration=prod&configuration=staging&protocol=messages&model=m1&model=m2", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	p := q.lastToolParams
	if len(p.Names) != 2 || p.Names[1] != "Write" || len(p.Providers) != 2 ||
		len(p.Configurations) != 2 || len(p.Protocols) != 1 || len(p.Models) != 2 {
		t.Errorf("plural params not plumbed: %+v", p)
	}
}

func TestToolCalls_PendingStatusDerived(t *testing.T) {
	q := &fakeQueries{toolCalls: []store.ToolCall{{ToolCallID: "toolu_2", ToolName: "Read"}}}
	h := newQueryServer(t, q)
	resp := get(t, h, "/api/v1/tool-calls", true)
	var body struct {
		ToolCalls []adminc.ToolCallEntry `json:"tool_calls"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.ToolCalls[0].Status != "pending" {
		t.Errorf("status = %q, want pending", body.ToolCalls[0].Status)
	}
}

func TestToolCalls_BadWindowAndErrors(t *testing.T) {
	h := newQueryServer(t, &fakeQueries{})
	if resp := get(t, h, "/api/v1/tool-calls?from=nope", true); resp.Code != http.StatusBadRequest {
		t.Errorf("bad from: status = %d, want 400", resp.Code)
	}
	// Invalid cursor maps to 400.
	bad := &fakeQueries{toolCallsErr: store.ErrInvalidCursor}
	if resp := get(t, newQueryServer(t, bad), "/api/v1/tool-calls?cursor=x", true); resp.Code != http.StatusBadRequest {
		t.Errorf("bad cursor: status = %d, want 400", resp.Code)
	}
	// A generic store error is a 500.
	boom := &fakeQueries{toolCallsErr: errors.New("db")}
	if resp := get(t, newQueryServer(t, boom), "/api/v1/tool-calls", true); resp.Code != http.StatusInternalServerError {
		t.Errorf("store err: status = %d, want 500", resp.Code)
	}
}

func TestToolCall_GetByID(t *testing.T) {
	q := &fakeQueries{toolCall: store.ToolCall{ToolCallID: "toolu_9", ToolName: "Bash"}}
	h := newQueryServer(t, q)
	resp := get(t, h, "/api/v1/tool-calls/toolu_9", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	var e adminc.ToolCallEntry
	_ = json.NewDecoder(resp.Body).Decode(&e)
	if e.ToolCallID != "toolu_9" || e.ToolName != "Bash" {
		t.Errorf("entry = %+v", e)
	}
}

func TestToolCall_NotFoundAndError(t *testing.T) {
	nf := &fakeQueries{toolCallErr: store.ErrToolCallNotFound}
	if resp := get(t, newQueryServer(t, nf), "/api/v1/tool-calls/missing", true); resp.Code != http.StatusNotFound {
		t.Errorf("not found: status = %d, want 404", resp.Code)
	}
	boom := &fakeQueries{toolCallErr: errors.New("db")}
	if resp := get(t, newQueryServer(t, boom), "/api/v1/tool-calls/x", true); resp.Code != http.StatusInternalServerError {
		t.Errorf("store err: status = %d, want 500", resp.Code)
	}
}

func TestToolNames_Facet(t *testing.T) {
	q := &fakeQueries{toolNames: []string{"Bash", "Read", "Skill"}}
	h := newQueryServer(t, q)
	resp := get(t, h, "/api/v1/tool-calls/facets", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	var body struct {
		ToolNames []string `json:"tool_names"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.ToolNames) != 3 || body.ToolNames[2] != "Skill" {
		t.Errorf("tool_names = %v", body.ToolNames)
	}
	// nil names serialize as [] (never null) so the dropdown decodes cleanly.
	empty := &fakeQueries{}
	r2 := get(t, newQueryServer(t, empty), "/api/v1/tool-calls/facets", true)
	var b2 struct {
		ToolNames []string `json:"tool_names"`
	}
	_ = json.NewDecoder(r2.Body).Decode(&b2)
	if b2.ToolNames == nil {
		t.Error("tool_names = null, want []")
	}
}

func TestToolNames_Error(t *testing.T) {
	q := &fakeQueries{toolNamesErr: errors.New("db")}
	if resp := get(t, newQueryServer(t, q), "/api/v1/tool-calls/facets", true); resp.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.Code)
	}
}

func TestToolCalls_RequiresAuth(t *testing.T) {
	h := newQueryServer(t, &fakeQueries{})
	if resp := get(t, h, "/api/v1/tool-calls", false); resp.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.Code)
	}
}
