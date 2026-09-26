package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	adminc "github.com/andyjmorgan/slipspace-gateway/contracts/admin"
	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	contractsres "github.com/andyjmorgan/slipspace-gateway/contracts/resilience"
	"github.com/andyjmorgan/slipspace-gateway/internal/config"
)

// TestPoliciesHandler_SameProviderTwice_DistinctNamesAndBreakerLookup proves
// the policies view names duplicate-provider arms exactly as the orchestrator
// does (Group.TargetNames) and looks their breaker state up under those
// names — the key the data plane actually writes.
func TestPoliciesHandler_SameProviderTwice_DistinctNamesAndBreakerLookup(t *testing.T) {
	t.Parallel()
	grp := contractsconfig.Group{
		Mode: contractsres.ModeLoadBalance,
		Targets: []contractsconfig.Target{
			{Provider: "openai", Alias: "gpt-4o", Weight: 90},
			{Provider: "openai", Alias: "gpt-4o-canary", Weight: 10},
			{Provider: "anthropic"},
		},
	}
	resolved := &config.ResolvedConfig{Groups: contractsconfig.GroupsConfig{"canary": grp}}
	cb := stubBreakerStates{states: map[string]string{
		"canary|openai#gpt-4o-canary": "open",
		// Under the old provider-keyed lookup this entry would have been
		// consulted for both openai arms; it must now be ignored.
		"canary|openai": "half_open",
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
	if len(got.Policies) != 1 || len(got.Policies[0].Targets) != 3 {
		t.Fatalf("shape = %+v; want one policy with three targets", got.Policies)
	}
	targets := got.Policies[0].Targets

	want := []struct {
		name, provider, state string
	}{
		{"openai#gpt-4o", "openai", "closed"},
		{"openai#gpt-4o-canary", "openai", "open"},
		{"anthropic", "anthropic", "closed"},
	}
	for i, w := range want {
		if targets[i].Name != w.name {
			t.Errorf("targets[%d].Name = %q; want %q", i, targets[i].Name, w.name)
		}
		if targets[i].Provider != w.provider {
			t.Errorf("targets[%d].Provider = %q; want the real provider %q", i, targets[i].Provider, w.provider)
		}
		if targets[i].CircuitState != w.state {
			t.Errorf("targets[%d].CircuitState = %q; want %q (looked up under the target name)", i, targets[i].CircuitState, w.state)
		}
	}
}
