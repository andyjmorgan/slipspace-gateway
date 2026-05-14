// Package config defines the public, on-disk YAML schema for the Sluice
// gateway. These types are intentionally exported so control-plane code and
// downstream SDKs can consume them without importing internal packages.
package config

// GatewayConfig is the top-level `gateway` block from gateway.yaml.
type GatewayConfig struct {
	// HTTP configures the inbound listener that accepts client traffic.
	HTTP HTTPConfig `yaml:"http" json:"http"`

	// Shutdown controls in-flight request draining on SIGTERM/SIGINT.
	Shutdown ShutdownConfig `yaml:"shutdown" json:"shutdown"`

	// Reporting controls the NATS side-channel used for end-of-pipeline events.
	Reporting ReportingConfig `yaml:"reporting" json:"reporting"`

	// Observability controls the metrics, traces, and logging sinks.
	Observability ObservabilityConfig `yaml:"observability" json:"observability"`
}

// HTTPConfig configures the gateway's inbound HTTP listener.
type HTTPConfig struct {
	// Bind is the host:port the listener binds to (e.g., "0.0.0.0:8080").
	Bind string `yaml:"bind" json:"bind"`
}

// ShutdownConfig configures graceful shutdown behaviour.
type ShutdownConfig struct {
	// DrainTimeoutSeconds bounds how long the server waits for in-flight
	// requests to complete before force-closing on shutdown.
	DrainTimeoutSeconds int `yaml:"drain_timeout_seconds" json:"drain_timeout_seconds"`
}

// ReportingConfig configures NATS-backed event reporting.
type ReportingConfig struct {
	// Enabled toggles NATS publication of pipeline events. When false the
	// publisher is wired but every Publish call is a no-op.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// NATS configures the publisher's connection and stream parameters.
	NATS NATSConfig `yaml:"nats" json:"nats"`
}

// NATSConfig configures the NATS publisher used for end-of-pipeline events.
type NATSConfig struct {
	// URL is the NATS server connection string.
	URL string `yaml:"url" json:"url"`

	// Stream is the JetStream stream events are published to.
	Stream string `yaml:"stream" json:"stream"`

	// Bucket is the JetStream object-store bucket used to stash large
	// payloads that exceed StashThresholdBytes.
	Bucket string `yaml:"bucket" json:"bucket"`

	// StashThresholdBytes is the envelope size at which the publisher stashes
	// the payload into the object store and publishes a reference instead.
	// See the "NATS Reporting (Envelope Pattern)" design note for the 768 KB
	// rationale.
	StashThresholdBytes int `yaml:"stash_threshold_bytes" json:"stash_threshold_bytes"`

	// StreamMaxAgeMinutes is the retention bound on the JetStream stream.
	StreamMaxAgeMinutes int `yaml:"stream_max_age_minutes" json:"stream_max_age_minutes"`

	// BucketTTLMinutes is the time-to-live applied to stashed payloads in the
	// object store; it must outlive the consumer's worst-case fetch window.
	BucketTTLMinutes int `yaml:"bucket_ttl_minutes" json:"bucket_ttl_minutes"`

	// PublishQueueSize bounds the in-process publish buffer. When full the
	// publisher drops rather than blocking — see the "reporting never blocks
	// the request path" invariant in CLAUDE.md.
	PublishQueueSize int `yaml:"publish_queue_size" json:"publish_queue_size"`
}

// ObservabilityConfig configures Prometheus, OTLP, and logging sinks.
type ObservabilityConfig struct {
	// Prometheus controls the local scrape endpoint.
	Prometheus PrometheusConfig `yaml:"prometheus" json:"prometheus"`

	// OTLP controls the OpenTelemetry push exporter.
	OTLP OTLPConfig `yaml:"otlp" json:"otlp"`

	// Logging controls the slog JSON handler.
	Logging LoggingConfig `yaml:"logging" json:"logging"`
}

// PrometheusConfig configures the Prometheus scrape endpoint.
type PrometheusConfig struct {
	// Enabled toggles whether the gateway exposes /metrics at all.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Bind is the host:port the scrape endpoint listens on; conventionally a
	// separate port from the data-plane listener so it can be firewalled
	// independently.
	Bind string `yaml:"bind" json:"bind"`
}

// OTLPConfig configures the OpenTelemetry OTLP exporter.
type OTLPConfig struct {
	// Enabled toggles the OTLP push exporter.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Endpoint is the collector URL (e.g., "http://otel-collector:4317").
	Endpoint string `yaml:"endpoint" json:"endpoint"`

	// Protocol selects the OTLP transport ("grpc" or "http/protobuf").
	Protocol string `yaml:"protocol" json:"protocol"`
}

// LoggingConfig configures the slog handler.
type LoggingConfig struct {
	// Format selects the slog handler ("json" or "text"). Production deploys
	// should always be "json" so logs are machine-parseable.
	Format string `yaml:"format" json:"format"`

	// Level is the minimum slog level emitted ("debug", "info", "warn", "error").
	Level string `yaml:"level" json:"level"`
}
