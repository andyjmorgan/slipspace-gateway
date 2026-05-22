// Package configexport produces a redacted, downloadable snapshot of the
// gateway's on-disk YAML configuration for the admin Settings page.
//
// The redactor walks declared secret-bearing paths in the YAML tree and
// replaces leaf values with the literal "***". Adding a new secret-bearing
// field type in future must require an explicit redactor update — that is
// the safer failure mode versus regex-matching key names.
//
// Declared secret paths (mirroring contracts/config + contracts/admin):
//
//   - api_keys[].secret               // Sluice-issued sk_live_... keys
//   - configurations.<name>.upstream_credentials.* // provider API keys
//   - admin.password                  // admin Basic-auth password
//
// The bundler reads accepted YAML files through config.ListConfigFiles so the
// export stays in lockstep with what the loader sees — no separate hardcoded
// filename list to drift.
package configexport
