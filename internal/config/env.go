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
	DefaultHTTPBind             = ":8585"
	DefaultShutdownDrainSeconds = 300
	DefaultLogFormat            = "json"
	DefaultLogLevel             = "info"
	DefaultOTLPProtocol         = "grpc"
	DefaultConfigDir            = "/etc/sluice/"

	// DefaultSpoolRoot is the on-disk root for the connector spool.
	// Operators point this at a PVC mount in production so the spool
	// survives process restarts; tests / dev override.
	DefaultSpoolRoot = "/var/lib/sluice/spool"

	// DefaultRulesMaxGroupDepth caps recursion through nested RuleGroup
	// children during evaluation. The cap is a guardrail against
	// pathological YAML that would otherwise blow the stack; 8 fits
	// every realistic operator-authored policy with headroom.
	DefaultRulesMaxGroupDepth = 8

	// DefaultAdminSnapshotInterval is how often the admin console's
	// metric snapshotter reads the in-process registry. 5 minutes
	// gives the 24h dashboard 288 sample points, matching the design's
	// chart resolution. E2E tests override via SLUICE_ADMIN_SNAPSHOT_INTERVAL.
	DefaultAdminSnapshotIntervalMs = 300_000

	// DefaultAdminLiveFeedCapacity sizes the in-process ring of completed
	// requests that backs the admin console's live-messages pane. 100 is
	// deliberately small: a 1M-token-context request body can be 4 MB+,
	// and the pane is honest about the fact that this is a few-minute
	// live tail, not an audit log. Operators set
	// SLUICE_ADMIN_LIVE_FEED_CAPACITY=0 to disable the pane entirely.
	DefaultAdminLiveFeedCapacity = 100

	// DefaultAdminLiveFeedBodyBytes is the total byte budget of the
	// in-process body LRU that backs GET /admin/api/v1/messages/{id}/body.
	// 200 MB holds ~25 max-sized bodies or hundreds of typical-sized
	// ones. Set to 0 to disable body capture entirely; the live-tail
	// pane still renders metadata.
	DefaultAdminLiveFeedBodyBytes = 200 * 1024 * 1024

	// DefaultAdminLiveFeedBodyMaxBytes caps per-body capture before
	// truncation. 8 MB fits OpenAI vision payloads and most Claude /
	// Gemini messages without surprises; bodies above the cap are
	// stored head-only with a truncated flag so the operator can see
	// something rather than nothing.
	DefaultAdminLiveFeedBodyMaxBytes = 8 * 1024 * 1024

	// MaxRulesMaxGroupDepth is the upper bound accepted by Validate.
	// Beyond this, the cost of authoring + evaluating a tree this deep
	// outweighs any expressive gain; a flat priority chain is clearer.
	MaxRulesMaxGroupDepth = 64
)

// SLUICE_* env var names. Exported so callers (CLI validator, tests,
// integration harness) can read or set them by symbolic name.
const (
	EnvHTTPBind                  = "SLUICE_HTTP_BIND"
	EnvShutdownDrainSeconds      = "SLUICE_SHUTDOWN_DRAIN_SECONDS"
	EnvLogFormat                 = "SLUICE_LOG_FORMAT"
	EnvLogLevel                  = "SLUICE_LOG_LEVEL"
	EnvPrometheusBind            = "SLUICE_PROMETHEUS_BIND"
	EnvOTLPEndpoint              = "SLUICE_OTLP_ENDPOINT"
	EnvOTLPProtocol              = "SLUICE_OTLP_PROTOCOL"
	EnvConfigDir                 = "SLUICE_CONFIG_DIR"
	EnvSpoolRoot                 = "SLUICE_SPOOL_ROOT"
	EnvRulesMaxGroupDepth        = "SLUICE_RULES_MAX_GROUP_DEPTH"
	EnvAdminSnapshotIntervalMs   = "SLUICE_ADMIN_SNAPSHOT_INTERVAL_MS"
	EnvAdminLiveFeedCapacity     = "SLUICE_ADMIN_LIVE_FEED_CAPACITY"
	EnvAdminLiveFeedBodyBytes    = "SLUICE_ADMIN_LIVE_FEED_BODY_BYTES"
	EnvAdminLiveFeedBodyMaxBytes = "SLUICE_ADMIN_LIVE_FEED_BODY_MAX_BYTES"
	EnvRedactExtraHeaders        = "SLUICE_REDACT_EXTRA_HEADERS"
	EnvSessionIDHeaders          = "SLUICE_SESSION_ID_HEADERS"
	EnvExternalURL               = "SLUICE_EXTERNAL_URL"
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
	EnvConfigDir,
	EnvSpoolRoot,
	EnvRulesMaxGroupDepth,
	EnvAdminSnapshotIntervalMs,
	EnvAdminLiveFeedCapacity,
	EnvAdminLiveFeedBodyBytes,
	EnvAdminLiveFeedBodyMaxBytes,
	EnvRedactExtraHeaders,
	EnvSessionIDHeaders,
	EnvExternalURL,
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

	// ConfigDir holds the policy + providers YAML directory loaded by
	// Load. The CLI's --dir flag overrides this for the file load only.
	ConfigDir string

	// SpoolRoot is the on-disk root for the connector spool. The Spool
	// constructs records/<connector>/{active,sealed,uploading,
	// deadletter,quarantine}/ under this path. Operators point at a
	// PVC mount in production so segments survive process restart.
	SpoolRoot string

	// RulesMaxGroupDepth caps recursive descent through nested
	// RuleGroup children during evaluation. Operator-authored policies
	// rarely need more than 3-4 levels; the cap is a guardrail against
	// pathological YAML triggering stack overflow in the evaluator.
	RulesMaxGroupDepth int

	// AdminSnapshotIntervalMs is the interval the admin console's
	// metric snapshotter uses between Collect calls, in milliseconds.
	// Production defaults to 5 minutes; e2e tests drop this to ~200ms
	// so the dashboard reflects real traffic in test wall-clock.
	AdminSnapshotIntervalMs int

	// AdminLiveFeedCapacity sizes the in-process ring of completed
	// requests that backs the admin console's live-messages pane.
	// Zero disables the ring (and the messages endpoints).
	AdminLiveFeedCapacity int

	// AdminLiveFeedBodyBytes is the total byte budget of the body LRU
	// that backs the per-event body endpoint. Zero disables body
	// capture; the live-tail pane still renders metadata.
	AdminLiveFeedBodyBytes int

	// AdminLiveFeedBodyMaxBytes caps per-body capture before
	// truncation. Bodies above the cap are stored head-only with a
	// truncated flag. Ignored when AdminLiveFeedBodyBytes is zero.
	AdminLiveFeedBodyMaxBytes int

	// RedactExtraHeaders is the operator-supplied addendum to the
	// built-in sensitive-header substring matcher used by
	// internal/headers.Redactor. Each entry is a lowercased substring
	// matched against inbound header names; any header containing one
	// of these has its value replaced by "[REDACTED]" before it lands
	// in the live-messages ring, connector Records, or proxy log
	// envelopes. The built-in list (auth / api-key / token / cookie /
	// secret / sluice-identity) is always active; this slice extends
	// it for environment-specific headers (internal tracing IDs that
	// carry tenant identifiers, custom auth schemes, etc.).
	RedactExtraHeaders []string

	// SessionIDHeaders is the operator-supplied addendum to the built-in
	// session-id fallback chain (observability.DefaultSessionIDHeaders).
	// Each entry is a header name appended, in order, after the shipped
	// defaults; the authoritative X-Sluice-Session-Id is always tried
	// first regardless. Lets an operator bundle a custom client's
	// conversation header (e.g. X-Acme-Conversation-Id) without a code
	// change. A header also present in RedactExtraHeaders is skipped
	// during resolution so a promoted session id can't bypass redaction.
	SessionIDHeaders []string

	// ExternalURL is the gateway's externally reachable base URL (e.g.
	// https://sluice.example.com), used to resolve the {external_url}
	// template reference in response-side body rewrites — chiefly
	// rebasing provider-returned URLs (Anthropic batches results_url)
	// back through the gateway. Empty leaves {external_url} unresolved,
	// dropping any rewrite that depends on it.
	ExternalURL string
}

// PrometheusEnabled reports whether the Prometheus scrape listener is
// configured. False when PrometheusBind is empty.
func (e *ServerEnv) PrometheusEnabled() bool { return e.PrometheusBind != "" }

// OTLPEnabled reports whether the OTLP exporter is configured. False
// when OTLPEndpoint is empty.
func (e *ServerEnv) OTLPEnabled() bool { return e.OTLPEndpoint != "" }

// LiveFeedEnabled reports whether the admin live-messages ring is wired.
// False when AdminLiveFeedCapacity is zero (or negative, which Validate
// rejects). When false, cmd/gateway skips constructing the ring and the
// admin mux degrades the /messages/* endpoints to 503.
func (e *ServerEnv) LiveFeedEnabled() bool { return e.AdminLiveFeedCapacity > 0 }

// LiveFeedBodiesEnabled reports whether the admin body LRU is wired.
// False when AdminLiveFeedBodyBytes is zero. Body capture also requires
// LiveFeedEnabled() — without a ring, no event_id exists to key bodies
// against.
func (e *ServerEnv) LiveFeedBodiesEnabled() bool {
	return e.LiveFeedEnabled() && e.AdminLiveFeedBodyBytes > 0
}

// LoadEnv parses the SLUICE_* env vars and returns a populated
// ServerEnv. Absent or empty vars fall back to the documented defaults.
// Parse failures wrap ErrInvalidEnv; validation against the allowed
// enum sets is deferred to Validate.
func LoadEnv() (*ServerEnv, error) {
	drain, err := envInt(EnvShutdownDrainSeconds, DefaultShutdownDrainSeconds)
	if err != nil {
		return nil, err
	}
	groupDepth, err := envInt(EnvRulesMaxGroupDepth, DefaultRulesMaxGroupDepth)
	if err != nil {
		return nil, err
	}
	snapInterval, err := envInt(EnvAdminSnapshotIntervalMs, DefaultAdminSnapshotIntervalMs)
	if err != nil {
		return nil, err
	}
	liveFeedCap, err := envInt(EnvAdminLiveFeedCapacity, DefaultAdminLiveFeedCapacity)
	if err != nil {
		return nil, err
	}
	bodyBytes, err := envInt(EnvAdminLiveFeedBodyBytes, DefaultAdminLiveFeedBodyBytes)
	if err != nil {
		return nil, err
	}
	bodyMaxBytes, err := envInt(EnvAdminLiveFeedBodyMaxBytes, DefaultAdminLiveFeedBodyMaxBytes)
	if err != nil {
		return nil, err
	}

	return &ServerEnv{
		HTTPBind:                  envString(EnvHTTPBind, DefaultHTTPBind),
		ShutdownDrainSeconds:      drain,
		LogFormat:                 envString(EnvLogFormat, DefaultLogFormat),
		LogLevel:                  envString(EnvLogLevel, DefaultLogLevel),
		PrometheusBind:            envString(EnvPrometheusBind, ""),
		OTLPEndpoint:              envString(EnvOTLPEndpoint, ""),
		OTLPProtocol:              envString(EnvOTLPProtocol, DefaultOTLPProtocol),
		ConfigDir:                 envString(EnvConfigDir, DefaultConfigDir),
		SpoolRoot:                 envString(EnvSpoolRoot, DefaultSpoolRoot),
		RulesMaxGroupDepth:        groupDepth,
		AdminSnapshotIntervalMs:   snapInterval,
		AdminLiveFeedCapacity:     liveFeedCap,
		AdminLiveFeedBodyBytes:    bodyBytes,
		AdminLiveFeedBodyMaxBytes: bodyMaxBytes,
		RedactExtraHeaders:        envCSVList(EnvRedactExtraHeaders),
		SessionIDHeaders:          envCSVList(EnvSessionIDHeaders),
		ExternalURL:               envString(EnvExternalURL, ""),
	}, nil
}

// envCSVList parses a comma-separated env var into a normalized slice.
// Whitespace is trimmed from each entry, empty entries are dropped,
// and the slice preserves operator-supplied order so callers can rely
// on first-match semantics if they ever need them. Returns nil when
// the env var is unset or contains no non-empty entries.
func envCSVList(name string) []string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
	if e.SpoolRoot == "" {
		return fmt.Errorf("%s: %w: must be non-empty", EnvSpoolRoot, ErrInvalidEnv)
	}
	if e.RulesMaxGroupDepth < 1 || e.RulesMaxGroupDepth > MaxRulesMaxGroupDepth {
		return fmt.Errorf("%s=%d: %w: must be in [1, %d]", EnvRulesMaxGroupDepth, e.RulesMaxGroupDepth, ErrInvalidEnv, MaxRulesMaxGroupDepth)
	}
	if e.AdminSnapshotIntervalMs <= 0 {
		return fmt.Errorf("%s=%d: %w: must be positive", EnvAdminSnapshotIntervalMs, e.AdminSnapshotIntervalMs, ErrInvalidEnv)
	}
	if e.AdminLiveFeedCapacity < 0 {
		return fmt.Errorf("%s=%d: %w: must be non-negative (0 disables)", EnvAdminLiveFeedCapacity, e.AdminLiveFeedCapacity, ErrInvalidEnv)
	}
	if e.AdminLiveFeedBodyBytes < 0 {
		return fmt.Errorf("%s=%d: %w: must be non-negative (0 disables)", EnvAdminLiveFeedBodyBytes, e.AdminLiveFeedBodyBytes, ErrInvalidEnv)
	}
	if e.AdminLiveFeedBodyMaxBytes < 0 {
		return fmt.Errorf("%s=%d: %w: must be non-negative", EnvAdminLiveFeedBodyMaxBytes, e.AdminLiveFeedBodyMaxBytes, ErrInvalidEnv)
	}
	if e.AdminLiveFeedBodyBytes > 0 && e.AdminLiveFeedBodyMaxBytes == 0 {
		return fmt.Errorf("%s must be > 0 when %s is non-zero: %w", EnvAdminLiveFeedBodyMaxBytes, EnvAdminLiveFeedBodyBytes, ErrInvalidEnv)
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
