// Package admin holds the on-disk schema for the management console's
// admin block. The console (SPA + control-plane API) runs as a second
// http.Server bound to its own port inside the gateway binary; this
// package carries the configuration shape that gates it.
package admin

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// Username is the hardcoded operator username for HTTP Basic auth. The
// console does not support multi-operator identity in v1.1 — the gateway
// is internal infrastructure and the password rotates out-of-band.
const Username = "admin"

// EnvPassword is the environment variable consulted at password
// resolution time. When set (non-empty after trim), it wins over the
// yaml Password field. Use this to keep the admin secret out of the
// yaml file in production while leaving yaml workable for dev.
//
//nolint:gosec // G101: this is the env-var NAME, not a credential value
const EnvPassword = "SLUICE_ADMIN_PASSWORD"

// DefaultBindAddr is the listener address used when Config.BindAddr is
// unset. Binds all interfaces on port 8081 — point a k8s NetworkPolicy
// or Service at it for isolation. Use "127.0.0.1:8081" for loopback-
// only deployments where access is fronted by a sidecar.
const DefaultBindAddr = "0.0.0.0:8081"

// Sentinel errors returned by Config.Validate.
var (
	// ErrPasswordRequired fires when Enabled is true but neither the
	// EnvPassword env var nor Config.Password is populated.
	ErrPasswordRequired = errors.New("admin: enabled without a password (set Password in admin.yaml or " + EnvPassword + ")")

	// ErrInvalidBindAddr fires when Config.BindAddr cannot be parsed
	// as host:port with a numeric port.
	ErrInvalidBindAddr = errors.New("admin: bind_addr is not a valid host:port")
)

// Config is the `admin:` top-level block of admin.yaml.
//
// The console is off by default. When Enabled is true, the gateway
// starts a second http.Server bound to BindAddr (or DefaultBindAddr)
// serving the embedded SPA at "/" and the control-plane API under
// "/api/v1/*". HTTP Basic auth protects the API; the SPA's static
// assets are public.
//
// Password resolution prefers the EnvPassword env var when set;
// otherwise it reads the Password field. This lets production
// deployments keep the secret in a k8s Secret (mounted as env) while
// dev environments can use yaml.
type Config struct {
	// Enabled gates the admin listener. False = no second http.Server
	// is started, no admin routes exist anywhere.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// BindAddr is the admin listener address (host:port). Empty
	// resolves to DefaultBindAddr at runtime. Must not collide with
	// the data-plane listener.
	BindAddr string `yaml:"bind_addr,omitempty" json:"bind_addr,omitempty"`

	// Password is the operator credential used for HTTP Basic auth.
	// May be empty in yaml — ResolvePassword falls back to EnvPassword.
	// Never serialised to JSON.
	Password string `yaml:"password,omitempty" json:"-"`
}

// EffectiveBindAddr returns BindAddr when set, otherwise DefaultBindAddr.
func (c *Config) EffectiveBindAddr() string {
	if strings.TrimSpace(c.BindAddr) == "" {
		return DefaultBindAddr
	}
	return c.BindAddr
}

// ResolvePassword returns the runtime password. The EnvPassword env
// var wins over the yaml Password field when both are populated;
// either source alone is honoured. Both empty returns "".
func (c *Config) ResolvePassword() string {
	if v := strings.TrimSpace(os.Getenv(EnvPassword)); v != "" {
		return v
	}
	return c.Password
}

// Validate enforces the admin block's invariants. Disabled blocks pass
// unconditionally — operators may leave bind/password unset when the
// console is off.
func (c *Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.ResolvePassword() == "" {
		return ErrPasswordRequired
	}
	if err := validateBindAddr(c.EffectiveBindAddr()); err != nil {
		return fmt.Errorf("%w: %q", err, c.EffectiveBindAddr())
	}
	return nil
}

// validateBindAddr asserts host:port shape with a numeric port. An
// empty host (":8081") is accepted — binds all interfaces.
func validateBindAddr(bind string) error {
	host, port, err := net.SplitHostPort(bind)
	if err != nil {
		return ErrInvalidBindAddr
	}
	if host == "" && port == "" {
		return ErrInvalidBindAddr
	}
	if _, perr := strconv.Atoi(port); perr != nil {
		return ErrInvalidBindAddr
	}
	return nil
}
