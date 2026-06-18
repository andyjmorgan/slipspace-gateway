package config_test

import (
	"errors"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/config"
)

func TestLoadEnv_AllDefaults(t *testing.T) {
	for _, name := range config.EnvVarNames() {
		t.Setenv(name, "")
	}

	env, err := config.LoadEnv()
	if err != nil {
		t.Fatalf("LoadEnv: %v", err)
	}

	cases := []struct {
		name string
		got  any
		want any
	}{
		{"HTTPBind", env.HTTPBind, config.DefaultHTTPBind},
		{"ShutdownDrainSeconds", env.ShutdownDrainSeconds, config.DefaultShutdownDrainSeconds},
		{"LogFormat", env.LogFormat, config.DefaultLogFormat},
		{"LogLevel", env.LogLevel, config.DefaultLogLevel},
		{"PrometheusBind", env.PrometheusBind, ""},
		{"OTLPEndpoint", env.OTLPEndpoint, ""},
		{"OTLPProtocol", env.OTLPProtocol, config.DefaultOTLPProtocol},
		{"ConfigDir", env.ConfigDir, config.DefaultConfigDir},
		{"SpoolRoot", env.SpoolRoot, config.DefaultSpoolRoot},
		{"RulesMaxGroupDepth", env.RulesMaxGroupDepth, config.DefaultRulesMaxGroupDepth},
		{"UpstreamResponseHeaderTimeoutSeconds", env.UpstreamResponseHeaderTimeoutSeconds, config.DefaultUpstreamResponseHeaderTimeoutSeconds},
		{"AdminLiveFeedCapacity", env.AdminLiveFeedCapacity, config.DefaultAdminLiveFeedCapacity},
		{"AdminLiveFeedBodyBytes", env.AdminLiveFeedBodyBytes, config.DefaultAdminLiveFeedBodyBytes},
		{"AdminLiveFeedBodyMaxBytes", env.AdminLiveFeedBodyMaxBytes, config.DefaultAdminLiveFeedBodyMaxBytes},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}

	if env.PrometheusEnabled() {
		t.Error("PrometheusEnabled() = true with empty PrometheusBind")
	}
	if env.OTLPEnabled() {
		t.Error("OTLPEnabled() = true with empty OTLPEndpoint")
	}
}

func TestLoadEnv_OverridesApplied(t *testing.T) {
	t.Setenv(config.EnvHTTPBind, "127.0.0.1:0")
	t.Setenv(config.EnvShutdownDrainSeconds, "42")
	t.Setenv(config.EnvLogFormat, "text")
	t.Setenv(config.EnvLogLevel, "debug")
	t.Setenv(config.EnvPrometheusBind, "127.0.0.1:0")
	t.Setenv(config.EnvOTLPEndpoint, "http://otel:4317")
	t.Setenv(config.EnvOTLPProtocol, "http/protobuf")
	t.Setenv(config.EnvConfigDir, "/somewhere/else")
	t.Setenv(config.EnvSpoolRoot, "/var/spool/sluice-test")
	t.Setenv(config.EnvRulesMaxGroupDepth, "16")
	t.Setenv(config.EnvUpstreamResponseHeaderTimeoutSeconds, "300")

	env, err := config.LoadEnv()
	if err != nil {
		t.Fatalf("LoadEnv: %v", err)
	}
	if err := env.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	if env.UpstreamResponseHeaderTimeoutSeconds != 300 {
		t.Errorf("UpstreamResponseHeaderTimeoutSeconds = %d", env.UpstreamResponseHeaderTimeoutSeconds)
	}

	if env.HTTPBind != "127.0.0.1:0" {
		t.Errorf("HTTPBind = %q", env.HTTPBind)
	}
	if env.ShutdownDrainSeconds != 42 {
		t.Errorf("ShutdownDrainSeconds = %d", env.ShutdownDrainSeconds)
	}
	if env.LogFormat != "text" {
		t.Errorf("LogFormat = %q", env.LogFormat)
	}
	if env.SpoolRoot != "/var/spool/sluice-test" {
		t.Errorf("SpoolRoot = %q", env.SpoolRoot)
	}
	if env.RulesMaxGroupDepth != 16 {
		t.Errorf("RulesMaxGroupDepth = %d", env.RulesMaxGroupDepth)
	}
	if !env.PrometheusEnabled() || !env.OTLPEnabled() {
		t.Errorf("toggles wrong: prom=%v otlp=%v", env.PrometheusEnabled(), env.OTLPEnabled())
	}
	if !env.LiveFeedEnabled() {
		t.Errorf("LiveFeedEnabled() = false with default capacity")
	}
	if !env.LiveFeedBodiesEnabled() {
		t.Errorf("LiveFeedBodiesEnabled() = false with default body bytes")
	}
}

func TestServerEnv_LiveFeedBodiesDisabledByZeroBytes(t *testing.T) {
	for _, name := range config.EnvVarNames() {
		t.Setenv(name, "")
	}
	t.Setenv(config.EnvAdminLiveFeedBodyBytes, "0")
	env, err := config.LoadEnv()
	if err != nil {
		t.Fatalf("LoadEnv: %v", err)
	}
	if err := env.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if env.LiveFeedBodiesEnabled() {
		t.Errorf("LiveFeedBodiesEnabled() = true with body_bytes=0")
	}
}

func TestServerEnv_Validate_RejectsMaxBytesZeroWhenBytesPositive(t *testing.T) {
	env := defaultEnv(t)
	env.AdminLiveFeedBodyMaxBytes = 0
	if err := env.Validate(); !errors.Is(err, config.ErrInvalidEnv) {
		t.Fatalf("err = %v, want ErrInvalidEnv", err)
	}
}

func TestServerEnv_Validate_NegativeLiveFeedBodyBytes(t *testing.T) {
	env := defaultEnv(t)
	env.AdminLiveFeedBodyBytes = -1
	if err := env.Validate(); !errors.Is(err, config.ErrInvalidEnv) {
		t.Fatalf("err = %v, want ErrInvalidEnv", err)
	}
}

func TestServerEnv_LiveFeedDisabledByZero(t *testing.T) {
	for _, name := range config.EnvVarNames() {
		t.Setenv(name, "")
	}
	t.Setenv(config.EnvAdminLiveFeedCapacity, "0")
	env, err := config.LoadEnv()
	if err != nil {
		t.Fatalf("LoadEnv: %v", err)
	}
	if err := env.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if env.LiveFeedEnabled() {
		t.Errorf("LiveFeedEnabled() = true with capacity=0")
	}
}

func TestServerEnv_Validate_NegativeLiveFeedCapacity(t *testing.T) {
	env := defaultEnv(t)
	env.AdminLiveFeedCapacity = -1
	if err := env.Validate(); !errors.Is(err, config.ErrInvalidEnv) {
		t.Fatalf("err = %v, want ErrInvalidEnv", err)
	}
}

func TestLoadEnv_BadIntWrapsErrInvalidEnv(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"drain", config.EnvShutdownDrainSeconds},
		{"group_depth", config.EnvRulesMaxGroupDepth},
		{"upstream_header_timeout", config.EnvUpstreamResponseHeaderTimeoutSeconds},
		{"live_feed_capacity", config.EnvAdminLiveFeedCapacity},
		{"live_feed_body_bytes", config.EnvAdminLiveFeedBodyBytes},
		{"live_feed_body_max_bytes", config.EnvAdminLiveFeedBodyMaxBytes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.key, "not-a-number")
			_, err := config.LoadEnv()
			if !errors.Is(err, config.ErrInvalidEnv) {
				t.Fatalf("err = %v, want ErrInvalidEnv", err)
			}
		})
	}
}

func TestServerEnv_Validate_BadHTTPBind(t *testing.T) {
	env := defaultEnv(t)
	env.HTTPBind = "garbage"
	if err := env.Validate(); !errors.Is(err, config.ErrInvalidBind) {
		t.Fatalf("err = %v, want ErrInvalidBind", err)
	}
}

func TestServerEnv_Validate_BadPrometheusBind(t *testing.T) {
	env := defaultEnv(t)
	env.PrometheusBind = "no-port"
	if err := env.Validate(); !errors.Is(err, config.ErrInvalidBind) {
		t.Fatalf("err = %v, want ErrInvalidBind", err)
	}
}

func TestServerEnv_Validate_BindEdgeCases(t *testing.T) {
	cases := []struct {
		name    string
		bind    string
		wantErr bool
	}{
		{name: "empty host with numeric port", bind: ":9000", wantErr: false},
		{name: "non-numeric port", bind: "127.0.0.1:notaport", wantErr: true},
		{name: "both halves empty", bind: ":", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := defaultEnv(t)
			env.HTTPBind = tc.bind
			err := env.Validate()
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Fatalf("Validate(%q) err=%v want-err=%v", tc.bind, err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, config.ErrInvalidBind) {
				t.Fatalf("err = %v, want ErrInvalidBind", err)
			}
		})
	}
}

func TestServerEnv_Validate_EmptyPrometheusBindAllowed(t *testing.T) {
	env := defaultEnv(t)
	env.PrometheusBind = ""
	if err := env.Validate(); err != nil {
		t.Fatalf("empty PrometheusBind should be allowed: %v", err)
	}
}

func TestServerEnv_Validate_NonPositiveInts(t *testing.T) {
	cases := []struct {
		name string
		set  func(*config.ServerEnv)
	}{
		{"drain", func(e *config.ServerEnv) { e.ShutdownDrainSeconds = 0 }},
		{"spool_root_empty", func(e *config.ServerEnv) { e.SpoolRoot = "" }},
		{"group_depth_low", func(e *config.ServerEnv) { e.RulesMaxGroupDepth = 0 }},
		{"group_depth_high", func(e *config.ServerEnv) { e.RulesMaxGroupDepth = config.MaxRulesMaxGroupDepth + 1 }},
		{"upstream_header_timeout_below_floor", func(e *config.ServerEnv) {
			e.UpstreamResponseHeaderTimeoutSeconds = config.MinUpstreamResponseHeaderTimeoutSeconds - 1
		}},
		{"upstream_header_timeout_zero", func(e *config.ServerEnv) { e.UpstreamResponseHeaderTimeoutSeconds = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := defaultEnv(t)
			tc.set(env)
			if err := env.Validate(); !errors.Is(err, config.ErrInvalidEnv) {
				t.Fatalf("err = %v, want ErrInvalidEnv", err)
			}
		})
	}
}

func TestServerEnv_Validate_UnknownLogLevel(t *testing.T) {
	env := defaultEnv(t)
	env.LogLevel = "shouty"
	if err := env.Validate(); !errors.Is(err, config.ErrUnknownLogLevel) {
		t.Fatalf("err = %v, want ErrUnknownLogLevel", err)
	}
}

func TestServerEnv_Validate_UnknownLogFormat(t *testing.T) {
	env := defaultEnv(t)
	env.LogFormat = "yaml"
	if err := env.Validate(); !errors.Is(err, config.ErrUnknownLogFormat) {
		t.Fatalf("err = %v, want ErrUnknownLogFormat", err)
	}
}

func TestServerEnv_Validate_UnknownOTLPProtocol(t *testing.T) {
	env := defaultEnv(t)
	env.OTLPProtocol = "thrift"
	if err := env.Validate(); !errors.Is(err, config.ErrUnknownOTLPProtocol) {
		t.Fatalf("err = %v, want ErrUnknownOTLPProtocol", err)
	}
}

func TestLoadEnv_RedactExtraHeaders(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"unset", "", nil},
		{"empty after trim", "   ", nil},
		{"single entry", "x-acme-trace-id", []string{"x-acme-trace-id"}},
		{"multi with whitespace", " x-acme-trace-id , X-Tenant-Key ", []string{"x-acme-trace-id", "X-Tenant-Key"}},
		{"empty entries dropped", "a,,b, ,c", []string{"a", "b", "c"}},
		{"all empty yields nil", ", , ,", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Pin every other env var to default to avoid leakage from
			// the surrounding shell (matches defaultEnv's pattern).
			for _, name := range config.EnvVarNames() {
				t.Setenv(name, "")
			}
			t.Setenv(config.EnvRedactExtraHeaders, tc.raw)
			env, err := config.LoadEnv()
			if err != nil {
				t.Fatalf("LoadEnv: %v", err)
			}
			got := env.RedactExtraHeaders
			if len(got) != len(tc.want) {
				t.Fatalf("RedactExtraHeaders = %v (len %d), want %v (len %d)", got, len(got), tc.want, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("RedactExtraHeaders[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestEnvVarNames_ReturnsFreshCopy(t *testing.T) {
	a := config.EnvVarNames()
	a[0] = "MUTATED"
	b := config.EnvVarNames()
	if b[0] == "MUTATED" {
		t.Fatal("EnvVarNames returned a shared backing array")
	}
}

func defaultEnv(t *testing.T) *config.ServerEnv {
	t.Helper()
	for _, name := range config.EnvVarNames() {
		t.Setenv(name, "")
	}
	env, err := config.LoadEnv()
	if err != nil {
		t.Fatalf("LoadEnv: %v", err)
	}
	return env
}
