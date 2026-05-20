package config_test

import (
	"errors"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
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
		{"NATSURL", env.NATSURL, ""},
		{"NATSStream", env.NATSStream, config.DefaultNATSStream},
		{"NATSBucket", env.NATSBucket, config.DefaultNATSBucket},
		{"NATSStashThresholdBytes", env.NATSStashThresholdBytes, config.DefaultNATSStashThresholdBytes},
		{"NATSPublishQueueSize", env.NATSPublishQueueSize, config.DefaultNATSPublishQueueSize},
		{"ConfigDir", env.ConfigDir, config.DefaultConfigDir},
		{"RulesMaxGroupDepth", env.RulesMaxGroupDepth, config.DefaultRulesMaxGroupDepth},
		{"AdminLiveFeedCapacity", env.AdminLiveFeedCapacity, config.DefaultAdminLiveFeedCapacity},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}

	if env.ReportingEnabled() {
		t.Error("ReportingEnabled() = true with empty NATSURL")
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
	t.Setenv(config.EnvNATSURL, "nats://nats:4222")
	t.Setenv(config.EnvNATSStream, "MY_STREAM")
	t.Setenv(config.EnvNATSBucket, "MY_BUCKET")
	t.Setenv(config.EnvNATSStashThresholdBytes, "12345")
	t.Setenv(config.EnvNATSPublishQueueSize, "999")
	t.Setenv(config.EnvConfigDir, "/somewhere/else")
	t.Setenv(config.EnvRulesMaxGroupDepth, "16")

	env, err := config.LoadEnv()
	if err != nil {
		t.Fatalf("LoadEnv: %v", err)
	}
	if err := env.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
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
	if env.NATSStashThresholdBytes != 12345 {
		t.Errorf("StashThresholdBytes = %d", env.NATSStashThresholdBytes)
	}
	if env.RulesMaxGroupDepth != 16 {
		t.Errorf("RulesMaxGroupDepth = %d", env.RulesMaxGroupDepth)
	}
	if !env.ReportingEnabled() || !env.PrometheusEnabled() || !env.OTLPEnabled() {
		t.Errorf("toggles wrong: rep=%v prom=%v otlp=%v",
			env.ReportingEnabled(), env.PrometheusEnabled(), env.OTLPEnabled())
	}
	if !env.LiveFeedEnabled() {
		t.Errorf("LiveFeedEnabled() = false with default capacity")
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
		{"stash", config.EnvNATSStashThresholdBytes},
		{"queue", config.EnvNATSPublishQueueSize},
		{"group_depth", config.EnvRulesMaxGroupDepth},
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
		{"stash", func(e *config.ServerEnv) { e.NATSStashThresholdBytes = -1 }},
		{"queue", func(e *config.ServerEnv) { e.NATSPublishQueueSize = 0 }},
		{"group_depth_low", func(e *config.ServerEnv) { e.RulesMaxGroupDepth = 0 }},
		{"group_depth_high", func(e *config.ServerEnv) { e.RulesMaxGroupDepth = config.MaxRulesMaxGroupDepth + 1 }},
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
