package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// Default values for ServerEnv fields. Each constant pairs with the
// SLUICE_* env var that overrides it; see LoadEnv for the mapping.
const (
	DefaultHTTPBind                = ":8585"
	DefaultShutdownDrainSeconds    = 300
	DefaultLogFormat               = "json"
	DefaultLogLevel                = "info"
	DefaultOTLPProtocol            = "grpc"
	DefaultNATSStream              = "GATEWAY_EVENTS"
	DefaultNATSBucket              = "GATEWAY_EVENT_STASH"
	DefaultNATSStashThresholdBytes = 786432
	DefaultNATSPublishQueueSize    = 10000
	DefaultConfigDir               = "/etc/sluice/"

	// DefaultRulesMaxGroupDepth caps recursion through nested RuleGroup
	// children during evaluation. The cap is a guardrail against
	// pathological YAML that would otherwise blow the stack; 8 fits
	// every realistic operator-authored policy with headroom.
	DefaultRulesMaxGroupDepth = 8

	// MaxRulesMaxGroupDepth is the upper bound accepted by Validate.
	// Beyond this, the cost of authoring + evaluating a tree this deep
	// outweighs any expressive gain; a flat priority chain is clearer.
	MaxRulesMaxGroupDepth = 64
)

// SLUICE_* env var names. Exported so callers (CLI validator, tests,
// integration harness) can read or set them by symbolic name.
const (
	EnvHTTPBind                = "SLUICE_HTTP_BIND"
	EnvShutdownDrainSeconds    = "SLUICE_SHUTDOWN_DRAIN_SECONDS"
	EnvLogFormat               = "SLUICE_LOG_FORMAT"
	EnvLogLevel                = "SLUICE_LOG_LEVEL"
	EnvPrometheusBind          = "SLUICE_PROMETHEUS_BIND"
	EnvOTLPEndpoint            = "SLUICE_OTLP_ENDPOINT"
	EnvOTLPProtocol            = "SLUICE_OTLP_PROTOCOL"
	EnvNATSURL                 = "SLUICE_NATS_URL"
	EnvNATSStream              = "SLUICE_NATS_STREAM"
	EnvNATSBucket              = "SLUICE_NATS_BUCKET"
	EnvNATSStashThresholdBytes = "SLUICE_NATS_STASH_THRESHOLD_BYTES"
	EnvNATSPublishQueueSize    = "SLUICE_NATS_PUBLISH_QUEUE_SIZE"
	EnvConfigDir               = "SLUICE_CONFIG_DIR"
	EnvRulesMaxGroupDepth      = "SLUICE_RULES_MAX_GROUP_DEPTH"
)

// envVarNames lists every SLUICE_* var LoadEnv consults. Used by the CLI
// validator's success line ("N vars resolved") and by tests that need an
// authoritative list of inputs.
var envVarNames = []string{
	EnvHTTPBind,
	EnvShutdownDrainSeconds,
	EnvLogFormat,
	EnvLogLevel,
	EnvPrometheusBind,
	EnvOTLPEndpoint,
	EnvOTLPProtocol,
	EnvNATSURL,
	EnvNATSStream,
	EnvNATSBucket,
	EnvNATSStashThresholdBytes,
	EnvNATSPublishQueueSize,
	EnvConfigDir,
	EnvRulesMaxGroupDepth,
}

// EnvVarNames returns the set of SLUICE_* env vars consulted by LoadEnv,
// in the canonical order. The returned slice is a fresh copy; callers may
// modify it without affecting the package.
func EnvVarNames() []string {
	out := make([]string, len(envVarNames))
	copy(out, envVarNames)
	return out
}

// ServerEnv carries the per-process server configuration parsed from the
// SLUICE_* env vars. Server config is restart-to-change by design — there
// is no live-reload pathway, so the values land on the struct once at
// startup and are read-only thereafter.
type ServerEnv struct {
	// HTTPBind is the data-plane listener address (host:port).
	HTTPBind string

	// ShutdownDrainSeconds bounds graceful drain on SIGTERM/SIGINT.
	ShutdownDrainSeconds int

	// LogFormat selects the slog handler: "json" or "text".
	LogFormat string

	// LogLevel is the minimum slog level: "debug"/"info"/"warn"/"error".
	LogLevel string

	// PrometheusBind is the host:port for the /metrics scrape endpoint.
	// Empty disables the scrape listener.
	PrometheusBind string

	// OTLPEndpoint is the OTLP exporter target. Empty disables OTLP.
	OTLPEndpoint string

	// OTLPProtocol is the OTLP transport: "grpc" or "http/protobuf".
	OTLPProtocol string

	// NATSURL is the NATS server connection string. Empty disables
	// reporting — the publisher is not wired and events drop on the
	// floor with the dropped counter incremented.
	NATSURL string

	// NATSStream is the JetStream stream name reporting events publish to.
	NATSStream string

	// NATSBucket is the Object Store bucket used to stash payloads that
	// exceed NATSStashThresholdBytes.
	NATSBucket string

	// NATSStashThresholdBytes is the inline-vs-stashed cutoff. Envelopes
	// above this byte length are uploaded to the Object Store and the
	// publish carries a reference instead.
	NATSStashThresholdBytes int

	// NATSPublishQueueSize bounds the publisher's in-process queue.
	// When full, Publish drops on the floor — never blocks.
	NATSPublishQueueSize int

	// ConfigDir holds the policy + providers YAML directory loaded by
	// Load. The CLI's --dir flag overrides this for the file load only.
	ConfigDir string

	// RulesMaxGroupDepth caps recursive descent through nested
	// RuleGroup children during evaluation. Operator-authored policies
	// rarely need more than 3-4 levels; the cap is a guardrail against
	// pathological YAML triggering stack overflow in the evaluator.
	RulesMaxGroupDepth int
}

// ReportingEnabled reports whether NATS reporting is configured. False
// when NATSURL is empty.
func (e *ServerEnv) ReportingEnabled() bool { return e.NATSURL != "" }

// PrometheusEnabled reports whether the Prometheus scrape listener is
// configured. False when PrometheusBind is empty.
func (e *ServerEnv) PrometheusEnabled() bool { return e.PrometheusBind != "" }

// OTLPEnabled reports whether the OTLP exporter is configured. False
// when OTLPEndpoint is empty.
func (e *ServerEnv) OTLPEnabled() bool { return e.OTLPEndpoint != "" }

// LoadEnv parses the SLUICE_* env vars and returns a populated
// ServerEnv. Absent or empty vars fall back to the documented defaults.
// Parse failures wrap ErrInvalidEnv; validation against the allowed
// enum sets is deferred to Validate.
func LoadEnv() (*ServerEnv, error) {
	drain, err := envInt(EnvShutdownDrainSeconds, DefaultShutdownDrainSeconds)
	if err != nil {
		return nil, err
	}
	stash, err := envInt(EnvNATSStashThresholdBytes, DefaultNATSStashThresholdBytes)
	if err != nil {
		return nil, err
	}
	queue, err := envInt(EnvNATSPublishQueueSize, DefaultNATSPublishQueueSize)
	if err != nil {
		return nil, err
	}
	groupDepth, err := envInt(EnvRulesMaxGroupDepth, DefaultRulesMaxGroupDepth)
	if err != nil {
		return nil, err
	}

	return &ServerEnv{
		HTTPBind:                envString(EnvHTTPBind, DefaultHTTPBind),
		ShutdownDrainSeconds:    drain,
		LogFormat:               envString(EnvLogFormat, DefaultLogFormat),
		LogLevel:                envString(EnvLogLevel, DefaultLogLevel),
		PrometheusBind:          envString(EnvPrometheusBind, ""),
		OTLPEndpoint:            envString(EnvOTLPEndpoint, ""),
		OTLPProtocol:            envString(EnvOTLPProtocol, DefaultOTLPProtocol),
		NATSURL:                 envString(EnvNATSURL, ""),
		NATSStream:              envString(EnvNATSStream, DefaultNATSStream),
		NATSBucket:              envString(EnvNATSBucket, DefaultNATSBucket),
		NATSStashThresholdBytes: stash,
		NATSPublishQueueSize:    queue,
		ConfigDir:               envString(EnvConfigDir, DefaultConfigDir),
		RulesMaxGroupDepth:      groupDepth,
	}, nil
}

// Validate enforces invariants on a ServerEnv. Returns the first
// violation as a wrapped sentinel error.
func (e *ServerEnv) Validate() error {
	if err := validateBind(e.HTTPBind); err != nil {
		return fmt.Errorf("%s=%q: %w", EnvHTTPBind, e.HTTPBind, err)
	}
	if e.PrometheusBind != "" {
		if err := validateBind(e.PrometheusBind); err != nil {
			return fmt.Errorf("%s=%q: %w", EnvPrometheusBind, e.PrometheusBind, err)
		}
	}
	if e.ShutdownDrainSeconds <= 0 {
		return fmt.Errorf("%s=%d: %w: must be positive", EnvShutdownDrainSeconds, e.ShutdownDrainSeconds, ErrInvalidEnv)
	}
	if e.NATSStashThresholdBytes <= 0 {
		return fmt.Errorf("%s=%d: %w: must be positive", EnvNATSStashThresholdBytes, e.NATSStashThresholdBytes, ErrInvalidEnv)
	}
	if e.NATSPublishQueueSize <= 0 {
		return fmt.Errorf("%s=%d: %w: must be positive", EnvNATSPublishQueueSize, e.NATSPublishQueueSize, ErrInvalidEnv)
	}
	if e.RulesMaxGroupDepth < 1 || e.RulesMaxGroupDepth > MaxRulesMaxGroupDepth {
		return fmt.Errorf("%s=%d: %w: must be in [1, %d]", EnvRulesMaxGroupDepth, e.RulesMaxGroupDepth, ErrInvalidEnv, MaxRulesMaxGroupDepth)
	}
	switch strings.ToLower(strings.TrimSpace(e.LogLevel)) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("%s=%q: %w", EnvLogLevel, e.LogLevel, ErrUnknownLogLevel)
	}
	switch strings.ToLower(strings.TrimSpace(e.LogFormat)) {
	case "json", "text":
	default:
		return fmt.Errorf("%s=%q: %w", EnvLogFormat, e.LogFormat, ErrUnknownLogFormat)
	}
	switch strings.ToLower(strings.TrimSpace(e.OTLPProtocol)) {
	case "grpc", "http/protobuf":
	default:
		return fmt.Errorf("%s=%q: %w", EnvOTLPProtocol, e.OTLPProtocol, ErrUnknownOTLPProtocol)
	}
	return nil
}

// envString reads name and returns its trimmed value, or fallback when
// the var is unset or empty after trimming.
func envString(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

// envInt reads name and parses it as a base-10 int. An empty value
// returns fallback. A non-numeric value wraps ErrInvalidEnv.
func envInt(name string, fallback int) (int, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s=%q: %w: %w", name, v, ErrInvalidEnv, err)
	}
	return n, nil
}

// validateBind asserts host:port shape with a numeric port. The host
// component is allowed to be empty (":8585" binds all interfaces).
func validateBind(bind string) error {
	host, port, err := net.SplitHostPort(bind)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidBind, err)
	}
	if host == "" && port == "" {
		return ErrInvalidBind
	}
	if _, perr := strconv.Atoi(port); perr != nil {
		return fmt.Errorf("%w: port not numeric", ErrInvalidBind)
	}
	return nil
}
