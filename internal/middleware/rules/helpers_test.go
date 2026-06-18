package rules_test

import (
	contractsrules "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/config"
)

// testStore wraps a per-configuration-rules map in a fresh config.Store
// so tests built before the Store refactor can keep their call sites
// short. The Store is intentionally minimal — only PerConfigurationRules
// is populated — because the rules evaluator never touches the other
// fields.
func testStore(perConfigRules map[string][]*contractsrules.RuleContract) *config.Store {
	return config.NewStore(&config.ResolvedConfig{
		PerConfigurationRules: perConfigRules,
	})
}
