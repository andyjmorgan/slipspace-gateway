package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/contracts/resilience"
	rulescontract "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

// fixtureResolved builds a small but representative ResolvedConfig with the
// indexes populated. Used by every handler test below to avoid duplicating
// the wiring.
func fixtureResolved(t *testing.T) *config.ResolvedConfig {
	t.Helper()
	resilienceName := "fast-fail"
	rc := &config.ResolvedConfig{
		Providers: contractsconfig.ProvidersConfig{
			"openai": contractsconfig.Provider{
				BaseURL: "https://api.openai.com",
				Endpoints: map[string]contractsconfig.Endpoint{
					"chat_completions": {
						Path:          "/v1/chat/completions",
						Method:        []string{"POST"},
						AcceptedPaths: []string{"/v1/chat/completions"},
						RequestKind:   "openai.chat",
					},
					"models": {
						Path:          "/v1/models",
						Method:        []string{"GET"},
						AcceptedPaths: []string{"/v1/models"},
						RequestKind:   "openai.models",
					},
				},
			},
			"anthropic": contractsconfig.Provider{
				BaseURL:        "https://api.anthropic.com",
				Prefix:         "anthropic",
				PrefixRequired: true,
				Endpoints: map[string]contractsconfig.Endpoint{
					"messages": {
						Path:           "/v1/messages",
						Method:         []string{"POST"},
						AcceptedPaths:  []string{"/v1/messages"},
						RequestKind:    "anthropic.messages",
						PrefixOptional: true,
					},
				},
			},
		},
		Configurations: contractsconfig.ConfigurationsConfig{
			"dev": contractsconfig.Configuration{
				UpstreamCredentials: map[string]string{
					"openai":    "sk-very-secret-openai-key-abcd1234",
					"anthropic": "sk-ant-supersecret-mnop5678",
				},
				RuleNames:      []string{"only-openai"},
				ResilienceName: &resilienceName,
				Tags:           map[string]string{"tier": "dev"},
			},
			"prod": contractsconfig.Configuration{
				UpstreamCredentials: map[string]string{
					"openai": "sk-prod-tighten-this-wxyz9876",
				},
			},
		},
		APIKeys: contractsconfig.APIKeysConfig{
			{Secret: "sk_dev_first_key_1234", Name: "dev-1", Configuration: "dev", Enabled: true},   //nolint:gosec // synthetic test fixture, not a real credential
			{Secret: "sk_dev_second_key_5678", Name: "dev-2", Configuration: "dev", Enabled: false}, //nolint:gosec // synthetic test fixture, not a real credential
			{Secret: "sk_prod_xxxx_9999", Name: "prod-1", Configuration: "prod", Enabled: true},     //nolint:gosec // synthetic test fixture, not a real credential
		},
		Rules: []rulescontract.RuleContract{
			{
				Name: "only-openai",
				Condition: &rulescontract.ProviderCondition{
					Type:             "provider",
					Operator:         rulescontract.EnumEquals,
					ExpectedProvider: "openai",
				},
				Actions: []rulescontract.Action{
					&rulescontract.SetHeaderAction{Type: "setHeader", HeaderName: "X-Demo", HeaderAction: rulescontract.HeaderSet, HeaderValue: "ok"},
				},
				Behavior: rulescontract.BehaviorContinue,
			},
		},
		ResiliencePolicies: []resilience.ResilienceConfig{
			{Name: "fast-fail", Mode: resilience.ModeNone, TimeoutSeconds: 5},
		},
	}
	if err := rc.Validate(); err != nil {
		t.Fatalf("fixture: validate: %v", err)
	}
	// Hand-populate the indexes the handlers read. Mirrors the pattern in
	// internal/routing/router_test.go::buildConfig — kept inline rather
	// than exporting buildIndexes from the config package so the handler
	// tests stay decoupled from loader internals.
	populateIndexes(rc)
	return rc
}

// populateIndexes mirrors config.buildIndexes for the handful of indexes
// the admin handlers read. Kept local to the test so a future loader
// refactor does not break these tests through the back door.
func populateIndexes(rc *config.ResolvedConfig) {
	rc.SecretIndex = make(map[string]*contractsconfig.APIKey, len(rc.APIKeys))
	for i := range rc.APIKeys {
		rc.SecretIndex[rc.APIKeys[i].Secret] = &rc.APIKeys[i]
	}
	rc.ConfigurationIndex = make(map[string]*contractsconfig.Configuration, len(rc.Configurations))
	for name, cfg := range rc.Configurations {
		entry := cfg
		rc.ConfigurationIndex[name] = &entry
	}
	rc.RuleIndex = make(map[string]*rulescontract.RuleContract, len(rc.Rules))
	for i := range rc.Rules {
		rc.RuleIndex[rc.Rules[i].Name] = &rc.Rules[i]
	}
	rc.ResilienceIndex = make(map[string]*resilience.ResilienceConfig, len(rc.ResiliencePolicies))
	for i := range rc.ResiliencePolicies {
		rc.ResilienceIndex[rc.ResiliencePolicies[i].Name] = &rc.ResiliencePolicies[i]
	}
	rc.PerConfigurationRules = make(map[string][]*rulescontract.RuleContract, len(rc.Configurations))
	for name, cfg := range rc.Configurations {
		if len(cfg.RuleNames) == 0 {
			continue
		}
		attached := make([]*rulescontract.RuleContract, 0, len(cfg.RuleNames))
		for _, ruleName := range cfg.RuleNames {
			if rule, ok := rc.RuleIndex[ruleName]; ok {
				attached = append(attached, rule)
			}
		}
		rc.PerConfigurationRules[name] = attached
	}
	rc.RouteIndex = make(map[string]config.Route)
	for providerName, p := range rc.Providers {
		for endpointName, e := range p.Endpoints {
			bareEmits := !p.PrefixRequired || e.PrefixOptional
			for _, ap := range e.AcceptedPaths {
				if p.Prefix != "" {
					rc.RouteIndex["/"+p.Prefix+ap] = config.Route{Provider: providerName, Endpoint: endpointName}
				}
				if bareEmits {
					rc.RouteIndex[ap] = config.Route{Provider: providerName, Endpoint: endpointName}
				}
			}
		}
	}
}

func TestConfigurationsListHandler_HappyPath(t *testing.T) {
	rc := fixtureResolved(t)
	h := admin.ConfigurationsListHandler(config.NewStore(rc))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/configurations", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got []admin.ConfigurationSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (dev, prod)", len(got))
	}
	if got[0].Name != "dev" || got[1].Name != "prod" {
		t.Errorf("order = [%s, %s], want [dev, prod] (sorted)", got[0].Name, got[1].Name)
	}
	if got[0].KeyCount != 2 {
		t.Errorf("dev KeyCount = %d, want 2", got[0].KeyCount)
	}
	if got[0].RuleCount != 1 {
		t.Errorf("dev RuleCount = %d, want 1", got[0].RuleCount)
	}
}

func TestConfigurationsListHandler_NilResolved503(t *testing.T) {
	h := admin.ConfigurationsListHandler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/configurations", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestConfigurationDetailHandler_RedactsCredentialsAndKeys(t *testing.T) {
	rc := fixtureResolved(t)
	h := admin.ConfigurationDetailHandler(config.NewStore(rc))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/configurations/dev", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Hard rule: no upstream credential or api-key secret leaves the
	// handler. If any plaintext token appears in the body the redaction
	// boundary has been bypassed.
	for _, leaked := range []string{
		"sk-very-secret-openai-key-abcd1234",
		"sk-ant-supersecret-mnop5678",
		"sk_dev_first_key_1234",
		"sk_dev_second_key_5678",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("body leaked secret %q: %s", leaked, body)
		}
	}
	var got admin.ConfigurationDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "dev" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.UpstreamCredentials["openai"].Last4 != "1234" {
		t.Errorf("openai last4 = %q, want 1234", got.UpstreamCredentials["openai"].Last4)
	}
	if got.UpstreamCredentials["openai"].Length != len("sk-very-secret-openai-key-abcd1234") {
		t.Errorf("openai length = %d", got.UpstreamCredentials["openai"].Length)
	}
	if got.UpstreamCredentials["anthropic"].Last4 != "5678" {
		t.Errorf("anthropic last4 = %q", got.UpstreamCredentials["anthropic"].Last4)
	}
	if len(got.APIKeys) != 2 {
		t.Fatalf("APIKeys len = %d, want 2 (only the keys belonging to dev)", len(got.APIKeys))
	}
	if got.APIKeys[0].Secret.Last4 != "1234" {
		t.Errorf("api key 0 last4 = %q", got.APIKeys[0].Secret.Last4)
	}
	if len(got.Rules) != 1 || got.Rules[0].Name != "only-openai" {
		t.Errorf("Rules = %+v", got.Rules)
	}
	if got.Rules[0].ConditionSummary == "" {
		t.Errorf("ConditionSummary empty for only-openai")
	}
	if len(got.Rules[0].ActionTypes) != 1 || got.Rules[0].ActionTypes[0] != "setHeader" {
		t.Errorf("ActionTypes = %v", got.Rules[0].ActionTypes)
	}
}

func TestConfigurationDetailHandler_NotFound(t *testing.T) {
	rc := fixtureResolved(t)
	h := admin.ConfigurationDetailHandler(config.NewStore(rc))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/configurations/does-not-exist", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestRulesListHandler_AlphabeticalAndIncludesUsedBy(t *testing.T) {
	rc := fixtureResolved(t)
	h := admin.RulesListHandler(config.NewStore(rc))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/rules", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []admin.RuleSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Name != "only-openai" {
		t.Errorf("got %+v", got[0])
	}
	if len(got[0].UsedBy) != 1 || got[0].UsedBy[0] != "dev" {
		t.Errorf("UsedBy = %v, want [dev]", got[0].UsedBy)
	}
}

func TestRuleDetailHandler_HappyPath(t *testing.T) {
	rc := fixtureResolved(t)
	h := admin.RuleDetailHandler(config.NewStore(rc))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/rules/only-openai", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// The Condition/Actions fields are interfaces — Go cannot
	// re-unmarshal them without a dispatcher, but the SPA consumes the
	// payload as untyped JSON, so we deserialise via a JSON-friendly
	// shadow struct here.
	var got struct {
		Name      string            `json:"name"`
		Behavior  string            `json:"behavior,omitempty"`
		Condition json.RawMessage   `json:"condition"`
		Actions   []json.RawMessage `json:"actions"`
		UsedBy    []string          `json:"used_by"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "only-openai" {
		t.Errorf("Name = %q", got.Name)
	}
	if len(got.UsedBy) != 1 || got.UsedBy[0] != "dev" {
		t.Errorf("UsedBy = %v", got.UsedBy)
	}
	if !strings.Contains(string(got.Condition), `"provider"`) {
		t.Errorf("Condition body missing provider type: %s", got.Condition)
	}
	if len(got.Actions) != 1 {
		t.Errorf("Actions len = %d", len(got.Actions))
	}
}

func TestRuleDetailHandler_NotFound(t *testing.T) {
	rc := fixtureResolved(t)
	h := admin.RuleDetailHandler(config.NewStore(rc))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/rules/missing", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestProvidersListHandler_HappyPath(t *testing.T) {
	rc := fixtureResolved(t)
	h := admin.ProvidersListHandler(config.NewStore(rc))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/providers", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []admin.ProviderSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Name != "anthropic" || got[1].Name != "openai" {
		t.Errorf("order = [%s, %s], want sorted", got[0].Name, got[1].Name)
	}
	if !got[0].PrefixRequired {
		t.Errorf("anthropic PrefixRequired = false, want true")
	}
}

func TestProviderDetailHandler_IncludesPerEndpointOverrides(t *testing.T) {
	rc := fixtureResolved(t)
	h := admin.ProviderDetailHandler(config.NewStore(rc))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/providers/anthropic", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got admin.ProviderDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "anthropic" {
		t.Errorf("Name = %q", got.Name)
	}
	if len(got.Endpoints) != 1 || got.Endpoints[0].Name != "messages" {
		t.Errorf("Endpoints = %+v", got.Endpoints)
	}
	if !got.Endpoints[0].PrefixOptional {
		t.Errorf("messages PrefixOptional = false, want true")
	}
}

// TestAPIKeysRevealHandler_ReturnsPlaintextForExactMatch is the reveal
// endpoint's happy path. The plaintext secret comes back unredacted —
// that's the entire point of this endpoint — but only when the composite
// (configuration, name) lookup matches exactly. Lives behind the same
// Basic auth as the rest of the admin tree.
func TestAPIKeysRevealHandler_ReturnsPlaintextForExactMatch(t *testing.T) {
	rc := fixtureResolved(t)
	h := admin.APIKeysRevealHandler(config.NewStore(rc))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/api-keys/reveal?configuration=dev&name=dev-1", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got admin.APIKeyReveal
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Secret != "sk_dev_first_key_1234" {
		t.Errorf("Secret = %q, want plaintext", got.Secret)
	}
	if got.Configuration != "dev" || got.Name != "dev-1" || !got.Enabled {
		t.Errorf("identity wrong: %+v", got)
	}
}

func TestAPIKeysRevealHandler_MissingParamsAndMisses(t *testing.T) {
	rc := fixtureResolved(t)
	h := admin.APIKeysRevealHandler(config.NewStore(rc))
	cases := []struct {
		name     string
		path     string
		wantCode int
	}{
		{"no-params", "/api/v1/config/api-keys/reveal", http.StatusBadRequest},
		{"missing-name", "/api/v1/config/api-keys/reveal?configuration=dev", http.StatusBadRequest},
		{"missing-config", "/api/v1/config/api-keys/reveal?name=dev-1", http.StatusBadRequest},
		{"wrong-config", "/api/v1/config/api-keys/reveal?configuration=nope&name=dev-1", http.StatusNotFound},
		{"wrong-name", "/api/v1/config/api-keys/reveal?configuration=dev&name=ghost", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}

// TestAllHandlers_NilResolvedReturn503 guards every config handler against
// nil ResolvedConfig — the wiring contract is "feature unavailable" not
// "panic on construct". One subtest per handler keeps the failure mode
// obvious in CI output.
func TestAllHandlers_NilResolvedReturn503(t *testing.T) {
	cases := []struct {
		name    string
		handler http.Handler
		path    string
	}{
		{"api-keys.reveal", admin.APIKeysRevealHandler(nil), "/api/v1/config/api-keys/reveal?configuration=x&name=y"},
		{"configurations.list", admin.ConfigurationsListHandler(nil), "/api/v1/config/configurations"},
		{"configurations.detail", admin.ConfigurationDetailHandler(nil), "/api/v1/config/configurations/x"},
		{"rules.list", admin.RulesListHandler(nil), "/api/v1/config/rules"},
		{"rules.detail", admin.RuleDetailHandler(nil), "/api/v1/config/rules/x"},
		{"providers.list", admin.ProvidersListHandler(nil), "/api/v1/config/providers"},
		{"providers.detail", admin.ProviderDetailHandler(nil), "/api/v1/config/providers/x"},
		{"routes", admin.RoutesHandler(nil), "/api/v1/config/routes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			tc.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want 503", rec.Code)
			}
		})
	}
}

// TestDetailHandlers_NotFoundOnBlankAndMissing covers two close-together
// 404 paths on each detail handler — empty path (trailing slash) and a
// name that does not match any loaded entity.
func TestDetailHandlers_NotFoundOnBlankAndMissing(t *testing.T) {
	rc := fixtureResolved(t)
	cases := []struct {
		name    string
		handler http.Handler
		prefix  string
	}{
		{"configurations", admin.ConfigurationDetailHandler(config.NewStore(rc)), "/api/v1/config/configurations/"},
		{"rules", admin.RuleDetailHandler(config.NewStore(rc)), "/api/v1/config/rules/"},
		{"providers", admin.ProviderDetailHandler(config.NewStore(rc)), "/api/v1/config/providers/"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/blank", func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.prefix, nil)
			tc.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
		})
		t.Run(tc.name+"/missing", func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.prefix+"does-not-exist", nil)
			tc.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
		})
	}
}

// TestRulesListHandler_UsedByCoversMultipleConfigurations verifies a rule
// referenced by two configurations comes back with both names — the
// "used by" backlink is otherwise easy to silently drop.
func TestRulesListHandler_UsedByCoversMultipleConfigurations(t *testing.T) {
	rc := fixtureResolved(t)
	// Attach the same rule to prod so it now appears under both configs.
	prod := rc.Configurations["prod"]
	prod.RuleNames = []string{"only-openai"}
	rc.Configurations["prod"] = prod

	h := admin.RulesListHandler(config.NewStore(rc))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/rules", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []admin.RuleSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	if len(got[0].UsedBy) != 2 {
		t.Errorf("UsedBy = %v, want 2 configurations", got[0].UsedBy)
	}
	if got[0].UsedBy[0] != "dev" || got[0].UsedBy[1] != "prod" {
		t.Errorf("UsedBy ordering = %v, want sorted [dev, prod]", got[0].UsedBy)
	}
}

func TestRoutesHandler_FlattensRouteIndex(t *testing.T) {
	rc := fixtureResolved(t)
	h := admin.RoutesHandler(config.NewStore(rc))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/routes", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []admin.RouteRow
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("expected non-empty route table")
	}
	// anthropic.messages is PrefixOptional so it should appear at both
	// /v1/messages (bare) and /anthropic/v1/messages (prefixed).
	var seenBare, seenPrefixed bool
	for _, r := range got {
		switch r.Path {
		case "/v1/messages":
			seenBare = true
		case "/anthropic/v1/messages":
			seenPrefixed = true
		}
	}
	if !seenBare || !seenPrefixed {
		t.Errorf("expected both bare and prefixed anthropic.messages; saw bare=%v prefixed=%v", seenBare, seenPrefixed)
	}
}
