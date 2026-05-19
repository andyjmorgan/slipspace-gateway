package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

// CLI-local aliases for the canonical env var name and default. Exposed so
// the resolveConfigDir helper (and its tests) stay decoupled from changes to
// the underlying constant names inside internal/config.
const (
	configDirEnv     = config.EnvConfigDir
	configDirDefault = config.DefaultConfigDir
)

func runConfigValidate(ctx context.Context, args []string, stdout, _ io.Writer) error {
	fs := flag.NewFlagSet("config validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("dir", "", "configuration directory (overrides $SLUICE_CONFIG_DIR for the file load)")

	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if fs.NArg() != 0 {
		return errUsage
	}

	env, err := config.LoadEnv()
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "FAIL: %s: %s\n", classifyConfigErr(err), err.Error())
		return errHandled
	}
	if err := env.Validate(); err != nil {
		_, _ = fmt.Fprintf(stdout, "FAIL: %s: %s\n", classifyConfigErr(err), err.Error())
		return errHandled
	}

	resolvedDir := env.ConfigDir
	if *dir != "" {
		resolvedDir = *dir
	}

	resolved, err := config.Load(ctx, resolvedDir)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "FAIL: %s: %s\n", classifyConfigErr(err), err.Error())
		return errHandled
	}

	_, _ = fmt.Fprintf(stdout,
		"OK: env %d vars resolved, %d configuration(s), %d api_keys, %d providers, %d routes\n",
		len(config.EnvVarNames()),
		len(resolved.Configurations),
		len(resolved.APIKeys),
		len(resolved.Providers),
		len(resolved.RouteIndex),
	)
	return nil
}

// resolveConfigDir picks the file-load directory in precedence order: the
// explicit --dir flag, then $SLUICE_CONFIG_DIR, then the documented default.
// Retained so the existing CLI unit tests still cover the resolution rules
// independently of the env-parse path.
func resolveConfigDir(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv(configDirEnv); env != "" {
		return env
	}
	return configDirDefault
}

func classifyConfigErr(err error) string {
	switch {
	case errors.Is(err, config.ErrInvalidEnv),
		errors.Is(err, config.ErrUnknownLogLevel),
		errors.Is(err, config.ErrUnknownLogFormat),
		errors.Is(err, config.ErrUnknownOTLPProtocol):
		return "invalid_env"
	case errors.Is(err, config.ErrEmptyDirectory):
		return "empty_directory"
	case errors.Is(err, config.ErrUnexpectedConfigFile):
		return "unexpected_config_file"
	case errors.Is(err, config.ErrWrongFileForKey):
		return "wrong_file_for_key"
	case errors.Is(err, config.ErrNoConfigurations):
		return "no_configurations"
	case errors.Is(err, config.ErrUnknownConfiguration):
		return "unknown_configuration"
	case errors.Is(err, config.ErrPathCollision):
		return "path_collision"
	case errors.Is(err, config.ErrPrefixRequiredEmpty):
		return "prefix_required_empty"
	case errors.Is(err, config.ErrInvalidBind):
		return "invalid_bind"
	case errors.Is(err, config.ErrParse):
		return "parse_error"
	default:
		return "other"
	}
}
