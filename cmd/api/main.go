package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/version"
)

const (
	binaryName      = "api"
	bindAddr        = "0.0.0.0:8484"
	shutdownTimeout = 5 * time.Second
)

func main() {
	os.Exit(mainErr())
}

func mainErr() int {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s %s\n", binaryName, version.Version)
		return 0
	}

	slog.SetDefault(newLogger(os.Getenv("LOG_LEVEL")))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("api exited with error", "err", err)
		return 1
	}
	return 0
}

func run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", notImplemented)

	srv := &http.Server{
		Addr:              bindAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", bindAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutting down", "signal", ctx.Err())
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("api: serve: %w", err)
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// Detached from the cancelled signal context on purpose — the shutdown
	// budget must outlive the SIGTERM that triggered it.
	if err := srv.Shutdown(shutdownCtx); err != nil { //nolint:contextcheck
		return fmt.Errorf("api: shutdown: %w", err)
	}
	return nil
}

func notImplemented(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = w.Write([]byte("control plane not implemented until v1.1\n"))
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
