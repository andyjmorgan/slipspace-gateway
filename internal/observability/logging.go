package observability

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Standard structured-log field keys. They are exported so middleware in
// other packages can attach values without duplicating the constants.
const (
	LogFieldService       = "service"
	LogFieldVersion       = "version"
	LogFieldCorrelationID = "correlation_id"
	LogFieldSessionID     = "session_id"
	LogFieldAgentID       = "agent_id"
	LogFieldAPIKeyID      = "api_key_id"
	LogFieldConfiguration = "configuration"
	LogFieldProvider      = "provider"
	LogFieldProtocol      = "protocol"
	LogFieldModel         = "model"
)

// Supported logging formats.
const (
	LogFormatJSON = "json"
	LogFormatText = "text"
)

// NewLogger builds a slog.Logger with the requested format and level.
// An empty format defaults to JSON. An empty or unparseable level
// defaults to info. Output goes to os.Stdout; use NewLoggerWithWriter for
// tests that need to inspect the rendered output.
func NewLogger(format, level string) (*slog.Logger, error) {
	return NewLoggerWithWriter(os.Stdout, format, level)
}

// NewLoggerWithWriter is the test-friendly form of NewLogger.
func NewLoggerWithWriter(w io.Writer, format, level string) (*slog.Logger, error) {
	if w == nil {
		return nil, fmt.Errorf("observability: writer is required")
	}

	lvl, err := parseLevel(level)
	if err != nil {
		return nil, err
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", LogFormatJSON:
		handler = slog.NewJSONHandler(w, opts)
	case LogFormatText:
		handler = slog.NewTextHandler(w, opts)
	default:
		return nil, fmt.Errorf("observability: unsupported log format %q", format)
	}

	return slog.New(handler), nil
}

// EnrichLogger returns a logger that emits the service and version on
// every record. Callers further enrich per-request via WithLogger.
func EnrichLogger(l *slog.Logger, service, version string) *slog.Logger {
	if l == nil {
		l = slog.Default()
	}
	return l.With(
		slog.String(LogFieldService, service),
		slog.String(LogFieldVersion, version),
	)
}

// parseLevel maps the canonical "debug" / "info" / "warn" / "error"
// spellings to slog.Level. An empty string is treated as info.
func parseLevel(s string) (slog.Level, error) {
	if strings.TrimSpace(s) == "" {
		return slog.LevelInfo, nil
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.ToUpper(strings.TrimSpace(s)))); err != nil {
		return slog.LevelInfo, fmt.Errorf("observability: parse log level %q: %w", s, err)
	}
	return lvl, nil
}
