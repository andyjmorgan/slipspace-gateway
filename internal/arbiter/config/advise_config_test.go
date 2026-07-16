package config

import (
	"strings"
	"testing"
)

// adviseBase returns a Config valid except for the advise block, including the
// one registered gateway advise.enabled requires.
func adviseBase() Config {
	c := base()
	c.Gateways = []Gateway{{ID: "gw-a", HMACSecret: "secret-a"}}
	return c
}

// validAdvise returns a fully-populated enabled advise block.
func validAdvise() Advise {
	return Advise{
		Enabled: true,
		//nolint:gosec // test fixture path, not a real credential
		Upstream: AdviseUpstream{
			BaseURL:    "http://gateway.internal:8080",
			APIKeyFile: "/etc/arbiter/advisor-key",
			Model:      "judge-model-internal",
		},
		Candidates: []string{"cheap-candidate-a"},
	}
}

func TestValidate_Advise(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name:   "disabled block validates fine",
			mutate: func(c *Config) { c.Advise = Advise{} },
		},
		{
			name: "disabled block ignores missing upstream",
			mutate: func(c *Config) {
				c.Advise = Advise{Enabled: false, Candidates: nil}
				c.Gateways = nil
			},
		},
		{
			name:   "valid enabled block",
			mutate: func(*Config) {},
		},
		{
			name:    "enabled without base_url",
			mutate:  func(c *Config) { c.Advise.Upstream.BaseURL = "" },
			wantErr: "advise.upstream.base_url is required",
		},
		{
			name:    "enabled without api_key_file",
			mutate:  func(c *Config) { c.Advise.Upstream.APIKeyFile = "" },
			wantErr: "advise.upstream.api_key_file is required",
		},
		{
			name:    "enabled without model",
			mutate:  func(c *Config) { c.Advise.Upstream.Model = "" },
			wantErr: "advise.upstream.model is required",
		},
		{
			name:    "enabled without candidates",
			mutate:  func(c *Config) { c.Advise.Candidates = nil },
			wantErr: "advise.candidates is required",
		},
		{
			name:    "enabled without registered gateways",
			mutate:  func(c *Config) { c.Gateways = nil },
			wantErr: "at least one registered gateway",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := adviseBase()
			cfg.Advise = validAdvise()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want ok, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want contains %q", err, tc.wantErr)
			}
		})
	}
}

func TestAdviseEnabled(t *testing.T) {
	if (Config{}).AdviseEnabled() {
		t.Error("disabled by default")
	}
	if !(Config{Advise: Advise{Enabled: true}}).AdviseEnabled() {
		t.Error("enabled")
	}
}

func TestAdviseTimeoutSeconds(t *testing.T) {
	zero, neg, ten := 0, -5, 10
	cases := []struct {
		name string
		ptr  *int
		want int
	}{
		{"nil takes default", nil, DefaultAdviseTimeoutSeconds},
		{"zero takes default", &zero, DefaultAdviseTimeoutSeconds},
		{"negative takes default", &neg, DefaultAdviseTimeoutSeconds},
		{"explicit passes through", &ten, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{Advise: Advise{Upstream: AdviseUpstream{TimeoutSeconds: tc.ptr}}}
			if got := c.AdviseTimeoutSeconds(); got != tc.want {
				t.Errorf("AdviseTimeoutSeconds() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAdviseCacheTTLSeconds(t *testing.T) {
	zero, neg, hour := 0, -1, 3600
	cases := []struct {
		name string
		ptr  *int
		want int
	}{
		{"nil takes default", nil, DefaultAdviseCacheTTLSeconds},
		{"zero takes default", &zero, DefaultAdviseCacheTTLSeconds},
		{"negative takes default", &neg, DefaultAdviseCacheTTLSeconds},
		{"explicit passes through", &hour, 3600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{Advise: Advise{CacheTTLSeconds: tc.ptr}}
			if got := c.AdviseCacheTTLSeconds(); got != tc.want {
				t.Errorf("AdviseCacheTTLSeconds() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestLoad_AdviseBlockParses(t *testing.T) {
	body := validBody + `
advise:
  enabled: true
  upstream:
    base_url: http://gateway.internal:8080
    api_key_file: /etc/arbiter/advisor-key
    model: judge-model-internal
    timeout_seconds: 15
  candidates:
    - cheap-candidate-a
    - cheap-candidate-b
  cache_ttl_seconds: 600
`
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.AdviseEnabled() {
		t.Error("AdviseEnabled() = false")
	}
	if cfg.Advise.Upstream.BaseURL != "http://gateway.internal:8080" {
		t.Errorf("BaseURL = %q", cfg.Advise.Upstream.BaseURL)
	}
	if cfg.AdviseTimeoutSeconds() != 15 {
		t.Errorf("AdviseTimeoutSeconds() = %d, want 15", cfg.AdviseTimeoutSeconds())
	}
	if cfg.AdviseCacheTTLSeconds() != 600 {
		t.Errorf("AdviseCacheTTLSeconds() = %d, want 600", cfg.AdviseCacheTTLSeconds())
	}
	if len(cfg.Advise.Candidates) != 2 || cfg.Advise.Candidates[0] != "cheap-candidate-a" {
		t.Errorf("Candidates = %v", cfg.Advise.Candidates)
	}
}
