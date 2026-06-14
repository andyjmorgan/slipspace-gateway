package admin

// SettingsResponse is the telemetry service's applied (loaded) config, served
// read-only and secret-redacted at GET /api/v1/settings. Config is the config
// document as a generic tree (keys mirror the snake_case YAML the operator
// wrote), so the console can pretty-print it as JSON or stringify it back to
// YAML without a typed schema. Secrets (Postgres password, console password
// hash, gateway HMAC secrets, scanner evidence key) are already redacted to
// "***" by the time they reach this shape.
type SettingsResponse struct {
	// Config is the redacted applied config as a generic JSON object.
	Config map[string]any `json:"config"`
}
