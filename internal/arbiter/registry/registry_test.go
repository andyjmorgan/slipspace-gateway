package registry

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/config"
)

// sign returns the hex HMAC-SHA256 of body under secret, as a gateway would.
func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func newTestRegistry() *Registry {
	return New([]config.Gateway{
		{ID: "gw-a", HMACSecret: "secret-a"},
		{ID: "gw-b", HMACSecret: "secret-b"},
	})
}

func TestKnown(t *testing.T) {
	r := newTestRegistry()
	if !r.Known("gw-a") {
		t.Error("gw-a should be known")
	}
	if r.Known("gw-x") {
		t.Error("gw-x should be unknown")
	}
}

func TestVerify_Valid(t *testing.T) {
	r := newTestRegistry()
	body := []byte(`{"correlation_id":"abc"}`)
	if err := r.Verify("gw-a", body, sign("secret-a", body)); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerify_UnknownGateway(t *testing.T) {
	r := newTestRegistry()
	body := []byte("x")
	if err := r.Verify("gw-x", body, sign("secret-a", body)); !errors.Is(err, ErrUnknownGateway) {
		t.Fatalf("err = %v, want ErrUnknownGateway", err)
	}
}

func TestVerify_WrongSecret(t *testing.T) {
	r := newTestRegistry()
	body := []byte("x")
	// Signed with gw-b's secret but claims to be gw-a.
	if err := r.Verify("gw-a", body, sign("secret-b", body)); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestVerify_TamperedBody(t *testing.T) {
	r := newTestRegistry()
	sig := sign("secret-a", []byte("original"))
	if err := r.Verify("gw-a", []byte("tampered"), sig); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestVerify_MalformedHex(t *testing.T) {
	r := newTestRegistry()
	if err := r.Verify("gw-a", []byte("x"), "not-hex!!"); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}
