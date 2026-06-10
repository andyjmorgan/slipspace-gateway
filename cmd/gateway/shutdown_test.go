package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// TestWatchShutdownSignal proves the gateway emits the "shutting down"
// forensic marker — with the service/version enrichment — the moment the
// signal context is cancelled, and not before. The June 2026 binary-swap
// incident exited with no shutdown trace at all; this line is the contract
// that a signal-driven exit is always self-announcing.
func TestWatchShutdownSignal(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := observability.EnrichLogger(
		slog.New(slog.NewJSONHandler(&buf, nil)), "gateway", "v9.9.9-test")

	ctx, cancel := context.WithCancel(context.Background())
	done := watchShutdownSignal(ctx, logger, 7*time.Second)

	select {
	case <-done:
		t.Fatal("shutdown line emitted before the signal")
	case <-time.After(20 * time.Millisecond):
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown line not emitted after signal")
	}

	out := buf.String()
	for _, want := range []string{`"shutting down"`, `"version":"v9.9.9-test"`, `"drain_timeout":"7s"`} {
		if !strings.Contains(out, want) {
			t.Errorf("shutdown log missing %s: %q", want, out)
		}
	}
}
