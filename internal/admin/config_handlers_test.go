package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	rulescontract "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/admin"
	"github.com/andyjmorgan/slipspace-gateway/internal/config"
)

// fixtureResolved builds a small but representative v2 ResolvedConfig with the
// indexes populated. Used by every handler test below to avoid duplicating the
// wiring.
func fixtureResolved(t *testing.T) *config.ResolvedConfig {
	t.Helper()
	rc := &config.ResolvedConfig{
		Providers: contractsconfig.ProvidersConfig{
			"openai": contractsconfig.Provider{
				BaseURL: "https://api.openai.com",
				Protocols: map[string]contractsconfig.ProviderProtocol{
					"chat": {
						Path: "/v1/chat/completions",
						Auth: &contractsconfig.ProviderAuth{Header: "Authorization", Format: "Bearer {key}"},
					},
				},
			},
			"anthropic": contractsconfig.Provider{
				BaseURL: "https://api.anthropic.com",
				Protocols: map[string]contractsconfig.ProviderProtocol{
					"messages": {
						Path: "/v1/messages",
						Auth: &contractsconfig.ProviderAuth{Header: "x-api-key", Format: "{key}"},
					},
					"chat": {
						Path: "/v1/chat/completions",
						Auth: &contractsconfig.ProviderAuth{Header: "Authorization", Format: "Bearer {key}"},
					},
				},
				Passthrough: map[string]contractsconfig.PassthroughFamily{
					"messages_batches": {
						Auth: &contractsconfig.ProviderAuth{Header: "x-api-key", Format: "{key}"},
						Paths: []contractsconfig.PassthroughPath{
							{Match: "/v1/messages/batches", Methods: []string{"POST", "GET"}},
						},
					},
				},
			},
		},
		Configurations: map[string]contractsconfig.Configuration{
			"dev": {
				Credentials: map[string]string{
					"openai":    "sk-very-secret-openai-key-abcd1234",
					"anthropic": "sk-ant-supersecret-mnop5678",
				},
				Bindings: []contractsconfig.Binding{
					{Protocol: "chat", Models: []string{"gpt-*"}, Provider: "openai"},
					{Protocol: "messages", Models: []string{"claude-*"}, Provider: "anthropic"},
				},
				PassthroughBindings: []contractsconfig.PassthroughBinding{
					{Family: "messages_batches", Provider: "anthropic"},
				},
				RuleNames: []string{"only-openai"},
				Tags:      map[string]string{"tier": "dev"},
			},
			"prod": {
				Credentials: map[string]string{
					"openai": "sk-prod-tighten-this-wxyz9876",
				},
				Bindings: []contractsconfig.Binding{
					{Protocol: "chat", Models: []string{"gpt-*"}, Provider: "openai"},
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
	}
	populateIndexes(rc)
	return rc
}

// populateIndexes mirrors config.buildIndexes for the handful of indexes the
// admin handlers read. Kept local to the test so a future loader refactor does
// not break these tests through the back door.
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
	// Hard rule: no upstream credential or api-key secret leaves the handler.
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
	if got.Credentials["openai"].Last4 != "1234" {
		t.Errorf("openai last4 = %q, want 1234", got.Credentials["openai"].Last4)
	}
	if got.Credentials["openai"].Length != len("sk-very-secret-openai-key-abcd1234") {
		t.Errorf("openai length = %d", got.Credentials["openai"].Length)
	}
	if got.Credentials["anthropic"].Last4 != "5678" {
		t.Errorf("anthropic last4 = %q", got.Credentials["anthropic"].Last4)
	}
	if len(got.Bindings) != 2 {
		t.Errorf("Bindings = %+v, want 2", got.Bindings)
	}
	if len(got.PassthroughBindings) != 1 || got.PassthroughBindings[0].Family != "messages_batches" {
		t.Errorf("PassthroughBindings = %+v", got.PassthroughBindings)
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
	if !got[0].HasPassthrough {
		t.Errorf("anthropic HasPassthrough = false, want true")
	}
}

func TestProviderDetailHandler_IncludesProtocolsAndPassthrough(t *testing.T) {
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
	if len(got.Protocols) != 2 {
		t.Errorf("Protocols = %+v, want 2 (chat, messages)", got.Protocols)
	}
	// Sorted: chat then messages.
	if got.Protocols[0].Name != "chat" || got.Protocols[1].Name != "messages" {
		t.Errorf("Protocols order = %+v", got.Protocols)
	}
	if got.Protocols[1].AuthHeader != "x-api-key" {
		t.Errorf("messages auth header = %q, want x-api-key", got.Protocols[1].AuthHeader)
	}
	if len(got.Passthrough) != 1 || got.Passthrough[0].Name != "messages_batches" {
		t.Errorf("Passthrough = %+v", got.Passthrough)
	}
}

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
		{"bindings", admin.BindingsHandler(nil), "/api/v1/config/bindings"},
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

func TestBindingsHandler_FlattensAcrossConfigurations(t *testing.T) {
	rc := fixtureResolved(t)
	h := admin.BindingsHandler(config.NewStore(rc))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/bindings", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got struct {
		Bindings    []admin.BindingRow            `json:"bindings"`
		Passthrough []admin.PassthroughBindingRow `json:"passthrough_bindings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// dev has 2 generative bindings, prod has 1 → 3 total.
	if len(got.Bindings) != 3 {
		t.Fatalf("bindings = %d, want 3", len(got.Bindings))
	}
	// Each row carries its owning configuration.
	for _, b := range got.Bindings {
		if b.Configuration == "" {
			t.Errorf("binding row missing configuration: %+v", b)
		}
	}
	if len(got.Passthrough) != 1 || got.Passthrough[0].Family != "messages_batches" {
		t.Errorf("passthrough = %+v", got.Passthrough)
	}
}
