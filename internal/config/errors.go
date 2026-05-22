package config

import "errors"

// ErrEmptyDirectory is returned when the configuration directory contains no
// *.yaml files.
var ErrEmptyDirectory = errors.New("config: directory contains no yaml files")

// ErrUnexpectedConfigFile is returned when the configuration directory
// contains a file whose name is not one of the two accepted filenames
// (providers.yaml, policy.yaml).
var ErrUnexpectedConfigFile = errors.New("config: unexpected file in config directory")

// ErrWrongFileForKey is returned when a top-level key appears in a file that
// is not authorized to carry it — e.g., `providers:` in policy.yaml.
var ErrWrongFileForKey = errors.New("config: top-level key is not allowed in this file")

// ErrNoConfigurations is returned when the merged tree contains zero entries
// under `configurations`.
var ErrNoConfigurations = errors.New("config: at least one configuration required")

// ErrUnknownConfiguration is returned when an api_key references a configuration
// name that does not exist in the merged tree.
var ErrUnknownConfiguration = errors.New("config: api_key references unknown configuration")

// ErrPathCollision is returned when two providers claim the same fully-resolved
// route path. With prefix disambiguation, collisions are only possible when
// two providers both have `prefix_required: false` and share an accepted_path,
// or when two providers share both the same prefix and an accepted_path.
var ErrPathCollision = errors.New("config: route path claimed by multiple endpoints")

// ErrPrefixRequiredEmpty is returned when a provider has `prefix_required: true`
// but no prefix value — the provider would be unreachable.
var ErrPrefixRequiredEmpty = errors.New("config: prefix_required is true but prefix is empty")

// ErrInvalidAuthFormat is returned when a provider- or endpoint-level
// `auth_format` does not contain exactly one `{key}` placeholder.
var ErrInvalidAuthFormat = errors.New("config: auth_format must contain {key} exactly once")

// ErrAuthFormatWithoutHeader is returned when `auth_format` is set on a
// provider or endpoint that does not also set `auth_header`. The format is
// only consulted when an override header is in effect — set without a header,
// the format would be silently ignored, which is almost always a mistake.
var ErrAuthFormatWithoutHeader = errors.New("config: auth_format requires auth_header at the same level")

// ErrInvalidBind is returned when a bind env var (SLUICE_HTTP_BIND,
// SLUICE_PROMETHEUS_BIND) is not a valid host:port.
var ErrInvalidBind = errors.New("config: bind must be host:port")

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

// ErrTargetProviderMissingCredential is returned when a resilience policy's
// target references a provider that the referencing Configuration does not
// have an upstream credential mapping for. Caught at config-load time so
// the orchestrator never runs into "no credential for this target" at
// request time.
var ErrTargetProviderMissingCredential = errors.New("config: resilience target provider has no upstream credential in referencing configuration")

// ErrInvalidEnv is returned when a SLUICE_* env var fails to parse as the
// expected type or violates a numeric range invariant.
var ErrInvalidEnv = errors.New("config: invalid env var value")

// ErrUnknownLogLevel is returned when SLUICE_LOG_LEVEL is set to a value
// outside the accepted set.
var ErrUnknownLogLevel = errors.New("config: SLUICE_LOG_LEVEL must be debug|info|warn|error")

// ErrUnknownLogFormat is returned when SLUICE_LOG_FORMAT is set to a value
// outside the accepted set.
var ErrUnknownLogFormat = errors.New("config: SLUICE_LOG_FORMAT must be json|text")

// ErrUnknownOTLPProtocol is returned when SLUICE_OTLP_PROTOCOL is set to a
// value outside the accepted set.
var ErrUnknownOTLPProtocol = errors.New("config: SLUICE_OTLP_PROTOCOL must be grpc|http/protobuf")

// ErrDuplicateConnectorName is returned when two entries in the top-level
// connectors block share the same Name. Names are the reference target for
// ConnectorBinding.Connector so duplicates would make resolution ambiguous.
var ErrDuplicateConnectorName = errors.New("config: connector name defined more than once")

// ErrUnknownConnectorReference is returned when a Configuration's
// connector_bindings entry references a connector name that is not in the
// top-level connectors block. Caught at config-load so a misconfigured
// binding never reaches the spool.
var ErrUnknownConnectorReference = errors.New("config: configuration references unknown connector")
