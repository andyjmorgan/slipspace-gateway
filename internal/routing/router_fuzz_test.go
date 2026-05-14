package routing_test

import (
	"errors"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/routing"
)

// FuzzResolve drives the router with arbitrary (method, path) inputs to verify
// no input panics and the result is always either a successful Match or one of
// the typed routing errors. Crashing on a malformed path would expose the
// gateway to a trivial DoS vector — every accept path must terminate cleanly.
func FuzzResolve(f *testing.F) {
	rc := &config.ResolvedConfig{
		Providers: contractsconfig.ProvidersConfig{
			"openai": {
				Prefix:         "openai",
				PrefixRequired: false,
				Endpoints: map[string]contractsconfig.Endpoint{
					"chat_completions": {
						Method:        []string{"POST"},
						AcceptedPaths: []string{"/v1/chat/completions"},
					},
					"models": {
						Method:        []string{"GET"},
						AcceptedPaths: []string{"/v1/models"},
					},
				},
			},
			"gemini": {
				Prefix:         "gemini",
				PrefixRequired: true,
				Endpoints: map[string]contractsconfig.Endpoint{
					"generate_content": {
						Method:        []string{"POST"},
						AcceptedPaths: []string{"/v1beta/models/{model}:generateContent"},
					},
				},
			},
		},
		RouteIndex: map[string]config.Route{
			"/v1/chat/completions":                          {Provider: "openai", Endpoint: "chat_completions"},
			"/openai/v1/chat/completions":                   {Provider: "openai", Endpoint: "chat_completions"},
			"/v1/models":                                    {Provider: "openai", Endpoint: "models"},
			"/openai/v1/models":                             {Provider: "openai", Endpoint: "models"},
			"/gemini/v1beta/models/{model}:generateContent": {Provider: "gemini", Endpoint: "generate_content"},
		},
	}
	r, err := routing.New(rc)
	if err != nil {
		f.Fatalf("New: %v", err)
	}

	seeds := []struct {
		method, path string
	}{
		{"GET", "/v1/models"},
		{"POST", "/v1/chat/completions"},
		{"POST", "/gemini/v1beta/models/gemini-pro:generateContent"},
		{"", ""},
		{"PUT", "/"},
		{"GET", "/totally/unknown"},
		{"post", "/V1/Chat/Completions"},
		{"POST", "/gemini/v1beta/models/:generateContent"},
		{"POST", "/gemini/v1beta/models/a/b:generateContent"},
		{"GET", "/gemini/v1beta/models/anything:generateContent"},
		{"\x00", "\x00"},
		{"GET", "%2F%2F"},
	}
	for _, s := range seeds {
		f.Add(s.method, s.path)
	}

	f.Fuzz(func(t *testing.T, method, path string) {
		match, err := r.Resolve(method, path)
		if err == nil {
			if match.Provider == "" || match.Endpoint == "" {
				t.Fatalf("Resolve(%q,%q): nil err but empty match %+v", method, path, match)
			}
			return
		}
		if !errors.Is(err, routing.ErrNoRoute) && !errors.Is(err, routing.ErrMethodNotAllowed) {
			t.Fatalf("Resolve(%q,%q): unexpected error %v", method, path, err)
		}
	})
}
