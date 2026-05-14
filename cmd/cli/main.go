package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/andyjmorgan/sluice-gateway/internal/version"
)

const binaryName = "cli"

var errUsage = errors.New("usage")

func main() {
	os.Exit(mainErr())
}

func mainErr() int {
	versionFlags := flag.NewFlagSet(binaryName, flag.ContinueOnError)
	versionFlags.SetOutput(io.Discard)
	showVersion := versionFlags.Bool("version", false, "print version and exit")
	_ = versionFlags.Parse(os.Args[1:])

	if *showVersion {
		fmt.Printf("%s %s\n", binaryName, version.Version)
		return 0
	}

	slog.SetDefault(newLogger(os.Getenv("LOG_LEVEL")))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, errUsage) {
			printUsage(os.Stderr)
			return 2
		}
		slog.Error("cli exited with error", "err", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errUsage
	}

	switch args[0] {
	case "key":
		return runKey(ctx, args[1:])
	default:
		return errUsage
	}
}

func runKey(_ context.Context, args []string) error {
	if len(args) == 0 {
		return errUsage
	}

	switch args[0] {
	case "new":
		return runKeyNew(args[1:])
	default:
		return errUsage
	}
}

func runKeyNew(args []string) error {
	fs := flag.NewFlagSet("key new", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	label := fs.String("label", "", "human-readable label for the new key")

	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if fs.NArg() != 0 {
		return errUsage
	}

	_ = label
	fmt.Println("ak_live_PLACEHOLDER (key generation lands in a later task)")
	return nil
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage: cli <command> [args]")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "commands:")
	_, _ = fmt.Fprintln(w, "  key new [--label <name>]   generate a new API key (placeholder)")
	_, _ = fmt.Fprintln(w, "  --version                  print version and exit")
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
