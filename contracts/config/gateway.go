// Package config defines the public, on-disk YAML schema for the Sluice
// gateway. These types are intentionally exported so control-plane code and
// downstream SDKs can consume them without importing internal packages.
package config

// GatewayConfig is the top-level `gateway` block from gateway.yaml.
type GatewayConfig struct {
	HTTP HTTPConfig `yaml:"http" json:"http"`

	Shutdown ShutdownConfig `yaml:"shutdown" json:"shutdown"`

	Reporting ReportingConfig `yaml:"reporting" json:"reporting"`

	Observability ObservabilityConfig `yaml:"observability" json:"observability"`
}

// HTTPConfig configures the gateway's inbound HTTP listener.
type HTTPConfig struct {
	Bind string `yaml:"bind" json:"bind"`
}

// ShutdownConfig configures graceful shutdown behaviour.
type ShutdownConfig struct {
	DrainTimeoutSeconds int `yaml:"drain_timeout_seconds" json:"drain_timeout_seconds"`
}

// ReportingConfig configures NATS-backed event reporting.
type ReportingConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`

	NATS NATSConfig `yaml:"nats" json:"nats"`
}

// NATSConfig configures the NATS publisher used for end-of-pipeline events.
type NATSConfig struct {
	URL string `yaml:"url" json:"url"`

	Stream string `yaml:"stream" json:"stream"`

	Bucket string `yaml:"bucket" json:"bucket"`

	StashThresholdBytes int `yaml:"stash_threshold_bytes" json:"stash_threshold_bytes"`

	StreamMaxAgeMinutes int `yaml:"stream_max_age_minutes" json:"stream_max_age_minutes"`

	BucketTTLMinutes int `yaml:"bucket_ttl_minutes" json:"bucket_ttl_minutes"`

	PublishQueueSize int `yaml:"publish_queue_size" json:"publish_queue_size"`
}

// ObservabilityConfig configures Prometheus, OTLP, and logging sinks.
type ObservabilityConfig struct {
	Prometheus PrometheusConfig `yaml:"prometheus" json:"prometheus"`

	OTLP OTLPConfig `yaml:"otlp" json:"otlp"`

	Logging LoggingConfig `yaml:"logging" json:"logging"`
}

// PrometheusConfig configures the Prometheus scrape endpoint.
type PrometheusConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`

	Bind string `yaml:"bind" json:"bind"`
}

// OTLPConfig configures the OpenTelemetry OTLP exporter.
type OTLPConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`

	Endpoint string `yaml:"endpoint" json:"endpoint"`

	Protocol string `yaml:"protocol" json:"protocol"`
}

// LoggingConfig configures the slog handler.
type LoggingConfig struct {
	Format string `yaml:"format" json:"format"`

	Level string `yaml:"level" json:"level"`
}
