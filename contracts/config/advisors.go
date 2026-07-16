package config

import (
	"errors"
	"fmt"
	"net/url"
	"time"
)

// This file defines the agent-aware routing config surface: the top-level
// `advisors` block (where the Arbiter's advisory endpoint lives and how the
// gateway authenticates to it) and the per-configuration `agent_routing`
// block (which advisor to ask, what the pin lifecycle is, and which models a
// verdict may route to). See the "Agent-Aware Model Routing" design note.

// Advisor lifecycle defaults, applied when the operator leaves the knobs unset.
const (
	// DefaultAdvisorTimeoutMs bounds one advisory HTTP call. The call is
	// asynchronous (never on the request path), so the bound exists to reclaim
	// the goroutine, not to protect latency.
	DefaultAdvisorTimeoutMs = 5000

	// DefaultPinTTLSeconds is how long a stored pin stays live without
	// renewal. Declared in seconds (not time.Duration arithmetic) so the
	// tygo-generated TS constant stays a plain number.
	DefaultPinTTLSeconds = 7200

	// DefaultApplyWindowRequests is how many requests into a conversation a
	// late-arriving verdict may still be applied. Past the window the prompt
	// cache is warm on the default model and a switch would cost more than it
	// saves, so the verdict is discarded.
	DefaultApplyWindowRequests = 3
)

// AdvisorsConfig is the merged top-level `advisors` block — a map from advisor
// name to its connection definition.
type AdvisorsConfig map[string]Advisor

// Advisor is one advisory endpoint the gateway can consult for agent-aware
// routing verdicts (in practice: an Arbiter's POST /api/v1/advise/route).
type Advisor struct {
	// Endpoint is the full URL of the advisory route endpoint.
	Endpoint string `yaml:"endpoint" json:"endpoint"`

	// HMACSecretFile is the path of the file holding the shared HMAC secret
	// the gateway signs advisory requests with (same trust pattern as the
	// Record webhook). Only file paths are env/config-carried — never the
	// secret itself.
	HMACSecretFile string `yaml:"hmac_secret_file" json:"hmac_secret_file"`

	// GatewayID is sent as the X-Slipspace-Gateway-Id header so the advisor
	// verifies the signature against this gateway's registered secret (same
	// convention as the webhook connector's gateway_id).
	GatewayID string `yaml:"gateway_id" json:"gateway_id"`

	// TimeoutMs bounds one advisory call in milliseconds. Zero applies
	// DefaultAdvisorTimeoutMs.
	TimeoutMs int `yaml:"timeout_ms,omitempty" json:"timeout_ms,omitempty"`
}

// AgentRouting is a configuration's agent-aware routing policy: identify the
// calling agent from headers, ask the named advisor to classify eligible
// conversations at birth, and pin the verdict's model for the conversation's
// lifetime. Absent (nil) means the feature is off for the configuration.
type AgentRouting struct {
	// Advisor names an entry in the top-level advisors block.
	Advisor string `yaml:"advisor" json:"advisor"`

	// AllowModels is the enforcement point: a verdict whose model is not in
	// this list is discarded. Must be non-empty — the advisor recommends,
	// this list decides.
	AllowModels []string `yaml:"allow_models" json:"allow_models"`

	// PinTTLSeconds is how long a pin lives. Zero applies DefaultPinTTLSeconds.
	PinTTLSeconds int `yaml:"pin_ttl_seconds,omitempty" json:"pin_ttl_seconds,omitempty"`

	// ApplyWindowRequests is how many requests into a conversation a verdict
	// may still be applied (see DefaultApplyWindowRequests). Zero applies the
	// default.
	ApplyWindowRequests int `yaml:"apply_window_requests,omitempty" json:"apply_window_requests,omitempty"`
}

// Sentinel errors for advisor/agent-routing validation.
var (
	// ErrAdvisorEndpoint marks a missing or unparseable advisor endpoint.
	ErrAdvisorEndpoint = errors.New("advisor endpoint missing or invalid")

	// ErrAdvisorSecretFile marks a missing hmac_secret_file path.
	ErrAdvisorSecretFile = errors.New("advisor hmac_secret_file missing")

	// ErrAdvisorGatewayID marks a missing gateway_id.
	ErrAdvisorGatewayID = errors.New("advisor gateway_id missing")

	// ErrAgentRoutingAdvisor marks an agent_routing block referencing an
	// advisor name not present in the advisors block.
	ErrAgentRoutingAdvisor = errors.New("agent_routing references unknown advisor")

	// ErrAgentRoutingAllowModels marks an agent_routing block with an empty
	// allow_models list.
	ErrAgentRoutingAllowModels = errors.New("agent_routing allow_models must be non-empty")
)

// Validate checks one advisor definition.
func (a Advisor) Validate(name string) error {
	if a.Endpoint == "" {
		return fmt.Errorf("advisor %q: %w", name, ErrAdvisorEndpoint)
	}
	u, err := url.Parse(a.Endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("advisor %q: endpoint %q: %w", name, a.Endpoint, ErrAdvisorEndpoint)
	}
	if a.HMACSecretFile == "" {
		return fmt.Errorf("advisor %q: %w", name, ErrAdvisorSecretFile)
	}
	if a.GatewayID == "" {
		return fmt.Errorf("advisor %q: %w", name, ErrAdvisorGatewayID)
	}
	return nil
}

// Timeout returns the effective advisory call timeout.
func (a Advisor) Timeout() time.Duration {
	if a.TimeoutMs <= 0 {
		return DefaultAdvisorTimeoutMs * time.Millisecond
	}
	return time.Duration(a.TimeoutMs) * time.Millisecond
}

// PinTTL returns the effective pin lifetime.
func (ar *AgentRouting) PinTTL() time.Duration {
	if ar == nil || ar.PinTTLSeconds <= 0 {
		return DefaultPinTTLSeconds * time.Second
	}
	return time.Duration(ar.PinTTLSeconds) * time.Second
}

// ApplyWindow returns the effective apply window in requests.
func (ar *AgentRouting) ApplyWindow() int {
	if ar == nil || ar.ApplyWindowRequests <= 0 {
		return DefaultApplyWindowRequests
	}
	return ar.ApplyWindowRequests
}

// ModelAllowed reports whether a verdict model passes the allow-list.
func (ar *AgentRouting) ModelAllowed(model string) bool {
	if ar == nil {
		return false
	}
	for _, m := range ar.AllowModels {
		if m == model {
			return true
		}
	}
	return false
}
