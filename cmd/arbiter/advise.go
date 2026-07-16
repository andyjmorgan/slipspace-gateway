package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/advise"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/registry"
)

// buildAdviseHandler constructs the routing-advisor handler from the advise
// config block: the judge's bearer credential is read from its file at
// startup, never carried inline in config. rubric is the effective judge
// prompt already resolved by advise.ResolveRubric (main also stashes it on
// the applied-config snapshot).
func buildAdviseHandler(cfg config.Config, rubric string, reg *registry.Registry, log *slog.Logger) (http.Handler, error) {
	keyRaw, err := os.ReadFile(cfg.Advise.Upstream.APIKeyFile) //nolint:gosec // operator-trusted config path
	if err != nil {
		return nil, fmt.Errorf("read api_key_file: %w", err)
	}
	apiKey := string(bytes.TrimSpace(keyRaw))
	if apiKey == "" {
		return nil, fmt.Errorf("api_key_file %q is empty", cfg.Advise.Upstream.APIKeyFile)
	}

	judge := advise.NewJudge(
		cfg.Advise.Upstream.BaseURL,
		apiKey,
		cfg.Advise.Upstream.Model,
		rubric,
		cfg.Advise.Candidates,
		time.Duration(cfg.AdviseTimeoutSeconds())*time.Second,
	)
	cacheTTL := time.Duration(cfg.AdviseCacheTTLSeconds()) * time.Second
	return advise.NewHandler(reg, judge, cacheTTL, log), nil
}
