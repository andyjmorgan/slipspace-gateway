package observability_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
)

func TestNewLoggerWithWriter_JSONFormat(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger, err := observability.NewLoggerWithWriter(&buf, "json", "debug")
	if err != nil {
		t.Fatalf("NewLoggerWithWriter: %v", err)
	}
	logger.Info("hello", "k", "v")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", buf.String(), err)
	}
	if record["msg"] != "hello" || record["k"] != "v" {
		t.Errorf("unexpected record: %v", record)
	}
}

func TestNewLoggerWithWriter_TextFormat(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger, err := observability.NewLoggerWithWriter(&buf, "TEXT", "info")
	if err != nil {
		t.Fatalf("NewLoggerWithWriter: %v", err)
	}
	logger.Info("hi", "k", "v")

	if !strings.Contains(buf.String(), "msg=hi") {
		t.Errorf("text handler not used: %q", buf.String())
	}
	if strings.HasPrefix(strings.TrimSpace(buf.String()), "{") {
		t.Errorf("expected text output, got JSON: %q", buf.String())
	}
}

func TestNewLoggerWithWriter_DefaultsToJSON(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger, err := observability.NewLoggerWithWriter(&buf, "", "")
	if err != nil {
		t.Fatalf("NewLoggerWithWriter: %v", err)
	}
	logger.Info("x")

	if !strings.HasPrefix(strings.TrimSpace(buf.String()), "{") {
		t.Errorf("expected JSON default, got %q", buf.String())
	}
}

func TestNewLoggerWithWriter_RejectsUnknownFormat(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if _, err := observability.NewLoggerWithWriter(&buf, "yaml", ""); err == nil {
		t.Fatalf("expected error for unsupported format")
	}
}

func TestNewLoggerWithWriter_RejectsNilWriter(t *testing.T) {
	t.Parallel()

	if _, err := observability.NewLoggerWithWriter(nil, "json", ""); err == nil {
		t.Fatalf("expected error for nil writer")
	}
}

func TestNewLoggerWithWriter_LevelParsing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		level   string
		ok      bool
		emitDbg bool
	}{
		{"", true, false},
		{"debug", true, true},
		{"DEBUG", true, true},
		{"info", true, false},
		{"warn", true, false},
		{"error", true, false},
		{"loud", false, false},
	}

	for _, tc := range cases {
		t.Run(tc.level, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			logger, err := observability.NewLoggerWithWriter(&buf, "json", tc.level)
			if tc.ok != (err == nil) {
				t.Fatalf("level %q: err = %v, want ok=%v", tc.level, err, tc.ok)
			}
			if !tc.ok {
				return
			}
			logger.Debug("dbg")
			if tc.emitDbg && buf.Len() == 0 {
				t.Errorf("debug level should emit debug records")
			}
			if !tc.emitDbg && buf.Len() != 0 {
				t.Errorf("non-debug level emitted debug record: %q", buf.String())
			}
		})
	}
}

func TestNewLogger_WritesToStdout(t *testing.T) {
	t.Parallel()

	logger, err := observability.NewLogger("json", "info")
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	if logger == nil {
		t.Fatalf("nil logger")
	}
}

func TestEnrichLogger_AttachesServiceAndVersion(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger, err := observability.NewLoggerWithWriter(&buf, "json", "info")
	if err != nil {
		t.Fatalf("NewLoggerWithWriter: %v", err)
	}

	enriched := observability.EnrichLogger(logger, "sluice-gateway", "v0.1.0")
	enriched.Info("emit")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rec[observability.LogFieldService] != "sluice-gateway" {
		t.Errorf("missing service field: %v", rec)
	}
	if rec[observability.LogFieldVersion] != "v0.1.0" {
		t.Errorf("missing version field: %v", rec)
	}
}

func TestEnrichLogger_NilLoggerFallsBackToDefault(t *testing.T) {
	t.Parallel()

	enriched := observability.EnrichLogger(nil, "svc", "v")
	if enriched == nil {
		t.Fatalf("nil enriched logger")
	}
}
