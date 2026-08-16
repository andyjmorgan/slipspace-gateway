package auth

import (
	"errors"
	"fmt"

	"github.com/andyjmorgan/slipspace-gateway/internal/config"
)

// ErrUnauthorized covers every managed-mode failure where we refuse to reveal
// whether a key exists: missing bearer, malformed bearer, unknown secret,
// disabled key.
var ErrUnauthorized = errors.New("auth: unauthorized")

// ErrUnknownConfiguration is returned when a referenced configuration name does
// not exist in the loaded configuration index. Wraps config.ErrUnknownConfiguration
// so callers that do `errors.Is(err, config.ErrUnknownConfiguration)` succeed
// regardless of whether the failure was raised at load time (config validate)
// or at request time (auth resolve). One conceptual error, two phrasing layers.
var ErrUnknownConfiguration = fmt.Errorf("auth: %w", config.ErrUnknownConfiguration)

// Result is the audit tag emitted in structured logs for the resolution
// outcome.
type Result string

// Result tags emitted in the structured "result" field of auth log entries,
// and consumed by downstream log dashboards. This is the complete set
// classifyResult can produce.
//
// There is deliberately no separate tag for a missing versus a malformed
// bearer: every managed-mode discovery miss collapses into ErrUnauthorized
// with no APIKey attached, so both log as ResultUnknownKey. Splitting them
// would need a distinguishing sentinel threaded out of discoverManagedKey.
// See docs/auth.md.
const (
	ResultSuccess Result = "success"

	ResultUnknownKey Result = "unknown_key"

	ResultDisabledKey Result = "disabled_key"

	ResultUnknownConfiguration Result = "unknown_configuration"
)
