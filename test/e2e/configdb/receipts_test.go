//go:build e2e

package configdb_test

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/receipt"
)

// TestConfigDB_ReceiptChain exercises the Postgres-backed tamper-evidence chain
// against real Postgres: per-gateway append assigns contiguous seq, the stored
// chain verifies under the signing key, gateways have independent genesis
// chains, and tampering with a persisted receipt is detectable.
func TestConfigDB_ReceiptChain(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 7)
	}
	signer := receipt.NewEd25519Signer("k1", ed25519.NewKeyFromSeed(seed))

	for i := 0; i < 4; i++ {
		r, err := db.AppendReceipt(ctx, receipt.Record{
			GatewayID:     "gw-1",
			CorrelationID: fmt.Sprintf("corr-%d", i),
			BodyHash:      fmt.Sprintf("body-%d", i),
			Payload:       []byte(fmt.Sprintf(`{"model":"gpt-4o","i":%d}`, i)),
		}, signer)
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if r.Seq != uint64(i+1) {
			t.Errorf("append %d: seq = %d, want %d", i, r.Seq, i+1)
		}
	}

	// A second gateway gets its own genesis chain.
	if _, err := db.AppendReceipt(ctx, receipt.Record{GatewayID: "gw-2", Payload: []byte(`{}`)}, signer); err != nil {
		t.Fatalf("gw-2 append: %v", err)
	}

	chain, err := db.ListReceipts(ctx, "gw-1")
	if err != nil {
		t.Fatalf("list gw-1: %v", err)
	}
	if len(chain) != 4 {
		t.Fatalf("gw-1 chain len = %d, want 4", len(chain))
	}
	if err := receipt.VerifyChain(chain, signer.Public()); err != nil {
		t.Fatalf("stored gw-1 chain failed to verify: %v", err)
	}

	c2, err := db.ListReceipts(ctx, "gw-2")
	if err != nil {
		t.Fatalf("list gw-2: %v", err)
	}
	if len(c2) != 1 || c2[0].Seq != 1 || c2[0].PrevHash != nil {
		t.Fatalf("gw-2 chain = %+v, want one genesis receipt", c2)
	}
	if err := receipt.VerifyChain(c2, signer.Public()); err != nil {
		t.Fatalf("gw-2 chain failed to verify: %v", err)
	}

	// Tamper with a persisted receipt's payload: verification must catch it.
	chain[1].Payload = []byte(`{"model":"tampered"}`)
	if err := receipt.VerifyChain(chain, signer.Public()); err == nil {
		t.Fatal("tampered chain verified — tamper-evidence is broken")
	}
}
