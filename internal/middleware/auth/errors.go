package auth

import (
	"errors"
	"fmt"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
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

// ErrNoRoute signals the auth middleware was invoked without a routing decision
// on the request context. This is a programming error in the middleware wiring
// — auth must run downstream of routing.
var ErrNoRoute = errors.New("auth: no route on context")

// Result is the audit tag emitted in structured logs for the resolution
// outcome.
type Result string

// Result tags emitted in the structured "result" field of auth log entries.
// These are the values referenced by the Authentication & Auth Modes design
// note and consumed by downstream log dashboards.
const (
	ResultSuccess Result = "success"

	ResultUnknownKey Result = "unknown_key"

	ResultDisabledKey Result = "disabled_key"

	ResultUnknownConfiguration Result = "unknown_configuration"

	ResultMalformedBearer Result = "malformed_bearer"

	ResultMissingBearer Result = "missing_bearer"
)
