package resilience_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	contractsres "github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
	contractsrules "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/httperr"
	resiliencemw "github.com/andyjmorgan/slipspace-gateway/internal/middleware/resilience"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/rules"
)

// decodeJSONError asserts the orchestrator's terminal rejection is the
// documented httperr JSON shape (issue #554) and returns the decoded body.
func decodeJSONError(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) httperr.Body {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, wantStatus, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json (body=%s)", ct, rec.Body.String())
	}
	var body httperr.Body
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not the httperr JSON shape: %v (%s)", err, rec.Body.String())
	}
	return body
}

func requestWithPolicy(name string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return req.WithContext(rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: name}))
}

func TestFailover_Exhausted_WritesJSONError_AllFailed(t *testing.T) {
	t.Parallel()
	pol := failoverPolicy(
		contractsres.ResilienceTarget{Name: "openai", Provider: "openai", Order: 1, Actions: changeTo("openai")},
		contractsres.ResilienceTarget{Name: "anthropic", Provider: "anthropic", Order: 2, Actions: changeTo("anthropic")},
	)
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"ha": pol})
	next := &mockNext{outcomes: []attemptOutcome{
		{status: http.StatusServiceUnavailable},
		{status: http.StatusBadGateway},
	}}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, requestWithPolicy("ha"))

	body := decodeJSONError(t, rec, http.StatusBadGateway)
	if body.Error != "all_failed" {
		t.Fatalf("error code = %q, want all_failed", body.Error)
	}
}

func TestFailover_AllCBOpen_WritesJSONError_AllOpen(t *testing.T) {
	t.Parallel()
	store := resiliencemw.NewInMemoryBreakerStore(nil)
	cb := &contractsres.CircuitBreakerConfig{Enabled: true, FailureThreshold: 1, MinimumThroughput: 1, CooldownSeconds: 60}
	store.RecordFailure("ha", "t1", cb)
	store.RecordFailure("ha", "t2", cb)
	pol := &contractsres.ResilienceConfig{
		Name: "ha", Mode: contractsres.ModeFailover, CircuitBreaker: cb,
		Targets: []contractsres.ResilienceTarget{
			{Name: "t1", Provider: "openai", Order: 1, Actions: changeTo("openai")},
			{Name: "t2", Provider: "anthropic", Order: 2, Actions: changeTo("anthropic")},
		},
	}
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"ha": pol})
	h := resiliencemw.HTTPHandler(lookup, store, nil, &mockNext{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, requestWithPolicy("ha"))

	body := decodeJSONError(t, rec, http.StatusServiceUnavailable)
	if body.Error != "all_open" {
		t.Fatalf("error code = %q, want all_open", body.Error)
	}
}

func TestLoadBalance_Exhausted_WritesJSONError_AllFailed(t *testing.T) {
	t.Parallel()
	pol := &contractsres.ResilienceConfig{
		Name: "lb", Mode: contractsres.ModeLoadBalance,
		FailureStatusCodes: []int{500, 502, 503, 504},
		Targets: []contractsres.ResilienceTarget{
			{Name: "openai", Provider: "openai", Weight: 1, Actions: changeTo("openai")},
			{Name: "anthropic", Provider: "anthropic", Weight: 1, Actions: changeTo("anthropic")},
		},
	}
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"lb": pol})
	next := &mockNext{outcomes: []attemptOutcome{
		{status: 0, err: errors.New("dial refused")},
		{status: 0, err: errors.New("dial refused")},
	}}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, requestWithPolicy("lb"))

	// Every attempt was a transport error, so the orchestrator falls back to 502.
	body := decodeJSONError(t, rec, http.StatusBadGateway)
	if body.Error != "all_failed" {
		t.Fatalf("error code = %q, want all_failed", body.Error)
	}
}

func TestSingleTarget_ActionError_WritesJSONError(t *testing.T) {
	t.Parallel()
	// An empty changeProvider fails ApplyAction on the single-target path.
	pol := &contractsres.ResilienceConfig{
		Name: "single", Mode: contractsres.ModeNone,
		Targets: []contractsres.ResilienceTarget{{
			Name: "broken", Provider: "openai",
			Actions: []contractsrules.Action{&contractsrules.ChangeProviderAction{NewProvider: ""}},
		}},
	}
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"single": pol})
	h := resiliencemw.HTTPHandler(lookup, nil, nil, &mockNext{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, requestWithPolicy("single"))

	body := decodeJSONError(t, rec, http.StatusInternalServerError)
	if body.Error != "target_action_failed" {
		t.Fatalf("error code = %q, want target_action_failed", body.Error)
	}
}

func TestFailover_ActionError_WritesJSONError(t *testing.T) {
	t.Parallel()
	pol := failoverPolicy(
		contractsres.ResilienceTarget{Name: "broken", Provider: "openai", Order: 1,
			Actions: []contractsrules.Action{&contractsrules.ChangeProviderAction{NewProvider: ""}}},
	)
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"ha": pol})
	h := resiliencemw.HTTPHandler(lookup, nil, nil, &mockNext{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, requestWithPolicy("ha"))

	body := decodeJSONError(t, rec, http.StatusInternalServerError)
	if body.Error != "target_action_failed" {
		t.Fatalf("error code = %q, want target_action_failed", body.Error)
	}
}

func TestLoadBalance_ActionError_WritesJSONError(t *testing.T) {
	t.Parallel()
	pol := &contractsres.ResilienceConfig{
		Name: "lb", Mode: contractsres.ModeLoadBalance,
		Targets: []contractsres.ResilienceTarget{{
			Name: "broken", Provider: "openai", Weight: 1,
			Actions: []contractsrules.Action{&contractsrules.ChangeProviderAction{NewProvider: ""}},
		}},
	}
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"lb": pol})
	h := resiliencemw.HTTPHandler(lookup, nil, nil, &mockNext{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, requestWithPolicy("lb"))

	body := decodeJSONError(t, rec, http.StatusInternalServerError)
	if body.Error != "target_action_failed" {
		t.Fatalf("error code = %q, want target_action_failed", body.Error)
	}
}
