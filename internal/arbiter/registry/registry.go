// Package registry is the Arbiter's trust boundary for incoming
// large-payload webhooks. Gateways are registered in YAML with an HMAC secret;
// a webhook is accepted iff its X-Sluice-Signature verifies against the secret
// of the gateway it claims to be. This is the only inbound trust check — no
// gateway can read anything back from the service.
package registry

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/andyjmorgan/sluice-gateway/internal/arbiter/config"
)

// Sentinel errors. Callers map these to 401/403 without leaking which check
// failed beyond "rejected".
var (
	// ErrUnknownGateway is returned when no registered gateway matches the id.
	ErrUnknownGateway = errors.New("registry: unknown gateway")
	// ErrBadSignature is returned when the HMAC does not verify.
	ErrBadSignature = errors.New("registry: signature mismatch")
)

// Registry resolves a gateway id to its HMAC secret and verifies signatures.
// It is read-only after construction, so it needs no locking.
type Registry struct {
	secrets map[string][]byte
}

// New builds a Registry from the configured gateways. Config validation has
// already rejected empty/duplicate ids, so this is a straight index.
func New(gateways []config.Gateway) *Registry {
	secrets := make(map[string][]byte, len(gateways))
	for _, g := range gateways {
		secrets[g.ID] = []byte(g.HMACSecret)
	}
	return &Registry{secrets: secrets}
}

// Known reports whether a gateway id is registered.
func (r *Registry) Known(gatewayID string) bool {
	_, ok := r.secrets[gatewayID]
	return ok
}

// Verify checks that sigHex is the hex-encoded HMAC-SHA256 of body under the
// registered secret for gatewayID. The comparison is constant-time. Returns
// ErrUnknownGateway if the id is not registered, ErrBadSignature if it is but
// the signature does not match.
func (r *Registry) Verify(gatewayID string, body []byte, sigHex string) error {
	secret, ok := r.secrets[gatewayID]
	if !ok {
		return ErrUnknownGateway
	}

	want := computeMAC(secret, body)
	got, err := hex.DecodeString(sigHex)
	if err != nil {
		return ErrBadSignature
	}
	if !hmac.Equal(want, got) {
		return ErrBadSignature
	}
	return nil
}

// computeMAC returns the raw HMAC-SHA256 of body under secret.
func computeMAC(secret, body []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return mac.Sum(nil)
}
