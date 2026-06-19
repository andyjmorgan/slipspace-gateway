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

	"github.com/andyjmorgan/slipspace-gateway/internal/version"
)

const binaryName = "cli"

var (
	errUsage   = errors.New("usage")
	errHandled = errors.New("handled")
)

func main() {
	os.Exit(mainErr())
}

func mainErr() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return run(ctx, os.Args[1:], os.Stdout, os.Stderr)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	versionFlags := flag.NewFlagSet(binaryName, flag.ContinueOnError)
	versionFlags.SetOutput(io.Discard)
	showVersion := versionFlags.Bool("version", false, "print version and exit")
	_ = versionFlags.Parse(args)

	if *showVersion {
		_, _ = fmt.Fprintf(stdout, "%s %s\n", binaryName, version.Version)
		return 0
	}

	slog.SetDefault(newLogger(stderr, os.Getenv("LOG_LEVEL")))

	if err := dispatch(ctx, args, stdout, stderr); err != nil {
		switch {
		case errors.Is(err, errUsage):
			printUsage(stderr)
			return 2
		case errors.Is(err, errHandled):
			return 1
		default:
			_, _ = fmt.Fprintln(stderr, err.Error())
			return 1
		}
	}
	return 0
}

func dispatch(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}

	switch args[0] {
	case "key":
		return runKey(ctx, args[1:], stdout, stderr)
	case "config":
		return runConfig(ctx, args[1:], stdout, stderr)
	default:
		return errUsage
	}
}

func runKey(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}

	switch args[0] {
	case "new":
		return runKeyNew(ctx, args[1:], stdout, stderr)
	default:
		return errUsage
	}
}

func runConfig(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}

	switch args[0] {
	case "validate":
		return runConfigValidate(ctx, args[1:], stdout, stderr)
	default:
		return errUsage
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage: cli <command> [args]")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "commands:")
	_, _ = fmt.Fprintln(w, "  key new [--label <name>] [--configuration <name>] [--prefix <prefix>]")
	_, _ = fmt.Fprintln(w, "      generate a random API key and print a YAML snippet")
	_, _ = fmt.Fprintln(w, "  config validate [--dir <path>]")
	_, _ = fmt.Fprintln(w, "      validate a slipspace configuration directory")
	_, _ = fmt.Fprintln(w, "  --version")
	_, _ = fmt.Fprintln(w, "      print version and exit")
}

func newLogger(w io.Writer, levelEnv string) *slog.Logger {
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

	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(handler).With(
		"service", binaryName,
		"version", version.Version,
	)
}
