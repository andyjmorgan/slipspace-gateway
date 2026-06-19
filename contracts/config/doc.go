// Package config defines the public, on-disk YAML schema for the SlipSpace
// gateway's policy and providers files. These types are intentionally
// exported so control-plane code and downstream SDKs can consume them
// without importing internal packages.
//
// Server-level configuration (bind, drain, reporting, telemetry, logging)
// is no longer modeled here — it lives on internal/config.ServerEnv, sourced
// from SLIPSPACE_* env vars.
package config
