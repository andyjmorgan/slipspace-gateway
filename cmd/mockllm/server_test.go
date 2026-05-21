package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) (*httptest.Server, *registry) {
	t.Helper()
	reg := newRegistry()
	srv := newServer(reg)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts, reg
}

func TestServer_ProviderHandler_Basic(t *testing.T) {
	t.Parallel()
	ts, reg := newTestServer(t)

	reg.add("", CannedResponse{Path: "/v1/chat/completions", Status: 200, Body: `{"ok":true}`})

	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestServer_ProviderHandler_SessionRoutesToCorrectPool(t *testing.T) {
	t.Parallel()
	ts, reg := newTestServer(t)

	reg.add("alice", CannedResponse{Path: "/v1/chat/completions", Status: 200, Body: `alice`})
	reg.add("bob", CannedResponse{Path: "/v1/chat/completions", Status: 200, Body: `bob`})

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set(sessionHeader, "alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("alice: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "alice" {
		t.Errorf("alice body = %q; want alice", string(body))
	}

	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set(sessionHeader, "bob")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("bob: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "bob" {
		t.Errorf("bob body = %q; want bob", string(body))
	}
}

func TestServer_ProviderHandler_NoSessionFallsBackToGlobal(t *testing.T) {
	t.Parallel()
	ts, reg := newTestServer(t)
	reg.add("", CannedResponse{Path: "/v1/chat/completions", Status: 200, Body: `global`})

	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "global" {
		t.Errorf("body = %q; want global", string(body))
	}
}

func TestServer_ProviderHandler_MaxResponsesSequence(t *testing.T) {
	t.Parallel()
	ts, reg := newTestServer(t)

	// Two 503s, then a 200 that's the steady-state response.
	reg.add("seq", CannedResponse{Path: "/v1/chat/completions", Status: 503, Body: `down`, MaxResponses: 2})
	reg.add("seq", CannedResponse{Path: "/v1/chat/completions", Status: 200, Body: `ok`})

	statuses := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set(sessionHeader, "seq")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		statuses = append(statuses, resp.StatusCode)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	want := []int{503, 503, 200}
	for i, s := range statuses {
		if s != want[i] {
			t.Errorf("statuses[%d] = %d; want %d (full=%v)", i, s, want[i], statuses)
		}
	}
}

func TestServer_ProviderHandler_DelayHonoured(t *testing.T) {
	t.Parallel()
	ts, reg := newTestServer(t)
	reg.add("", CannedResponse{Path: "/v1/chat/completions", Status: 200, DelayMs: 150, Body: "delayed"})

	start := time.Now()
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = resp.Body.Close()
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Errorf("response arrived in %v; expected ≥100ms (delay configured 150ms)", elapsed)
	}
}

func TestServer_ProviderHandler_BehaviorClose(t *testing.T) {
	t.Parallel()
	ts, reg := newTestServer(t)
	reg.add("", CannedResponse{Path: "/v1/chat/completions", Behavior: BehaviorClose})

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{}`))
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected transport error from behavior=close; got nil")
	}
	// Underlying error varies (EOF / connection reset) — accept any
	// non-nil; the important thing is the client saw no status.
}

func TestServer_ProviderHandler_BehaviorHang_ClientCancelTerminates(t *testing.T) {
	t.Parallel()
	ts, reg := newTestServer(t)
	reg.add("", CannedResponse{Path: "/v1/chat/completions", Behavior: BehaviorHang})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(`{}`))
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected client-cancel error from behavior=hang; got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("err = %v; want deadline exceeded", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("hang behavior took %v to terminate after ctx cancel; should be ~200ms", elapsed)
	}
}

func TestServer_ControlPlane_SessionScopedPostAndDelete(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t)

	body, _ := json.Marshal(CannedResponse{Path: "/v1/chat/completions", Status: 200, Body: "alice"})

	resp, err := http.Post(ts.URL+"/control/responses?session=alice", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	// Verify by firing a request under that session.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set(sessionHeader, "alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	gotBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(gotBody) != "alice" {
		t.Errorf("alice session response = %q", string(gotBody))
	}

	// DELETE just alice's pool.
	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/control/responses?session=alice", http.NoBody)
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	_ = delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE status = %d", delResp.StatusCode)
	}

	// alice's pool now empty — next request should 404.
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set(sessionHeader, "alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post-clear: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("after clear: status = %d; want 404", resp.StatusCode)
	}
}

func TestServer_ControlPlane_StateExposesSessions(t *testing.T) {
	t.Parallel()
	ts, reg := newTestServer(t)
	reg.add("", CannedResponse{Path: "/a", Status: 200})
	reg.add("alice", CannedResponse{Path: "/b", Status: 200})

	resp, err := http.Get(ts.URL + "/control/state")
	if err != nil {
		t.Fatalf("GET state: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var state struct {
		Responses []CannedResponse            `json:"responses"`
		Sessions  map[string][]CannedResponse `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(state.Responses) != 1 || state.Responses[0].Path != "/a" {
		t.Errorf("global pool wrong: %+v", state.Responses)
	}
	if pool, ok := state.Sessions["alice"]; !ok || len(pool) != 1 || pool[0].Path != "/b" {
		t.Errorf("alice session wrong: %+v", state.Sessions)
	}
}

func TestServer_ControlPlane_CapturedFiltersBySession(t *testing.T) {
	t.Parallel()
	ts, reg := newTestServer(t)
	reg.add("", CannedResponse{Path: "/v1/chat/completions", Status: 200, Body: "ok"})

	for _, sess := range []string{"alice", "bob", "alice"} {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set(sessionHeader, sess)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("fire %s: %v", sess, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	resp, err := http.Get(ts.URL + "/control/captured?session=alice")
	if err != nil {
		t.Fatalf("GET captured: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var wrap struct {
		Captured []CapturedRequest `json:"captured"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(wrap.Captured) != 2 {
		t.Errorf("expected 2 captures for alice; got %d (%+v)", len(wrap.Captured), wrap.Captured)
	}
	for i, c := range wrap.Captured {
		if c.SessionID != "alice" {
			t.Errorf("captured[%d].SessionID = %q; want alice", i, c.SessionID)
		}
	}
}

func TestServer_ControlPlane_DeleteWithoutSessionClearsAll(t *testing.T) {
	t.Parallel()
	ts, reg := newTestServer(t)
	reg.add("", CannedResponse{Path: "/a", Status: 200})
	reg.add("alice", CannedResponse{Path: "/b", Status: 200})

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/control/responses", http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	_ = resp.Body.Close()

	if _, ok := reg.match("", "POST", "/a", ""); ok {
		t.Error("global pool not cleared by session-less DELETE")
	}
	if _, ok := reg.match("alice", "POST", "/b", ""); ok {
		t.Error("session pool not cleared by session-less DELETE (back-compat broken)")
	}
}
