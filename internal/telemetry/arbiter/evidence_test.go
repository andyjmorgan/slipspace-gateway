package arbiter

import (
	"bytes"
	"testing"
)

func TestEvidenceCipher_RoundTrip(t *testing.T) {
	c, err := newEvidenceCipher(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	const secret = "user SSN 123-45-6789 and email a@b.com" //nolint:gosec // test fixture, not a real credential
	ct, nonce, err := c.seal(secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("123-45-6789")) {
		t.Fatal("ciphertext leaks plaintext")
	}
	got, err := c.open(ct, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if got != secret {
		t.Errorf("round trip = %q, want %q", got, secret)
	}
	if c.keyID == "" {
		t.Error("key id should be a non-empty fingerprint")
	}
}

func TestEvidenceCipher_TamperDetected(t *testing.T) {
	c, _ := newEvidenceCipher(bytes.Repeat([]byte{1}, 32))
	ct, nonce, _ := c.seal("payload")
	ct[0] ^= 0xff // flip a bit
	if _, err := c.open(ct, nonce); err == nil {
		t.Error("expected GCM auth failure on tampered ciphertext")
	}
}

func TestNewEvidenceCipher_BadKey(t *testing.T) {
	if _, err := newEvidenceCipher([]byte("short")); err == nil {
		t.Error("expected error for non-32-byte key")
	}
}
