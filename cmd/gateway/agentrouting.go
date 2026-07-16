package main

import (
	"bytes"
	"log/slog"
	"os"

	"github.com/andyjmorgan/slipspace-gateway/internal/agentroute"
	"github.com/andyjmorgan/slipspace-gateway/internal/config"
)

// buildAgentRouter constructs the agent-aware routing service from the loaded
// advisors block, reading each advisor's HMAC secret file. nil when no
// advisors are configured — the selection middleware treats nil as
// feature-off. Advisor endpoints (like the admin block) bind at startup; the
// per-configuration agent_routing policy is read live from each request's
// snapshot.
func buildAgentRouter(resolved *config.ResolvedConfig, logger *slog.Logger) *agentroute.Service {
	if len(resolved.Advisors) == 0 {
		return nil
	}
	secrets := make(map[string][]byte, len(resolved.Advisors))
	for name, a := range resolved.Advisors {
		raw, err := os.ReadFile(a.HMACSecretFile) //nolint:gosec // operator-trusted config path
		if err != nil {
			logger.Error("agentroute: read advisor hmac secret", "advisor", name, "error", err.Error())
			continue
		}
		secret := bytes.TrimSpace(raw)
		if len(secret) == 0 {
			logger.Error("agentroute: advisor hmac secret file is empty", "advisor", name)
			continue
		}
		secrets[name] = secret
	}
	return agentroute.NewService(resolved.Advisors, secrets, logger)
}
