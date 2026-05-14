package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
)

const (
	defaultKeyPrefix        = "sk_live_"
	defaultKeyConfiguration = "production"
	keyRandomBytes          = 32
)

func runKeyNew(_ context.Context, args []string, stdout, _ io.Writer) error {
	fs := flag.NewFlagSet("key new", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	label := fs.String("label", "", "human-readable label for the new key")
	configuration := fs.String("configuration", defaultKeyConfiguration, "configuration name the key references")
	prefix := fs.String("prefix", defaultKeyPrefix, "key prefix (e.g. sk_live_, sk_dev_)")

	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if fs.NArg() != 0 {
		return errUsage
	}

	key, err := generateKey(*prefix)
	if err != nil {
		return fmt.Errorf("cli: key new: %w", err)
	}

	writeKeyOutput(stdout, key, *label, *configuration)
	return nil
}

func generateKey(prefix string) (string, error) {
	buf := make([]byte, keyRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read crypto/rand: %w", err)
	}
	return prefix + hex.EncodeToString(buf), nil
}

func writeKeyOutput(w io.Writer, key, label, configuration string) {
	_, _ = fmt.Fprintln(w, key)
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "# yaml-snippet:")
	_, _ = fmt.Fprintln(w, "api_keys:")
	_, _ = fmt.Fprintf(w, "  - secret: %q\n", key)
	if label != "" {
		_, _ = fmt.Fprintf(w, "    name: %q\n", label)
	}
	_, _ = fmt.Fprintf(w, "    configuration: %s\n", configuration)
	_, _ = fmt.Fprintln(w, "    enabled: true")
}
