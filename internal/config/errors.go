package config

import "errors"

// ErrEmptyDirectory is returned when the configuration directory contains no
// *.yaml files.
var ErrEmptyDirectory = errors.New("config: directory contains no yaml files")

// ErrUnexpectedConfigFile is a retained v1 sentinel and is no longer returned.
// The v2 loader merges every *.yaml in the config directory by top-level key
// and has no filename allowlist (see Load in config_model.go). Kept only so
// existing errors.Is callers still compile.
var ErrUnexpectedConfigFile = errors.New("config: unexpected file in config directory")

// ErrWrongFileForKey is a retained v1 sentinel and is no longer returned. Under
// the v2 model any file may carry any top-level key; the only per-key rule is
// that one block must not be set by two files (see ErrDuplicateKey). Kept only
// so existing errors.Is callers still compile.
var ErrWrongFileForKey = errors.New("config: top-level key is not allowed in this file")

// ErrNoConfigurations is returned when the merged tree contains zero entries
// under `configurations`.
var ErrNoConfigurations = errors.New("config: at least one configuration required")

// ErrUnknownConfiguration is returned when an api_key references a configuration
// name that does not exist in the merged tree.
var ErrUnknownConfiguration = errors.New("config: api_key references unknown configuration")

// ErrPathCollision is a retained v1 sentinel and is no longer returned. It
// described the retired prefix-routing model, where providers claimed
// fully-resolved route paths disambiguated by prefix. Kept only so existing
// errors.Is callers still compile.
var ErrPathCollision = errors.New("config: route path claimed by multiple endpoints")

// ErrPrefixRequiredEmpty is a retained v1 sentinel and is no longer returned.
// `prefix_required` belonged to the retired prefix-routing model. Kept only so
// existing errors.Is callers still compile.
var ErrPrefixRequiredEmpty = errors.New("config: prefix_required is true but prefix is empty")

// ErrInvalidAuthFormat is returned when a provider protocol's or passthrough
// family's `auth.format` does not contain exactly one `{key}` placeholder.
var ErrInvalidAuthFormat = errors.New("config: auth_format must contain {key} exactly once")

// ErrAuthFormatWithoutHeader is returned when `auth.format` is set on a
// protocol or passthrough family that does not also set `auth.header`. The
// format is only consulted when an override header is in effect — set without a
// header, the format would be silently ignored, which is almost always a
// mistake.
var ErrAuthFormatWithoutHeader = errors.New("config: auth_format requires auth_header at the same level")

// ErrInvalidBind is returned when a bind env var (SLIPSPACE_HTTP_BIND,
// SLIPSPACE_PROMETHEUS_BIND) is not a valid host:port.
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

// ErrRetiredEndpointCondition is returned when a rule carries the retired
// "endpoint" condition discriminator (renamed to "protocol"). Because the
// condition registry decodes unknown discriminators to an inert
// UnknownCondition, a stale "endpoint" rule would silently evaluate false
// rather than error — this guard makes the breaking rename fail loud so
// operators update their config instead of losing the rule.
var ErrRetiredEndpointCondition = errors.New(`config: rule condition "endpoint" was renamed to "protocol"`)

// ErrDuplicateRuleID is a retained v1 sentinel and is no longer returned. v2
// validation checks rule Name uniqueness only (validateLibraries,
// config_validate.go); rules are addressed by name through RuleIndex, so a
// duplicate ID is inert. Kept only so existing errors.Is callers still compile.
var ErrDuplicateRuleID = errors.New("config: rule id defined more than once in the rules library")

// ErrUnknownResilienceName is a retained v1 sentinel and is no longer returned.
// The `resilience_policies` library it referenced was replaced by the top-level
// `groups` block in v2; a binding names a group directly and group existence is
// checked in validateBindings. Kept only so existing errors.Is callers still
// compile.
var ErrUnknownResilienceName = errors.New("config: configuration references unknown resilience policy")

// ErrDuplicateResilienceName is a retained v1 sentinel and is no longer
// returned. It described duplicate Names in the retired `resilience_policies`
// library, replaced by the top-level `groups` block in v2. Kept only so
// existing errors.Is callers still compile.
var ErrDuplicateResilienceName = errors.New("config: resilience policy name defined more than once")

// ErrDuplicateResilienceID is a retained v1 sentinel and is no longer returned.
// It described duplicate IDs in the retired `resilience_policies` library,
// replaced by the top-level `groups` block in v2. Kept only so existing
// errors.Is callers still compile.
var ErrDuplicateResilienceID = errors.New("config: resilience policy id defined more than once")

// ErrTargetProviderMissingCredential is a retained v1 sentinel and is no longer
// returned. It described the retired resilience_policies model. Under v2 the
// check is NOT performed at load time: a binding or group target whose provider
// has no entry in the configuration's credentials map validates cleanly and
// fails per-request in selection.resolveTarget instead. Kept only so existing
// errors.Is callers still compile.
var ErrTargetProviderMissingCredential = errors.New("config: resilience target provider has no upstream credential in referencing configuration")

// ErrInvalidEnv is returned when a SLIPSPACE_* env var fails to parse as the
// expected type or violates a numeric range invariant.
var ErrInvalidEnv = errors.New("config: invalid env var value")

// ErrUnknownLogLevel is returned when SLIPSPACE_LOG_LEVEL is set to a value
// outside the accepted set.
var ErrUnknownLogLevel = errors.New("config: SLIPSPACE_LOG_LEVEL must be debug|info|warn|error")

// ErrUnknownLogFormat is returned when SLIPSPACE_LOG_FORMAT is set to a value
// outside the accepted set.
var ErrUnknownLogFormat = errors.New("config: SLIPSPACE_LOG_FORMAT must be json|text")

// ErrUnknownOTLPProtocol is returned when SLIPSPACE_OTLP_PROTOCOL is set to a
// value outside the accepted set.
var ErrUnknownOTLPProtocol = errors.New("config: SLIPSPACE_OTLP_PROTOCOL must be grpc|http/protobuf")

// ErrDuplicateConnectorName is returned when two entries in the top-level
// connectors block share the same Name. Names are the reference target for
// ConnectorBinding.Connector so duplicates would make resolution ambiguous.
var ErrDuplicateConnectorName = errors.New("config: connector name defined more than once")

// ErrUnknownConnectorReference is returned when a Configuration's
// connector_bindings entry references a connector name that is not in the
// top-level connectors block. Caught at config-load so a misconfigured
// binding never reaches the spool.
var ErrUnknownConnectorReference = errors.New("config: configuration references unknown connector")

// ErrDuplicateKey is returned by Load when a top-level block (providers,
// groups, configurations, …) is set by more than one YAML file in the config
// directory. Each block must have a single authoring home.
var ErrDuplicateKey = errors.New("config: top-level block set by more than one file")

// ErrLegacyProvidersKey is returned by Load when a YAML file carries the
// pre-rename top-level `backends:` key. The block was renamed to `providers:`
// in the Vocabulary Refactor and the cut is hard — there is no alias — so the
// loader rejects the old key with a clear message rather than silently ignoring
// it (which would surface only as a confusing "at least one configuration"
// downstream).
var ErrLegacyProvidersKey = errors.New(`config: top-level "backends:" was renamed to "providers:" — rename the block (no back-compat alias)`)

// ErrValidation is the umbrella sentinel for v2 config validation failures
// (unknown provider/group/protocol reference, malformed binding, model-pattern
// collision, …). Wrapped with a specific message at each call site.
var ErrValidation = errors.New("config: v2 validation")
