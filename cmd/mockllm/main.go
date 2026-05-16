package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/safego"
	"github.com/andyjmorgan/sluice-gateway/internal/version"
)

const (
	binaryName      = "mockllm"
	defaultPort     = 5555
	shutdownTimeout = 5 * time.Second
)

func main() {
	os.Exit(mainErr())
}

func mainErr() int {
	port := flag.Int("port", defaultPort, "TCP port to listen on")
	responsesPath := flag.String("responses", "", "optional path to a YAML or JSON file of canned responses")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s %s\n", binaryName, version.Version)
		return 0
	}

	slog.SetDefault(newLogger(os.Getenv("LOG_LEVEL")))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, *port, *responsesPath); err != nil {
		slog.Error("mockllm exited with error", "err", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, port int, responsesPath string) error {
	reg := newRegistry()

	if responsesPath != "" {
		loaded, err := loadResponsesFile(responsesPath)
		if err != nil {
			return fmt.Errorf("mockllm: load responses: %w", err)
		}
		reg.replace(loaded)
		slog.Info("loaded canned responses", "path", responsesPath, "count", len(loaded))
	}

	srv := newServer(reg)

	addr := net.JoinHostPort("0.0.0.0", strconv.Itoa(port))
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	safego.Go(ctx, "mockllm.serve", slog.Default(), nil, func() {
		slog.Info("listening", "addr", addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	})

	select {
	case <-ctx.Done():
		slog.Info("shutting down", "signal", ctx.Err())
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("mockllm: serve: %w", err)
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// Detached from the cancelled signal context on purpose — the shutdown
	// budget must outlive the SIGTERM that triggered it.
	if err := httpSrv.Shutdown(shutdownCtx); err != nil { //nolint:contextcheck
		return fmt.Errorf("mockllm: shutdown: %w", err)
	}
	return nil
}

func newLogger(levelEnv string) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(strings.TrimSpace(levelEnv)) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	case "", "info":
		level = slog.LevelInfo
	default:
		level = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	return slog.New(handler).With(
		"service", binaryName,
		"version", version.Version,
	)
}
