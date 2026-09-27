package resilience_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	contractsres "github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
	resiliencemw "github.com/andyjmorgan/slipspace-gateway/internal/middleware/resilience"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/rules"
)

// captureState is a terminal handler that records the per-attempt
// MutableState the orchestrator handed down, in order.
type captureState struct {
	seen   []*rules.MutableState
	status int
}

func (c *captureState) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.seen = append(c.seen, rules.MutableStateFromContext(r.Context()))
	w.WriteHeader(c.status)
}

// TestAttemptState_CarriesTargetPathAndQuery pins issue #409 at the
// orchestrator: every mode's per-attempt state must carry the target's
// Path / Query so the final handler can re-apply them via
// selection.ResolveTarget — and a target with none must leave them empty
// (so the provider default applies), even when the baseline state came
// from an earlier attempt that had them.
func TestAttemptState_CarriesTargetPathAndQuery(t *testing.T) {
	t.Parallel()

	t.Run("single target (ModeNone)", func(t *testing.T) {
		t.Parallel()
		pol := &contractsres.ResilienceConfig{
			Name: "binding:azure", Mode: contractsres.ModeNone,
			Targets: []contractsres.ResilienceTarget{{
				Name: "azure", Provider: "azure",
				Path:    "/openai/deployments/gpt4o/chat/completions",
				Query:   map[string]string{"api-version": "2025-01-01"},
				Actions: changeTo("azure"),
			}},
		}
		lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"binding:azure": pol})
		next := &captureState{status: http.StatusOK}
		h := resiliencemw.HTTPHandler(lookup, nil, nil, next)

		h.ServeHTTP(httptest.NewRecorder(), requestWithPolicy("binding:azure"))

		if len(next.seen) != 1 {
			t.Fatalf("attempts = %d, want 1", len(next.seen))
		}
		st := next.seen[0]
		if st.TargetPath != "/openai/deployments/gpt4o/chat/completions" {
			t.Errorf("TargetPath = %q", st.TargetPath)
		}
		if st.TargetQuery["api-version"] != "2025-01-01" {
			t.Errorf("TargetQuery = %v", st.TargetQuery)
		}
		if st.Provider != "azure" {
			t.Errorf("Provider = %q", st.Provider)
		}
	})

	t.Run("failover: each attempt gets its own target's overrides", func(t *testing.T) {
		t.Parallel()
		pol := failoverPolicy(
			contractsres.ResilienceTarget{Name: "primary", Provider: "openai", Order: 1,
				Path: "/primary/path", Query: map[string]string{"v": "primary"}, Actions: changeTo("openai")},
			contractsres.ResilienceTarget{Name: "backup", Provider: "anthropic", Order: 2, Actions: changeTo("anthropic")},
		)
		lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"ha": pol})
		next := &mockNext{outcomes: []attemptOutcome{
			{status: http.StatusServiceUnavailable},
			{status: http.StatusOK, body: `{}`},
		}}
		// Wrap mockNext to also capture the states.
		var states []*rules.MutableState
		wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			states = append(states, rules.MutableStateFromContext(r.Context()))
			next.ServeHTTP(w, r)
		})
		h := resiliencemw.HTTPHandler(lookup, nil, nil, wrapped)

		h.ServeHTTP(httptest.NewRecorder(), requestWithPolicy("ha"))

		if len(states) != 2 {
			t.Fatalf("attempts = %d, want 2", len(states))
		}
		if states[0].TargetPath != "/primary/path" || states[0].TargetQuery["v"] != "primary" {
			t.Errorf("attempt 1 overrides = %q %v", states[0].TargetPath, states[0].TargetQuery)
		}
		if states[1].TargetPath != "" || states[1].TargetQuery != nil {
			t.Errorf("attempt 2 must carry no overrides (provider default), got %q %v", states[1].TargetPath, states[1].TargetQuery)
		}
	})

	t.Run("baseline overrides never leak into an attempt", func(t *testing.T) {
		t.Parallel()
		pol := &contractsres.ResilienceConfig{
			Name: "single", Mode: contractsres.ModeNone,
			Targets: []contractsres.ResilienceTarget{{Name: "openai", Provider: "openai", Actions: changeTo("openai")}},
		}
		lookup := stubLookup(map[string]*contractsres.ResilienceConfig{"single": pol})
		next := &captureState{status: http.StatusOK}
		h := resiliencemw.HTTPHandler(lookup, nil, nil, next)

		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		baseline := &rules.MutableState{PolicyRef: "single", TargetPath: "/stale", TargetQuery: map[string]string{"stale": "1"}}
		h.ServeHTTP(httptest.NewRecorder(), req.WithContext(rules.WithMutableState(req.Context(), baseline)))

		if len(next.seen) != 1 {
			t.Fatalf("attempts = %d, want 1", len(next.seen))
		}
		if next.seen[0].TargetPath != "" || next.seen[0].TargetQuery != nil {
			t.Errorf("stale baseline overrides leaked: %q %v", next.seen[0].TargetPath, next.seen[0].TargetQuery)
		}
	})
}
