package receipt

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

func testSigner() *Ed25519Signer {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	return NewEd25519Signer("key-test", ed25519.NewKeyFromSeed(seed))
}

func sampleRecords(n int) []Record {
	out := make([]Record, n)
	for i := range out {
		out[i] = Record{
			GatewayID:     "gw-1",
			Seq:           uint64(i + 1),
			CorrelationID: "corr-" + string(rune('a'+i)),
			BodyHash:      "bodyhash-" + string(rune('a'+i)),
			Payload:       []byte(`{"model":"gpt-4o","tokens":` + string(rune('0'+i)) + `}`),
		}
	}
	return out
}

func buildChain(signer Signer, records []Record) []Receipt {
	out := make([]Receipt, 0, len(records))
	var prev []byte
	for _, r := range records {
		rc := Chain(prev, r, signer)
		out = append(out, rc)
		prev = rc.Hash
	}
	return out
}

func TestChain_VerifiesCleanChain(t *testing.T) {
	signer := testSigner()
	chain := buildChain(signer, sampleRecords(5))

	if chain[0].PrevHash != nil {
		t.Errorf("genesis receipt prev_hash = %x, want nil", chain[0].PrevHash)
	}
	if chain[0].KeyID != "key-test" {
		t.Errorf("key id = %q", chain[0].KeyID)
	}
	if err := VerifyChain(chain, signer.Public()); err != nil {
		t.Fatalf("clean chain failed to verify: %v", err)
	}
}

func TestVerifyChain_DetectsTamperedPayload(t *testing.T) {
	signer := testSigner()
	chain := buildChain(signer, sampleRecords(4))

	// Mutate a stored payload without re-signing — the hash no longer matches
	// the record.
	chain[2].Payload = []byte(`{"model":"gpt-4o","tokens":999}`)

	if err := VerifyChain(chain, signer.Public()); !errors.Is(err, ErrBrokenChain) {
		t.Fatalf("tampered payload: err = %v, want ErrBrokenChain", err)
	}
}

func TestVerifyChain_DetectsTamperedBodyHash(t *testing.T) {
	signer := testSigner()
	chain := buildChain(signer, sampleRecords(3))

	chain[1].BodyHash = "swapped-body"
	if err := VerifyChain(chain, signer.Public()); !errors.Is(err, ErrBrokenChain) {
		t.Fatalf("tampered body hash: err = %v, want ErrBrokenChain", err)
	}
}

func TestVerifyChain_DetectsBrokenLink(t *testing.T) {
	signer := testSigner()
	chain := buildChain(signer, sampleRecords(4))

	chain[2].PrevHash = []byte("not-the-previous-hash-not-the-previous-ha")
	if err := VerifyChain(chain, signer.Public()); !errors.Is(err, ErrBrokenChain) {
		t.Fatalf("broken link: err = %v, want ErrBrokenChain", err)
	}
}

func TestVerifyChain_DetectsDroppedOrReordered(t *testing.T) {
	signer := testSigner()
	chain := buildChain(signer, sampleRecords(4))

	// Drop the middle receipt: the gap shows up as a seq mismatch.
	gapped := []Receipt{chain[0], chain[1], chain[3]}
	if err := VerifyChain(gapped, signer.Public()); !errors.Is(err, ErrBrokenChain) {
		t.Fatalf("dropped receipt: err = %v, want ErrBrokenChain", err)
	}

	reordered := []Receipt{chain[1], chain[0], chain[2], chain[3]}
	if err := VerifyChain(reordered, signer.Public()); !errors.Is(err, ErrBrokenChain) {
		t.Fatalf("reordered receipts: err = %v, want ErrBrokenChain", err)
	}
}

func TestVerifyChain_DetectsBadSignature(t *testing.T) {
	signer := testSigner()
	chain := buildChain(signer, sampleRecords(3))

	chain[0].Signature[0] ^= 0xff
	if err := VerifyChain(chain, signer.Public()); !errors.Is(err, ErrBrokenChain) {
		t.Fatalf("bad signature: err = %v, want ErrBrokenChain", err)
	}
}

func TestVerifyChain_RejectsWrongKey(t *testing.T) {
	signer := testSigner()
	chain := buildChain(signer, sampleRecords(2))

	otherSeed := make([]byte, ed25519.SeedSize)
	for i := range otherSeed {
		otherSeed[i] = byte(200 - i)
	}
	wrongPub := ed25519.NewKeyFromSeed(otherSeed).Public().(ed25519.PublicKey)

	if err := VerifyChain(chain, wrongPub); !errors.Is(err, ErrBrokenChain) {
		t.Fatalf("wrong key: err = %v, want ErrBrokenChain", err)
	}
}

func TestVerifyChain_EmptyChain(t *testing.T) {
	if err := VerifyChain(nil, testSigner().Public()); err != nil {
		t.Errorf("empty chain should verify trivially, got %v", err)
	}
}
