package arbiter

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// evidenceCipher seals offending fields with AES-256-GCM before they reach the
// store. The evidence table is a PII concentrator by design (ADR-018), so the
// plaintext never lands in Postgres — only ciphertext + nonce + a short key id
// for rotation bookkeeping.
type evidenceCipher struct {
	aead  cipher.AEAD
	keyID string
}

// newEvidenceCipher builds the cipher from a 32-byte AES-256 key. The key id is
// a short fingerprint of the key (not the key itself) so a rotation is
// traceable without exposing key material.
func newEvidenceCipher(key []byte) (*evidenceCipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("arbiter: evidence cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("arbiter: evidence gcm: %w", err)
	}
	sum := sha256.Sum256(key)
	return &evidenceCipher{aead: aead, keyID: hex.EncodeToString(sum[:4])}, nil
}

// seal encrypts plaintext, returning ciphertext and the fresh random nonce it
// was sealed under.
func (c *evidenceCipher) seal(plaintext string) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("arbiter: evidence nonce: %w", err)
	}
	return c.aead.Seal(nil, nonce, []byte(plaintext), nil), nonce, nil
}

// open decrypts a sealed evidence record. Used by the console to reveal the
// offending field on demand (and by tests to verify the round-trip).
func (c *evidenceCipher) open(ciphertext, nonce []byte) (string, error) {
	plain, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("arbiter: evidence open: %w", err)
	}
	return string(plain), nil
}
