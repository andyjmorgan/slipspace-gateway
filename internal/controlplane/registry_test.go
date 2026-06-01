package controlplane

import (
	"context"
	"errors"
	"testing"
	"time"
)

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestMemoryRegistry_Register(t *testing.T) {
	t.Run("missing id is an error", func(t *testing.T) {
		r := NewMemoryRegistry()
		if _, err := r.Register(context.Background(), RegisterInput{}); !errors.Is(err, ErrMissingGatewayID) {
			t.Fatalf("want ErrMissingGatewayID, got %v", err)
		}
	})

	t.Run("new gateway records RegisteredAt and LastSeen", func(t *testing.T) {
		r := NewMemoryRegistry()
		now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		r.now = fixedClock(now)

		g, err := r.Register(context.Background(), RegisterInput{
			ID:      "gw-1",
			Version: "v1.2.0",
			Labels:  map[string]string{"role": "edge"},
		})
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		if g.RegisteredAt != now || g.LastSeen != now {
			t.Fatalf("timestamps = %v / %v, want %v", g.RegisteredAt, g.LastSeen, now)
		}
		if g.Version != "v1.2.0" || g.Labels["role"] != "edge" {
			t.Fatalf("unexpected record: %+v", g)
		}
	})

	t.Run("re-register refreshes but preserves RegisteredAt", func(t *testing.T) {
		r := NewMemoryRegistry()
		t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		r.now = fixedClock(t0)
		if _, err := r.Register(context.Background(), RegisterInput{ID: "gw-1", Version: "v1"}); err != nil {
			t.Fatal(err)
		}

		t1 := t0.Add(time.Minute)
		r.now = fixedClock(t1)
		g, err := r.Register(context.Background(), RegisterInput{ID: "gw-1", Version: "v2"})
		if err != nil {
			t.Fatal(err)
		}
		if g.RegisteredAt != t0 {
			t.Fatalf("RegisteredAt = %v, want preserved %v", g.RegisteredAt, t0)
		}
		if g.LastSeen != t1 || g.Version != "v2" {
			t.Fatalf("refresh not applied: %+v", g)
		}
	})
}

func TestMemoryRegistry_Heartbeat(t *testing.T) {
	t.Run("missing id is an error", func(t *testing.T) {
		r := NewMemoryRegistry()
		if _, err := r.Heartbeat(context.Background(), HeartbeatInput{}); !errors.Is(err, ErrMissingGatewayID) {
			t.Fatalf("want ErrMissingGatewayID, got %v", err)
		}
	})

	t.Run("heartbeat for unknown gateway self-registers", func(t *testing.T) {
		r := NewMemoryRegistry()
		now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		r.now = fixedClock(now)

		g, err := r.Heartbeat(context.Background(), HeartbeatInput{
			ID:                 "gw-9",
			Version:            "v3",
			CachedConfigHashes: []string{"sha256:abc"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if g.RegisteredAt != now {
			t.Fatalf("self-register should stamp RegisteredAt, got %v", g.RegisteredAt)
		}
		if len(g.CachedConfigHashes) != 1 || g.CachedConfigHashes[0] != "sha256:abc" {
			t.Fatalf("cached hashes not recorded: %+v", g.CachedConfigHashes)
		}
	})

	t.Run("heartbeat updates an existing gateway", func(t *testing.T) {
		r := NewMemoryRegistry()
		t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		r.now = fixedClock(t0)
		if _, err := r.Register(context.Background(), RegisterInput{ID: "gw-1", Version: "v1"}); err != nil {
			t.Fatal(err)
		}
		t1 := t0.Add(30 * time.Second)
		r.now = fixedClock(t1)
		g, err := r.Heartbeat(context.Background(), HeartbeatInput{ID: "gw-1", Version: "v1"})
		if err != nil {
			t.Fatal(err)
		}
		if g.LastSeen != t1 || g.RegisteredAt != t0 {
			t.Fatalf("heartbeat timestamps wrong: %+v", g)
		}
	})
}

func TestMemoryRegistry_List(t *testing.T) {
	r := NewMemoryRegistry()
	for _, id := range []string{"gw-c", "gw-a", "gw-b"} {
		if _, err := r.Register(context.Background(), RegisterInput{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"gw-a", "gw-b", "gw-c"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, g := range got {
		if g.ID != want[i] {
			t.Fatalf("order[%d] = %q, want %q", i, g.ID, want[i])
		}
	}
}

func TestMemoryRegistry_ListReturnsCopies(t *testing.T) {
	r := NewMemoryRegistry()
	if _, err := r.Register(context.Background(), RegisterInput{
		ID:     "gw-1",
		Labels: map[string]string{"role": "edge"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := r.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Mutating the returned slice's maps must not corrupt the registry.
	got[0].Labels["role"] = "tampered"
	again, err := r.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Labels["role"] != "edge" {
		t.Fatalf("registry state leaked: %q", again[0].Labels["role"])
	}
}

func TestCloneHelpers(t *testing.T) {
	if cloneLabels(nil) != nil {
		t.Fatal("cloneLabels(nil) should be nil")
	}
	if cloneStrings(nil) != nil {
		t.Fatal("cloneStrings(nil) should be nil")
	}
	if got := cloneStrings([]string{"a"}); len(got) != 1 || got[0] != "a" {
		t.Fatalf("cloneStrings copy wrong: %+v", got)
	}
}
