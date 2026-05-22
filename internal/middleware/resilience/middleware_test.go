package resilience_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	contractsres "github.com/andyjmorgan/sluice-gateway/contracts/resilience"
	contractsrules "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	resiliencemw "github.com/andyjmorgan/sluice-gateway/internal/middleware/resilience"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
)

// stubLookup builds a PolicyLookup from a literal map for tests.
func stubLookup(policies map[string]*contractsres.ResilienceConfig) resiliencemw.PolicyLookup {
	return func(name string) *contractsres.ResilienceConfig {
		return policies[name]
	}
}

// stateCapture is the terminal handler used by the tests: records
// the MutableState the orchestrator middleware passed through so we
// can assert on whether it was cloned + mutated.
type stateCapture struct {
	state *rules.MutableState
}

func (c *stateCapture) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c.state = rules.MutableStateFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}
}

func TestHTTPHandler_NilLookup_IsPassthrough(t *testing.T) {
	t.Parallel()
	cap := &stateCapture{}
	h := resiliencemw.HTTPHandler(nil, nil, nil, cap.handler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "ha", Provider: "openai"})
	h.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))

	if cap.state == nil || cap.state.Provider != "openai" {
		t.Errorf("nil lookup must passthrough untouched; got %+v", cap.state)
	}
}

func TestHTTPHandler_EmptyPolicyRef_IsPassthrough(t *testing.T) {
	t.Parallel()
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{
		"ha": {Name: "ha"},
	})
	cap := &stateCapture{}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, cap.handler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{Provider: "openai"})
	h.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))

	if cap.state == nil || cap.state.Provider != "openai" {
		t.Errorf("empty PolicyRef must passthrough; got %+v", cap.state)
	}
}

func TestHTTPHandler_UnknownPolicyRef_IsPassthrough(t *testing.T) {
	t.Parallel()
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{
		"ha": {Name: "ha"},
	})
	cap := &stateCapture{}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, cap.handler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "never-declared", Provider: "openai"})
	h.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))

	if cap.state == nil || cap.state.Provider != "openai" {
		t.Errorf("unknown PolicyRef must degrade to passthrough; got %+v", cap.state)
	}
}

func TestHTTPHandler_ZeroTargets_IsPassthrough(t *testing.T) {
	t.Parallel()
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{
		"ha": {Name: "ha"},
	})
	cap := &stateCapture{}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, cap.handler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "ha", Provider: "openai"})
	h.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))

	if cap.state == nil || cap.state.Provider != "openai" {
		t.Errorf("zero-target policy must passthrough; got %+v", cap.state)
	}
}

func TestHTTPHandler_TargetWithoutActions_IsPassthrough(t *testing.T) {
	t.Parallel()
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{
		"ha": {
			Name: "ha",
			Targets: []contractsres.ResilienceTarget{
				{Name: "primary", Provider: "anthropic", Order: 1},
			},
		},
	})
	cap := &stateCapture{}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, cap.handler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "ha", Provider: "openai"})
	h.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))

	// PR-6 single-target degenerate: legacy fields (Provider) on the
	// target are not applied; the rules engine already drove the
	// destination. Multi-target failover (PR-7+) will read those.
	if cap.state == nil || cap.state.Provider != "openai" {
		t.Errorf("target with no Actions should leave state untouched; got %+v", cap.state)
	}
}

func TestHTTPHandler_AppliesTargetActions(t *testing.T) {
	t.Parallel()
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{
		"ha": {
			Name: "ha",
			Targets: []contractsres.ResilienceTarget{
				{
					Name:     "anthropic-fallback",
					Provider: "anthropic",
					Order:    1,
					Actions: []contractsrules.Action{
						&contractsrules.ChangeProviderAction{NewProvider: "anthropic"},
					},
				},
			},
		},
	})
	cap := &stateCapture{}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, cap.handler())

	original := &rules.MutableState{PolicyRef: "ha", Provider: "openai"}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := rules.WithMutableState(req.Context(), original)
	h.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))

	if cap.state == nil {
		t.Fatal("downstream saw nil state")
	}
	if cap.state.Provider != "anthropic" {
		t.Errorf("downstream Provider = %q; want anthropic (target action applied)", cap.state.Provider)
	}
	// Critical: the original state on the test's heap must be
	// untouched — the orchestrator applied actions on a clone.
	if original.Provider != "openai" {
		t.Errorf("original state mutated: Provider = %q; want openai", original.Provider)
	}
}

func TestHTTPHandler_StateCloneIsIndependent(t *testing.T) {
	t.Parallel()
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{
		"ha": {
			Name: "ha",
			Targets: []contractsres.ResilienceTarget{
				{
					Name:     "primary",
					Provider: "anthropic",
					Order:    1,
					Actions: []contractsrules.Action{
						&contractsrules.SetHeaderAction{HeaderName: "X-Forced", HeaderAction: contractsrules.HeaderSet, HeaderValue: "yes"},
					},
				},
			},
		},
	})
	cap := &stateCapture{}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, cap.handler())

	original := &rules.MutableState{PolicyRef: "ha", OutgoingHeaders: http.Header{"X-From-Rules": []string{"prior"}}}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := rules.WithMutableState(req.Context(), original)
	h.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))

	if cap.state.OutgoingHeaders.Get("X-From-Rules") != "prior" {
		t.Errorf("clone lost rules-applied header: %+v", cap.state.OutgoingHeaders)
	}
	if cap.state.OutgoingHeaders.Get("X-Forced") != "yes" {
		t.Errorf("clone missing target-applied header: %+v", cap.state.OutgoingHeaders)
	}
	if original.OutgoingHeaders.Get("X-Forced") != "" {
		t.Errorf("original mutated through clone: %+v", original.OutgoingHeaders)
	}
}

func TestHTTPHandler_ApplyActionError_Returns500(t *testing.T) {
	t.Parallel()
	// An empty changeProvider triggers errEmptyValue at apply time —
	// the middleware must surface that as a 500 to the client.
	lookup := stubLookup(map[string]*contractsres.ResilienceConfig{
		"ha": {
			Name: "ha",
			Targets: []contractsres.ResilienceTarget{
				{
					Name:     "broken",
					Provider: "anthropic",
					Order:    1,
					Actions: []contractsrules.Action{
						&contractsrules.ChangeProviderAction{NewProvider: ""},
					},
				},
			},
		},
	})
	cap := &stateCapture{}
	h := resiliencemw.HTTPHandler(lookup, nil, nil, cap.handler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := rules.WithMutableState(req.Context(), &rules.MutableState{PolicyRef: "ha"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d; want 500 on apply failure", rec.Code)
	}
	if cap.state != nil {
		t.Errorf("downstream handler was called despite apply failure: %+v", cap.state)
	}
}

func TestHTTPHandler_PanicsOnNilNext(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil next handler")
		}
	}()
	_ = resiliencemw.HTTPHandler(stubLookup(nil), nil, nil, nil)
}
