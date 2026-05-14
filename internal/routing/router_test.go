package routing_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/routing"
)

// buildConfig is a hand-rolled ResolvedConfig that mirrors the relevant shape
// of config-dev/providers.yaml so unit tests don't depend on the on-disk
// fixture changing.
func buildConfig(t *testing.T) *config.ResolvedConfig {
	t.Helper()

	rc := &config.ResolvedConfig{
		Providers: contractsconfig.ProvidersConfig{
			"openai": {
				BaseURL:        "http://mockllm:5555",
				Prefix:         "openai",
				PrefixRequired: false,
				Endpoints: map[string]contractsconfig.Endpoint{
					"chat_completions": {
						Path:          "/v1/chat/completions",
						Method:        []string{"POST"},
						AcceptedPaths: []string{"/v1/chat/completions", "/chat/completions"},
					},
					"models": {
						Path:          "/v1/models",
						Method:        []string{"GET"},
						AcceptedPaths: []string{"/v1/models", "/models"},
					},
				},
			},
			"anthropic": {
				BaseURL:        "http://mockllm:5555",
				Prefix:         "anthropic",
				PrefixRequired: true,
				Endpoints: map[string]contractsconfig.Endpoint{
					"messages": {
						Path:          "/v1/messages",
						Method:        []string{"POST"},
						AcceptedPaths: []string{"/v1/messages", "/messages"},
					},
					"models": {
						Path:          "/v1/models",
						Method:        []string{"GET"},
						AcceptedPaths: []string{"/v1/models"},
					},
				},
			},
			"gemini": {
				BaseURL:        "http://mockllm:5555",
				Prefix:         "gemini",
				PrefixRequired: true,
				Endpoints: map[string]contractsconfig.Endpoint{
					"generate_content": {
						Path:          "/v1beta/models/{model}:generateContent",
						Method:        []string{"POST"},
						AcceptedPaths: []string{"/v1beta/models/{model}:generateContent"},
					},
					"models": {
						Path:          "/v1beta/models",
						Method:        []string{"GET"},
						AcceptedPaths: []string{"/v1beta/models"},
					},
				},
			},
		},
		RouteIndex: map[string]config.Route{
			"/v1/chat/completions":                          {Provider: "openai", Endpoint: "chat_completions"},
			"/chat/completions":                             {Provider: "openai", Endpoint: "chat_completions"},
			"/openai/v1/chat/completions":                   {Provider: "openai", Endpoint: "chat_completions"},
			"/openai/chat/completions":                      {Provider: "openai", Endpoint: "chat_completions"},
			"/v1/models":                                    {Provider: "openai", Endpoint: "models"},
			"/models":                                       {Provider: "openai", Endpoint: "models"},
			"/openai/v1/models":                             {Provider: "openai", Endpoint: "models"},
			"/openai/models":                                {Provider: "openai", Endpoint: "models"},
			"/anthropic/v1/messages":                        {Provider: "anthropic", Endpoint: "messages"},
			"/anthropic/messages":                           {Provider: "anthropic", Endpoint: "messages"},
			"/anthropic/v1/models":                          {Provider: "anthropic", Endpoint: "models"},
			"/gemini/v1beta/models":                         {Provider: "gemini", Endpoint: "models"},
			"/gemini/v1beta/models/{model}:generateContent": {Provider: "gemini", Endpoint: "generate_content"},
		},
	}
	return rc
}

func TestNew_NilConfig(t *testing.T) {
	if _, err := routing.New(nil); err == nil {
		t.Fatal("New(nil) want error, got nil")
	}
}

func TestNew_UnknownProviderInRouteIndex(t *testing.T) {
	rc := &config.ResolvedConfig{
		Providers: contractsconfig.ProvidersConfig{},
		RouteIndex: map[string]config.Route{
			"/v1/chat/completions": {Provider: "ghost", Endpoint: "chat"},
		},
	}
	if _, err := routing.New(rc); err == nil {
		t.Fatal("want error for unknown provider, got nil")
	}
}

func TestNew_UnknownEndpointInRouteIndex(t *testing.T) {
	rc := &config.ResolvedConfig{
		Providers: contractsconfig.ProvidersConfig{
			"openai": {
				Endpoints: map[string]contractsconfig.Endpoint{
					"chat": {Path: "/v1/chat", Method: []string{"POST"}, AcceptedPaths: []string{"/v1/chat"}},
				},
			},
		},
		RouteIndex: map[string]config.Route{
			"/v1/chat": {Provider: "openai", Endpoint: "ghost"},
		},
	}
	if _, err := routing.New(rc); err == nil {
		t.Fatal("want error for unknown endpoint, got nil")
	}
}

func TestNew_EmptyMethodList(t *testing.T) {
	rc := &config.ResolvedConfig{
		Providers: contractsconfig.ProvidersConfig{
			"openai": {
				Endpoints: map[string]contractsconfig.Endpoint{
					"chat": {Path: "/v1/chat", Method: nil, AcceptedPaths: []string{"/v1/chat"}},
				},
			},
		},
		RouteIndex: map[string]config.Route{
			"/v1/chat": {Provider: "openai", Endpoint: "chat"},
		},
	}
	if _, err := routing.New(rc); err == nil {
		t.Fatal("want error for empty method list, got nil")
	}
}

func TestResolve(t *testing.T) {
	rc := buildConfig(t)
	r, err := routing.New(rc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	type want struct {
		provider string
		endpoint string
		params   map[string]string
		err      error
	}

	cases := []struct {
		name   string
		method string
		path   string
		want   want
	}{
		{
			name:   "openai bare chat",
			method: "POST",
			path:   "/v1/chat/completions",
			want:   want{provider: "openai", endpoint: "chat_completions"},
		},
		{
			name:   "openai prefixed chat",
			method: "POST",
			path:   "/openai/v1/chat/completions",
			want:   want{provider: "openai", endpoint: "chat_completions"},
		},
		{
			name:   "openai bare models",
			method: "GET",
			path:   "/v1/models",
			want:   want{provider: "openai", endpoint: "models"},
		},
		{
			name:   "anthropic prefixed messages",
			method: "POST",
			path:   "/anthropic/v1/messages",
			want:   want{provider: "anthropic", endpoint: "messages"},
		},
		{
			name:   "anthropic prefixed models",
			method: "GET",
			path:   "/anthropic/v1/models",
			want:   want{provider: "anthropic", endpoint: "models"},
		},
		{
			name:   "anthropic bare messages unreachable",
			method: "POST",
			path:   "/v1/messages",
			want:   want{err: routing.ErrNoRoute},
		},
		{
			name:   "gemini generate_content with model placeholder",
			method: "POST",
			path:   "/gemini/v1beta/models/gemini-pro:generateContent",
			want: want{
				provider: "gemini",
				endpoint: "generate_content",
				params:   map[string]string{"model": "gemini-pro"},
			},
		},
		{
			name:   "gemini generate_content with versioned model",
			method: "POST",
			path:   "/gemini/v1beta/models/gemini-1.5-flash-latest:generateContent",
			want: want{
				provider: "gemini",
				endpoint: "generate_content",
				params:   map[string]string{"model": "gemini-1.5-flash-latest"},
			},
		},
		{
			name:   "gemini bare generate_content unreachable",
			method: "POST",
			path:   "/v1beta/models/gemini-pro:generateContent",
			want:   want{err: routing.ErrNoRoute},
		},
		{
			name:   "method mismatch on exact path",
			method: "GET",
			path:   "/v1/chat/completions",
			want:   want{err: routing.ErrMethodNotAllowed},
		},
		{
			name:   "method mismatch on patterned path",
			method: "GET",
			path:   "/gemini/v1beta/models/gemini-pro:generateContent",
			want:   want{err: routing.ErrMethodNotAllowed},
		},
		{
			name:   "method lowercase normalized",
			method: "post",
			path:   "/v1/chat/completions",
			want:   want{provider: "openai", endpoint: "chat_completions"},
		},
		{
			name:   "unknown path",
			method: "GET",
			path:   "/totally/unknown",
			want:   want{err: routing.ErrNoRoute},
		},
		{
			name:   "patterned path with trailing slash fails to match",
			method: "POST",
			path:   "/gemini/v1beta/models/gemini-pro:generateContent/",
			want:   want{err: routing.ErrNoRoute},
		},
		{
			name:   "patterned path with slash inside model name fails",
			method: "POST",
			path:   "/gemini/v1beta/models/foo/bar:generateContent",
			want:   want{err: routing.ErrNoRoute},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.Resolve(tc.method, tc.path)
			if tc.want.err != nil {
				if !errors.Is(err, tc.want.err) {
					t.Fatalf("err = %v, want %v", err, tc.want.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got.Provider != tc.want.provider || got.Endpoint != tc.want.endpoint {
				t.Fatalf("got %s.%s, want %s.%s",
					got.Provider, got.Endpoint, tc.want.provider, tc.want.endpoint)
			}
			if len(tc.want.params) == 0 && len(got.Params) != 0 {
				t.Fatalf("Params = %v, want none", got.Params)
			}
			for k, v := range tc.want.params {
				if got.Params[k] != v {
					t.Fatalf("Params[%q] = %q, want %q", k, got.Params[k], v)
				}
			}
		})
	}
}

// TestResolve_AgainstConfigDevFixtures wires the router up to the on-disk
// config-dev/ tree so the end-to-end loader → router path is exercised. If
// config-dev/providers.yaml drifts in a way the router can't handle, this
// fails first.
func TestResolve_AgainstConfigDevFixtures(t *testing.T) {
	dir, err := filepath.Abs(filepath.Join("..", "..", "config-dev"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("config.Load(%s): %v", dir, err)
	}
	r, err := routing.New(resolved)
	if err != nil {
		t.Fatalf("routing.New: %v", err)
	}

	cases := []struct {
		method, path string
		provider     string
		endpoint     string
		params       map[string]string
	}{
		{"POST", "/v1/chat/completions", "openai", "chat_completions", nil},
		{"POST", "/openai/v1/chat/completions", "openai", "chat_completions", nil},
		{"GET", "/v1/models", "openai", "models", nil},
		{"GET", "/anthropic/v1/models", "anthropic", "models", nil},
		{"POST", "/anthropic/v1/messages", "anthropic", "messages", nil},
		{"GET", "/gemini/v1beta/models", "gemini", "models", nil},
		{"POST", "/gemini/v1beta/models/gemini-pro:generateContent", "gemini", "generate_content", map[string]string{"model": "gemini-pro"}},
	}
	for _, tc := range cases {
		got, err := r.Resolve(tc.method, tc.path)
		if err != nil {
			t.Errorf("Resolve(%s %s): %v", tc.method, tc.path, err)
			continue
		}
		if got.Provider != tc.provider || got.Endpoint != tc.endpoint {
			t.Errorf("Resolve(%s %s) = %s.%s, want %s.%s",
				tc.method, tc.path, got.Provider, got.Endpoint, tc.provider, tc.endpoint)
		}
		for k, v := range tc.params {
			if got.Params[k] != v {
				t.Errorf("Resolve(%s %s) Params[%q] = %q, want %q",
					tc.method, tc.path, k, got.Params[k], v)
			}
		}
	}
}

func TestNew_DeterministicPatternOrder(t *testing.T) {
	rc := &config.ResolvedConfig{
		Providers: contractsconfig.ProvidersConfig{
			"a": {
				Endpoints: map[string]contractsconfig.Endpoint{
					"x": {Method: []string{"GET"}, AcceptedPaths: []string{"/x/{v}"}},
				},
			},
			"b": {
				Endpoints: map[string]contractsconfig.Endpoint{
					"y": {Method: []string{"GET"}, AcceptedPaths: []string{"/y/{v}"}},
				},
			},
		},
		RouteIndex: map[string]config.Route{
			"/x/{v}": {Provider: "a", Endpoint: "x"},
			"/y/{v}": {Provider: "b", Endpoint: "y"},
		},
	}
	for i := 0; i < 5; i++ {
		r, err := routing.New(rc)
		if err != nil {
			t.Fatalf("iter %d: New: %v", i, err)
		}
		got, err := r.Resolve("GET", "/x/foo")
		if err != nil || got.Provider != "a" {
			t.Fatalf("iter %d: got %+v %v", i, got, err)
		}
	}
}
