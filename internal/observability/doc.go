// Package observability wires the gateway's metrics, traces, OTel
// logs/events, structured logging, and per-request context propagation.
//
// Metrics flow through an OpenTelemetry MeterProvider that can fan out to
// a Prometheus scrape endpoint, an OTLP push exporter, or both. Both
// readers may be enabled simultaneously. A ManualReader is always
// attached regardless of exporter configuration — it feeds the
// snapshotter behind the admin dashboard — so the MeterProvider is
// always a real SDK provider and callers always receive valid instrument
// handles without nil checks. The Snapshotter exposes those manual
// reads as a windowed sample API for the admin dashboard. The tracer
// and logger providers, by contrast, are no-op unless
// SLIPSPACE_OTLP_ENDPOINT is set; they sit behind Provider.Tracer() and
// Provider.EventLogger(), which carry the gen_ai spans and the
// gen_ai.client.inference.operation.details events.
//
// Logging uses log/slog with a JSON or text handler. The per-request
// logger is enriched with the standard fields documented in the
// Telemetry Strategy note and stashed on context.Context via
// WithLogger / FromContext.
//
// This package owns telemetry only. End-of-pipeline event reporting
// (request / response capture) lives in internal/spool and the
// per-destination connectors under internal/connector — intentionally
// a separate channel.
package observability
