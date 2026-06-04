package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

// twoConfigStore builds a v2 snapshot with two configurations whose bindings
// exercise every sort comparator branch in BindingsHandler: same
// (configuration, protocol) differing only by models (the models tie-break),
// differing protocols, differing configurations, and passthrough bindings
// across two configurations.
func twoConfigStore() *config.ResolvedConfig {
	rc := &config.ResolvedConfig{
		Providers: contractsconfig.ProvidersConfig{
			"openai": contractsconfig.Provider{
				BaseURL: "https://api.openai.com",
				Protocols: map[string]contractsconfig.ProviderProtocol{
					"chat":      {Path: "/v1/chat/completions", Auth: &contractsconfig.ProviderAuth{Header: "Authorization", Format: "Bearer {key}"}},
					"responses": {Path: "/v1/responses"},
				},
			},
			"anthropic": contractsconfig.Provider{
				BaseURL: "https://api.anthropic.com",
				Protocols: map[string]contractsconfig.ProviderProtocol{
					"messages": {Path: "/v1/messages", Auth: &contractsconfig.ProviderAuth{Header: "x-api-key", Format: "{key}"}},
				},
				Passthrough: map[string]contractsconfig.PassthroughFamily{
					"messages_batches": {Paths: []contractsconfig.PassthroughPath{{Match: "/v1/messages/batches", Methods: []string{"POST"}}}},
				},
			},
		},
		Configurations: map[string]contractsconfig.Configuration{
			"alpha": {
				Credentials: map[string]string{"openai": "sk-a", "anthropic": "ak-a"},
				Bindings: []contractsconfig.Binding{
					// same (alpha, chat) — models tie-break: "gpt-*" < "o1*"
					{Protocol: "chat", Models: []string{"o1*"}, Provider: "openai"},
					{Protocol: "chat", Models: []string{"gpt-*"}, Provider: "openai"},
					{Protocol: "messages", Models: []string{"claude-*"}, Provider: "anthropic"},
				},
				PassthroughBindings: []contractsconfig.PassthroughBinding{
					{Family: "messages_batches", Provider: "anthropic"},
				},
			},
			"beta": {
				Credentials: map[string]string{"openai": "sk-b", "anthropic": "ak-b"},
				Bindings: []contractsconfig.Binding{
					{Protocol: "responses", Models: []string{"gpt-*"}, Provider: "openai"},
				},
				PassthroughBindings: []contractsconfig.PassthroughBinding{
					{Family: "messages_batches", Provider: "anthropic"},
				},
			},
		},
	}
	populateIndexes(rc)
	return rc
}

func TestBindingsHandler_SortOrderingAcrossBranches(t *testing.T) {
	h := admin.BindingsHandler(config.NewStore(twoConfigStore()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/config/bindings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got struct {
		Bindings            []admin.BindingRow            `json:"bindings"`
		PassthroughBindings []admin.PassthroughBindingRow `json:"passthrough_bindings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Bindings) != 4 {
		t.Fatalf("bindings = %d, want 4", len(got.Bindings))
	}
	// Sorted by configuration, then protocol, then models join.
	// alpha/chat/gpt-* , alpha/chat/o1* , alpha/messages/claude-* , beta/responses/gpt-*
	want := [][2]string{
		{"alpha", "chat"}, {"alpha", "chat"}, {"alpha", "messages"}, {"beta", "responses"},
	}
	for i, w := range want {
		if got.Bindings[i].Configuration != w[0] || got.Bindings[i].Protocol != w[1] {
			t.Errorf("bindings[%d] = %s/%s, want %s/%s", i, got.Bindings[i].Configuration, got.Bindings[i].Protocol, w[0], w[1])
		}
	}
	// The two alpha/chat rows are ordered by models join: gpt-* before o1*.
	if got.Bindings[0].Models[0] != "gpt-*" || got.Bindings[1].Models[0] != "o1*" {
		t.Errorf("models tie-break order wrong: %v then %v", got.Bindings[0].Models, got.Bindings[1].Models)
	}
	if len(got.PassthroughBindings) != 2 {
		t.Fatalf("passthrough = %d, want 2", len(got.PassthroughBindings))
	}
	if got.PassthroughBindings[0].Configuration != "alpha" || got.PassthroughBindings[1].Configuration != "beta" {
		t.Errorf("passthrough not sorted by configuration: %+v", got.PassthroughBindings)
	}
}

func TestProviderDetailHandler_NoPassthrough(t *testing.T) {
	h := admin.ProviderDetailHandler(config.NewStore(twoConfigStore()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/config/providers/openai", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got admin.ProviderDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Passthrough) != 0 {
		t.Errorf("openai has no passthrough families, got %+v", got.Passthrough)
	}
	if len(got.Protocols) != 2 || got.Protocols[0].Name != "chat" || got.Protocols[1].Name != "responses" {
		t.Errorf("protocols = %+v, want chat then responses", got.Protocols)
	}
	// responses has no auth override → empty header.
	if got.Protocols[1].AuthHeader != "" {
		t.Errorf("responses auth_header = %q, want empty", got.Protocols[1].AuthHeader)
	}
}
