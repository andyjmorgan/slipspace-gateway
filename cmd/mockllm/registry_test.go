package main

import (
	"testing"
)

func TestRegistry_AddAndMatch_Global(t *testing.T) {
	t.Parallel()
	r := newRegistry()
	r.add("", CannedResponse{Path: "/v1/chat/completions", Status: 200, Body: "ok"})

	got, ok := r.match("", "POST", "/v1/chat/completions", "")
	if !ok {
		t.Fatal("match: want ok")
	}
	if got.Status != 200 || got.Body != "ok" {
		t.Errorf("got %+v", got)
	}
}

func TestRegistry_SessionScoped(t *testing.T) {
	t.Parallel()
	r := newRegistry()
	r.add("alice", CannedResponse{Path: "/v1/chat/completions", Status: 200, Body: "alice"})
	r.add("bob", CannedResponse{Path: "/v1/chat/completions", Status: 200, Body: "bob"})

	a, ok := r.match("alice", "POST", "/v1/chat/completions", "")
	if !ok || a.Body != "alice" {
		t.Errorf("alice got %+v ok=%v", a, ok)
	}
	b, ok := r.match("bob", "POST", "/v1/chat/completions", "")
	if !ok || b.Body != "bob" {
		t.Errorf("bob got %+v ok=%v", b, ok)
	}
}

func TestRegistry_FallbackToGlobal(t *testing.T) {
	t.Parallel()
	r := newRegistry()
	r.add("", CannedResponse{Path: "/v1/chat/completions", Status: 200, Body: "global"})

	got, ok := r.match("unknown-session", "POST", "/v1/chat/completions", "")
	if !ok {
		t.Fatal("expected fall-back to global pool")
	}
	if got.Body != "global" {
		t.Errorf("got %+v; want global", got)
	}
}

func TestRegistry_SessionWinsOverGlobal(t *testing.T) {
	t.Parallel()
	r := newRegistry()
	r.add("", CannedResponse{Path: "/v1/chat/completions", Status: 200, Body: "global"})
	r.add("alice", CannedResponse{Path: "/v1/chat/completions", Status: 200, Body: "alice"})

	got, ok := r.match("alice", "POST", "/v1/chat/completions", "")
	if !ok || got.Body != "alice" {
		t.Errorf("got %+v ok=%v; want session win", got, ok)
	}
}

func TestRegistry_NoMatch(t *testing.T) {
	t.Parallel()
	r := newRegistry()
	r.add("", CannedResponse{Path: "/v1/messages", Status: 200})

	if _, ok := r.match("", "POST", "/v1/chat/completions", ""); ok {
		t.Error("expected no match for different path")
	}
}

func TestRegistry_MaxResponses_PopsAtZero(t *testing.T) {
	t.Parallel()
	r := newRegistry()
	r.add("", CannedResponse{Path: "/v1/chat/completions", Status: 503, MaxResponses: 2})
	r.add("", CannedResponse{Path: "/v1/chat/completions", Status: 200})

	// First two hits exhaust the 503 entry.
	for i := 0; i < 2; i++ {
		got, ok := r.match("", "POST", "/v1/chat/completions", "")
		if !ok || got.Status != 503 {
			t.Fatalf("attempt %d: got %+v ok=%v; want 503", i, got, ok)
		}
	}
	// Third hit falls through to the 200.
	got, ok := r.match("", "POST", "/v1/chat/completions", "")
	if !ok || got.Status != 200 {
		t.Fatalf("third attempt: got %+v ok=%v; want 200", got, ok)
	}
}

func TestRegistry_MaxResponses_OneShotRemoves(t *testing.T) {
	t.Parallel()
	r := newRegistry()
	r.add("", CannedResponse{Path: "/v1/chat/completions", Status: 503, MaxResponses: 1})

	if _, ok := r.match("", "POST", "/v1/chat/completions", ""); !ok {
		t.Fatal("first match expected")
	}
	if _, ok := r.match("", "POST", "/v1/chat/completions", ""); ok {
		t.Fatal("second match should miss after pop")
	}
}

func TestRegistry_MaxResponses_ZeroIsUnlimited(t *testing.T) {
	t.Parallel()
	r := newRegistry()
	r.add("", CannedResponse{Path: "/v1/chat/completions", Status: 200, MaxResponses: 0})

	for i := 0; i < 5; i++ {
		if _, ok := r.match("", "POST", "/v1/chat/completions", ""); !ok {
			t.Fatalf("attempt %d: legacy unlimited should always match", i)
		}
	}
}

func TestRegistry_MaxResponses_ScopedPerSession(t *testing.T) {
	t.Parallel()
	r := newRegistry()
	r.add("alice", CannedResponse{Path: "/v1/chat/completions", Status: 503, MaxResponses: 1})
	r.add("bob", CannedResponse{Path: "/v1/chat/completions", Status: 503, MaxResponses: 1})

	// Drain alice — bob's entry must remain.
	if _, ok := r.match("alice", "POST", "/v1/chat/completions", ""); !ok {
		t.Fatal("alice first match expected")
	}
	if _, ok := r.match("alice", "POST", "/v1/chat/completions", ""); ok {
		t.Fatal("alice second match should miss")
	}
	if _, ok := r.match("bob", "POST", "/v1/chat/completions", ""); !ok {
		t.Fatal("bob's pool should still have its entry")
	}
}

func TestRegistry_ClearSession(t *testing.T) {
	t.Parallel()
	r := newRegistry()
	r.add("alice", CannedResponse{Path: "/v1/chat/completions", Status: 200})
	r.add("bob", CannedResponse{Path: "/v1/chat/completions", Status: 200})

	r.clearSession("alice")

	if _, ok := r.match("alice", "POST", "/v1/chat/completions", ""); ok {
		t.Error("alice was cleared; match should miss")
	}
	if _, ok := r.match("bob", "POST", "/v1/chat/completions", ""); !ok {
		t.Error("bob should be untouched")
	}
}

func TestRegistry_ClearAll(t *testing.T) {
	t.Parallel()
	r := newRegistry()
	r.add("", CannedResponse{Path: "/a", Status: 200})
	r.add("alice", CannedResponse{Path: "/b", Status: 200})

	r.clearAll()

	if _, ok := r.match("", "POST", "/a", ""); ok {
		t.Error("global pool not cleared")
	}
	if _, ok := r.match("alice", "POST", "/b", ""); ok {
		t.Error("alice pool not cleared")
	}
}

func TestRegistry_Replace_GlobalOnly(t *testing.T) {
	t.Parallel()
	r := newRegistry()
	r.add("alice", CannedResponse{Path: "/v1/chat/completions", Status: 200, Body: "alice"})

	r.replace([]CannedResponse{{Path: "/v1/messages", Status: 200, Body: "loaded"}})

	// replace wipes everything including alice.
	if _, ok := r.match("alice", "POST", "/v1/chat/completions", ""); ok {
		t.Error("replace should wipe session pools")
	}
	got, ok := r.match("", "POST", "/v1/messages", "")
	if !ok || got.Body != "loaded" {
		t.Errorf("global pool: got %+v ok=%v", got, ok)
	}
}

func TestRegistry_SnapshotSessions_ExcludesGlobal(t *testing.T) {
	t.Parallel()
	r := newRegistry()
	r.add("", CannedResponse{Path: "/a", Status: 200})
	r.add("alice", CannedResponse{Path: "/b", Status: 200})

	snap := r.snapshotSessions()
	if _, ok := snap[""]; ok {
		t.Error("snapshotSessions returned the global pool; should be excluded")
	}
	if len(snap["alice"]) != 1 {
		t.Errorf("alice pool size = %d; want 1", len(snap["alice"]))
	}
}

func TestRegistry_RequestBodyContains(t *testing.T) {
	t.Parallel()
	r := newRegistry()
	r.add("", CannedResponse{Path: "/v1/chat/completions", RequestBodyContains: "gpt-4o", Status: 200, Body: "match"})
	r.add("", CannedResponse{Path: "/v1/chat/completions", Status: 200, Body: "fallback"})

	got, ok := r.match("", "POST", "/v1/chat/completions", `{"model":"gpt-4o"}`)
	if !ok || got.Body != "match" {
		t.Errorf("body-contains match: got %+v ok=%v", got, ok)
	}
	got, ok = r.match("", "POST", "/v1/chat/completions", `{"model":"claude-3-5"}`)
	if !ok || got.Body != "fallback" {
		t.Errorf("body-contains miss falls through: got %+v ok=%v", got, ok)
	}
}

func TestRegistry_MethodCaseInsensitive(t *testing.T) {
	t.Parallel()
	r := newRegistry()
	r.add("", CannedResponse{Method: "POST", Path: "/x", Status: 200})

	if _, ok := r.match("", "post", "/x", ""); !ok {
		t.Error("method matcher should be case-insensitive")
	}
}
