package config

import "errors"

// ErrEmptyDirectory is returned when the configuration directory contains no
// *.yaml files.
var ErrEmptyDirectory = errors.New("config: directory contains no yaml files")

// ErrDuplicateTopLevelKey is returned when two files in the configuration
// directory define the same top-level key (e.g., both define `providers`).
var ErrDuplicateTopLevelKey = errors.New("config: duplicate top-level key across files")

// ErrUnknownTopLevelKey is returned when a YAML file contains a top-level key
// that is not one of {gateway, providers, configurations, api_keys}.
var ErrUnknownTopLevelKey = errors.New("config: unknown top-level key")

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
