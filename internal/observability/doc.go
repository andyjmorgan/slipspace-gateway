// Package observability wires the gateway's metrics, logging, and
// per-request context propagation.
//
// Metrics flow through an OpenTelemetry MeterProvider that can fan out to
// a Prometheus scrape endpoint, an OTLP push exporter, or both. Both
// readers may be enabled simultaneously; when neither is configured the
// package installs a no-op MeterProvider so callers always receive valid
// instrument handles without nil checks.
//
// Logging uses log/slog with a JSON or text handler. The per-request
// logger is enriched with the standard fields documented in the
// Telemetry Strategy note and stashed on context.Context via
// WithLogger / FromContext.
//
// This package owns telemetry only. NATS-backed event reporting lives in
// internal/bus and is intentionally a separate channel.
package observability
