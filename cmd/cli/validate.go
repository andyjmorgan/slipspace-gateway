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

const (
	configDirEnv     = "SLUICE_CONFIG_DIR"
	configDirDefault = "/etc/sluice/"
)

func runConfigValidate(ctx context.Context, args []string, stdout, _ io.Writer) error {
	fs := flag.NewFlagSet("config validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("dir", "", "configuration directory (defaults to $SLUICE_CONFIG_DIR or /etc/sluice/)")

	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if fs.NArg() != 0 {
		return errUsage
	}

	resolvedDir := resolveConfigDir(*dir)

	resolved, err := config.Load(ctx, resolvedDir)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "FAIL: %s: %s\n", classifyConfigErr(err), err.Error())
		return errHandled
	}

	_, _ = fmt.Fprintf(stdout,
		"OK: %d configuration(s), %d api_keys, %d providers, %d routes\n",
		len(resolved.Configurations),
		len(resolved.APIKeys),
		len(resolved.Providers),
		len(resolved.RouteIndex),
	)
	return nil
}

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
	case errors.Is(err, config.ErrEndpointNotInProvider):
		return "endpoint_not_in_provider"
	case errors.Is(err, config.ErrMalformedAllowedEndpoint):
		return "malformed_allowed_endpoint"
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
