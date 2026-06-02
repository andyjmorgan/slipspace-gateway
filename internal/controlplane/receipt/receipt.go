// Package receipt is the control plane's tamper-evidence core: signed,
// hash-chained receipts that attest the CP received and stored a captured
// record, unaltered, in order. It implements the verifiable-record subset of
// the CSA AARM spec — R2 (hash chain), R5 (signed receipts), R6 (identity
// binding via gateway id) — per the "Central Control Plane" design note
// (CP-5 / decision #8).
//
// The CP signs (not the gateway): for the internal trusted-boundary threat
// model the attestation that matters is "the CP received + stored this", so a
// single CP-held key is simpler than fleet-wide key distribution and still
// satisfies R5. Each gateway gets its own append-only chain keyed by
// (gateway_id, seq); a verifier re-derives every hash and checks every
// signature, so any reordering, gap, or mutation is detectable.
package receipt

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
)

// Record is the slim, signable description of one captured request the CP
// ingested. It is what a receipt attests to; the full body (if any) is anchored
// by its hash, not embedded.
type Record struct {
	// GatewayID identifies the fleet member the record came from (R6 identity
	// binding) and selects which chain the receipt extends.
	GatewayID string

	// Seq is the record's position in this gateway's chain, starting at 1.
	Seq uint64

	// CorrelationID joins the receipt to the request_events / request_bodies
	// rows for the same request.
	CorrelationID string

	// BodyHash is the hex SHA-256 of the best-effort full body, or "" when no
	// body was captured. Anchoring by hash keeps the receipt small and lets the
	// (best-effort) body be verified against the (reliable) chain.
	BodyHash string

	// Payload is the canonical metadata bytes the receipt commits to.
	Payload []byte
}

// Receipt is a signed, hash-chained attestation over a Record.
type Receipt struct {
	GatewayID     string
	Seq           uint64
	CorrelationID string
	BodyHash      string
	Payload       []byte

	// PrevHash is the Hash of the previous receipt in this gateway's chain, or
	// nil for the first (genesis) receipt.
	PrevHash []byte

	// Hash is the chain digest over PrevHash and the record fields.
	Hash []byte

	// Signature is Sign(Hash) under the key identified by KeyID.
	Signature []byte
	KeyID     string
}

// Signer signs a chain digest. Production holds the key in a KMS/HSM; the
// in-process Ed25519Signer is the trusted-boundary default.
type Signer interface {
	// Sign returns a signature over digest and the id of the signing key.
	Sign(digest []byte) (signature []byte, keyID string)
}

// Ed25519Signer signs chain digests with an in-memory Ed25519 key.
type Ed25519Signer struct {
	keyID string
	priv  ed25519.PrivateKey
}

// NewEd25519Signer builds a signer over priv, labelled keyID.
func NewEd25519Signer(keyID string, priv ed25519.PrivateKey) *Ed25519Signer {
	return &Ed25519Signer{keyID: keyID, priv: priv}
}

// Sign signs digest with the Ed25519 key.
func (s *Ed25519Signer) Sign(digest []byte) ([]byte, string) {
	return ed25519.Sign(s.priv, digest), s.keyID
}

// Public returns the verifying key.
func (s *Ed25519Signer) Public() ed25519.PublicKey {
	return s.priv.Public().(ed25519.PublicKey)
}

// Chain builds the next receipt: it computes the chain digest over prevHash and
// r, signs it, and returns the receipt. prevHash is nil for the genesis receipt.
func Chain(prevHash []byte, r Record, signer Signer) Receipt {
	digest := canonicalDigest(prevHash, r)
	sig, keyID := signer.Sign(digest)
	return Receipt{
		GatewayID:     r.GatewayID,
		Seq:           r.Seq,
		CorrelationID: r.CorrelationID,
		BodyHash:      r.BodyHash,
		Payload:       r.Payload,
		PrevHash:      prevHash,
		Hash:          digest,
		Signature:     sig,
		KeyID:         keyID,
	}
}

// ErrBrokenChain reports that a chain failed verification (a re-derived hash,
// link, or signature did not match), so the records were reordered, dropped, or
// tampered with.
var ErrBrokenChain = errors.New("receipt: broken or tampered chain")

// VerifyChain checks that receipts form an unbroken, correctly-signed chain for
// a single gateway under pub: contiguous Seq from 1, each PrevHash links the
// prior Hash, each Hash re-derives from its record, and each Signature
// validates. Any mismatch returns ErrBrokenChain.
func VerifyChain(receipts []Receipt, pub ed25519.PublicKey) error {
	var prev []byte
	for i, rc := range receipts {
		wantSeq := uint64(i + 1)
		if rc.Seq != wantSeq {
			return fmt.Errorf("%w: receipt %d has seq %d, want %d", ErrBrokenChain, i, rc.Seq, wantSeq)
		}
		if !bytesEqual(rc.PrevHash, prev) {
			return fmt.Errorf("%w: receipt %d prev_hash does not link the previous receipt", ErrBrokenChain, i)
		}
		want := canonicalDigest(rc.PrevHash, Record{
			GatewayID:     rc.GatewayID,
			Seq:           rc.Seq,
			CorrelationID: rc.CorrelationID,
			BodyHash:      rc.BodyHash,
			Payload:       rc.Payload,
		})
		if !bytesEqual(rc.Hash, want) {
			return fmt.Errorf("%w: receipt %d hash does not match its record (tampered)", ErrBrokenChain, i)
		}
		if !ed25519.Verify(pub, rc.Hash, rc.Signature) {
			return fmt.Errorf("%w: receipt %d signature invalid", ErrBrokenChain, i)
		}
		prev = rc.Hash
	}
	return nil
}

// canonicalDigest computes the chain hash over prevHash followed by the record
// fields. Every field is length-prefixed so distinct field values can never
// concatenate to the same byte stream (no ambiguity / extension attacks).
func canonicalDigest(prevHash []byte, r Record) []byte {
	h := sha256.New()
	writeField(h, prevHash)
	writeField(h, []byte(r.GatewayID))
	var seq [8]byte
	binary.BigEndian.PutUint64(seq[:], r.Seq)
	writeField(h, seq[:])
	writeField(h, []byte(r.CorrelationID))
	writeField(h, []byte(r.BodyHash))
	writeField(h, r.Payload)
	return h.Sum(nil)
}

func writeField(h hash.Hash, b []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	_, _ = h.Write(n[:])
	_, _ = h.Write(b)
}

// bytesEqual treats nil and empty as equal (the genesis prev_hash is nil).
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
