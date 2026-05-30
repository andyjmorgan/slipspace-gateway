package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	contractsres "github.com/andyjmorgan/sluice-gateway/contracts/resilience"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

// stubBreakerStates is the CircuitBreakerStateSource the tests pass to
// PoliciesHandler. Backed by a map so each test can preload whatever
// (policy, target) → state combos it cares about; unset pairs report
// "closed" (matches the real store's behaviour for never-observed
// pairs).
type stubBreakerStates struct {
	states map[string]string
}

func (s stubBreakerStates) State(policy, target string) string {
	if v, ok := s.states[policy+"|"+target]; ok {
		return v
	}
	return "closed"
}

func TestPoliciesHandler_NilConfigReturns503(t *testing.T) {
	t.Parallel()
	h := PoliciesHandler(nil, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/policies", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503", rec.Code)
	}
}

func TestPoliciesHandler_EmptyConfig(t *testing.T) {
	t.Parallel()
	resolved := &config.ResolvedConfigV2{}
	h := PoliciesHandler(config.NewStore(resolved), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/policies", nil))

	var got adminc.PoliciesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Policies) != 0 {
		t.Errorf("policies = %d; want 0", len(got.Policies))
	}
	if got.Pod == "" {
		t.Errorf("Pod label empty; expected hostname or 'unknown'")
	}
}

func TestPoliciesHandler_ProjectsTargetsAndCircuitState(t *testing.T) {
	t.Parallel()
	grp := contractsconfig.Group{
		Mode:               contractsres.ModeFailover,
		FailureStatusCodes: []int{503},
		Targets: []contractsconfig.Target{
			{Backend: "primary"},
			{Backend: "backup"},
		},
	}
	resolved := &config.ResolvedConfigV2{Groups: contractsconfig.GroupsConfig{"ha": grp}}
	cb := stubBreakerStates{states: map[string]string{
		"ha|primary": "open",
		// "ha|backup" intentionally absent → "closed"
	}}

	h := PoliciesHandler(config.NewStore(resolved), cb)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/policies", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}

	var got adminc.PoliciesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Policies) != 1 {
		t.Fatalf("policies = %d; want 1", len(got.Policies))
	}
	p := got.Policies[0]
	if p.Name != "ha" || p.Mode != "failover" {
		t.Errorf("policy = %+v; want ha/failover", p)
	}
	if len(p.Targets) != 2 {
		t.Fatalf("targets = %d; want 2", len(p.Targets))
	}
	if p.Targets[0].Name != "primary" || p.Targets[0].CircuitState != "open" {
		t.Errorf("targets[0] = %+v; want primary/open", p.Targets[0])
	}
	if p.Targets[1].Name != "backup" || p.Targets[1].CircuitState != "closed" {
		t.Errorf("targets[1] = %+v; want backup/closed", p.Targets[1])
	}
}

func TestPoliciesHandler_NilBreakerSource_AllUnknown(t *testing.T) {
	t.Parallel()
	grp := contractsconfig.Group{
		Mode: contractsres.ModeFailover,
		Targets: []contractsconfig.Target{
			{Backend: "primary"},
		},
	}
	resolved := &config.ResolvedConfigV2{Groups: contractsconfig.GroupsConfig{"ha": grp}}

	h := PoliciesHandler(config.NewStore(resolved), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/policies", nil))

	var got adminc.PoliciesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Policies[0].Targets[0].CircuitState != "unknown" {
		t.Errorf("CircuitState = %q; want unknown when no source", got.Policies[0].Targets[0].CircuitState)
	}
}

func TestPoliciesHandler_PolicyCBEnabledFlag(t *testing.T) {
	t.Parallel()
	grp := contractsconfig.Group{
		Mode: contractsres.ModeFailover,
		CircuitBreaker: &contractsres.CircuitBreakerConfig{
			Enabled:                  true,
			FailureThreshold:         3,
			SamplingDurationSeconds:  60,
			CooldownSeconds:          30,
			HalfOpenSuccessThreshold: 2,
		},
		Targets: []contractsconfig.Target{
			{Backend: "primary"},
		},
	}
	resolved := &config.ResolvedConfigV2{Groups: contractsconfig.GroupsConfig{"ha": grp}}

	h := PoliciesHandler(config.NewStore(resolved), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/policies", nil))

	var got adminc.PoliciesResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if !got.Policies[0].CircuitBreakerEnabled {
		t.Errorf("CircuitBreakerEnabled = false; want true")
	}
}
