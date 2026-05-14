package config

import "errors"

// ErrEmptyDirectory is returned when the configuration directory contains no
// *.yaml files.
var ErrEmptyDirectory = errors.New("config: directory contains no yaml files")

// ErrDuplicateTopLevelKey is retained as a backward-compatibility sentinel for
// external callers (notably cmd/cli's error classifier). The single-purpose
// file layout makes cross-file duplicates impossible by construction, so the
// loader no longer returns this. Duplicate keys WITHIN a single file surface
// as a yaml.v3 parse error wrapped in ErrParse.
//
// Deprecated: unreachable from Load; kept exported for compile-time
// compatibility with downstream classifiers.
var ErrDuplicateTopLevelKey = errors.New("config: duplicate top-level key across files")

// ErrUnknownTopLevelKey is retained for the same compatibility reason as
// ErrDuplicateTopLevelKey. With single-purpose files the relevant failure mode
// is "this key is not allowed in this file", reported via ErrWrongFileForKey.
//
// Deprecated: unreachable from Load; kept exported for compile-time
// compatibility with downstream classifiers.
var ErrUnknownTopLevelKey = errors.New("config: unknown top-level key")

// ErrUnexpectedConfigFile is returned when the configuration directory
// contains a file whose name is not one of the three accepted filenames
// (gateway.yaml, providers.yaml, policy.yaml).
var ErrUnexpectedConfigFile = errors.New("config: unexpected file in config directory")

// ErrWrongFileForKey is returned when a top-level key appears in a file that
// is not authorized to carry it — e.g., `gateway:` in policy.yaml.
var ErrWrongFileForKey = errors.New("config: top-level key is not allowed in this file")

// ErrNoConfigurations is returned when the merged tree contains zero entries
// under `configurations`.
var ErrNoConfigurations = errors.New("config: at least one configuration required")

// ErrUnknownConfiguration is returned when an api_key references a configuration
// name that does not exist in the merged tree.
var ErrUnknownConfiguration = errors.New("config: api_key references unknown configuration")

// ErrEndpointNotInProvider is returned when a configuration's allowed_endpoints
// references a provider.endpoint pair that is not in the merged providers block.
var ErrEndpointNotInProvider = errors.New("config: allowed_endpoint references unknown provider.endpoint")

// ErrMalformedAllowedEndpoint is returned when an allowed_endpoints entry is not
// in the form "provider.endpoint".
var ErrMalformedAllowedEndpoint = errors.New("config: allowed_endpoint must be provider.endpoint")

// ErrPathCollision is returned when two providers claim the same fully-resolved
// route path. With prefix disambiguation, collisions are only possible when
// two providers both have `prefix_required: false` and share an accepted_path,
// or when two providers share both the same prefix and an accepted_path.
var ErrPathCollision = errors.New("config: route path claimed by multiple endpoints")

// ErrPrefixRequiredEmpty is returned when a provider has `prefix_required: true`
// but no prefix value — the provider would be unreachable.
var ErrPrefixRequiredEmpty = errors.New("config: prefix_required is true but prefix is empty")

// ErrInvalidBind is returned when gateway.http.bind is not a valid host:port.
var ErrInvalidBind = errors.New("config: gateway.http.bind must be host:port")

// ErrParse is returned when a YAML file is malformed.
var ErrParse = errors.New("config: parse error")

// ErrUnknownRuleName is returned when a Configuration's rule_names entry does
// not resolve to a rule in the top-level rules library.
var ErrUnknownRuleName = errors.New("config: configuration references unknown rule name")

// ErrDuplicateRuleName is returned when two entries in the top-level rules
// library share the same Name. Names are the canonical reference target so
// duplicates would make resolution ambiguous.
var ErrDuplicateRuleName = errors.New("config: rule name defined more than once in the rules library")

// ErrDuplicateRuleID is returned when two entries in the top-level rules
// library share the same ID. Only entries with non-nil ID are compared —
// nil ID is the default in operator-authored static config.
var ErrDuplicateRuleID = errors.New("config: rule id defined more than once in the rules library")

// ErrUnknownResilienceName is returned when a Configuration's resilience_name
// does not resolve to a policy in the top-level resilience_policies library.
var ErrUnknownResilienceName = errors.New("config: configuration references unknown resilience policy")

// ErrDuplicateResilienceName is returned when two entries in the top-level
// resilience_policies library share the same Name.
var ErrDuplicateResilienceName = errors.New("config: resilience policy name defined more than once")

// ErrDuplicateResilienceID is returned when two entries in the top-level
// resilience_policies library share the same ID. Only entries with non-nil
// ID are compared.
var ErrDuplicateResilienceID = errors.New("config: resilience policy id defined more than once")
