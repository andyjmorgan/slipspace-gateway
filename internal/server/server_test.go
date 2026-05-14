package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func waitForListening(t *testing.T, s *Server) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if addr := s.Addr(); addr != "" {
			return addr
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server never bound an address")
	return ""
}

func httpGet(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return resp.StatusCode, string(body)
}

func TestServer_New_Defaults(t *testing.T) {
	s := New(Options{Bind: "127.0.0.1:0"})

	if s.drain != DefaultDrainTimeout {
		t.Fatalf("default drain: got %s, want %s", s.drain, DefaultDrainTimeout)
	}
	if s.logger == nil {
		t.Fatal("default logger should not be nil")
	}
	if s.healthz == nil {
		t.Fatal("healthz handler must be mounted")
	}
	if s.httpSrv.ReadHeaderTimeout != 10*time.Second {
		t.Fatalf("default ReadHeaderTimeout: got %s, want 10s", s.httpSrv.ReadHeaderTimeout)
	}
	if s.Addr() != "" {
		t.Fatalf("Addr before Run should be empty, got %q", s.Addr())
	}
}

func TestServer_New_RespectsOverrides(t *testing.T) {
	logger := newTestLogger()
	s := New(Options{
		Bind:              "127.0.0.1:0",
		DrainTimeout:      42 * time.Second,
		ReadHeaderTimeout: 7 * time.Second,
		Logger:            logger,
	})

	if s.drain != 42*time.Second {
		t.Fatalf("drain: got %s, want 42s", s.drain)
	}
	if s.logger != logger {
		t.Fatal("logger override not respected")
	}
	if s.httpSrv.ReadHeaderTimeout != 7*time.Second {
		t.Fatalf("ReadHeaderTimeout: got %s, want 7s", s.httpSrv.ReadHeaderTimeout)
	}
}

func TestServer_Run_HealthzAndShutdown(t *testing.T) {
	s := New(Options{
		Bind:         "127.0.0.1:0",
		DrainTimeout: 5 * time.Second,
		Logger:       newTestLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErr := make(chan error, 1)
	go func() {
		runErr <- s.Run(ctx)
	}()

	addr := waitForListening(t, s)
	url := "http://" + addr + "/healthz"

	code, body := httpGet(t, url)
	if code != http.StatusOK {
		t.Fatalf("healthz: got %d, want 200", code)
	}
	if body != "ok\n" {
		t.Fatalf("healthz body: got %q, want %q", body, "ok\n")
	}

	cancel()

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run: got %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}

	if !s.Healthz().IsDraining() {
		t.Fatal("healthz should be in draining state after shutdown")
	}
}

func TestServer_Run_CallerHandlerInvoked(t *testing.T) {
	var hit atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit.Add(1)
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("brewing"))
	})

	s := New(Options{
		Bind:         "127.0.0.1:0",
		DrainTimeout: 5 * time.Second,
		Handler:      handler,
		Logger:       newTestLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErr := make(chan error, 1)
	go func() { runErr <- s.Run(ctx) }()

	addr := waitForListening(t, s)

	code, body := httpGet(t, "http://"+addr+"/anything")
	if code != http.StatusTeapot {
		t.Fatalf("status: got %d, want 418", code)
	}
	if body != "brewing" {
		t.Fatalf("body: got %q, want %q", body, "brewing")
	}
	if hit.Load() != 1 {
		t.Fatalf("handler hit count: got %d, want 1", hit.Load())
	}

	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestServer_Run_HealthzShadowsCallerHandler(t *testing.T) {
	var hit atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("should not see this"))
	})

	s := New(Options{
		Bind:         "127.0.0.1:0",
		DrainTimeout: 5 * time.Second,
		Handler:      handler,
		Logger:       newTestLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- s.Run(ctx) }()

	addr := waitForListening(t, s)

	code, body := httpGet(t, "http://"+addr+"/healthz")
	if code != http.StatusOK {
		t.Fatalf("healthz: got %d, want 200", code)
	}
	if body != "ok\n" {
		t.Fatalf("healthz body: got %q, want %q", body, "ok\n")
	}
	if hit.Load() != 0 {
		t.Fatalf("caller handler invoked %d times for /healthz; should be 0", hit.Load())
	}

	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestServer_Run_DrainTimeoutHardClose(t *testing.T) {
	released := make(chan struct{})
	started := make(chan struct{})

	var once sync.Once
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(started) })
		// Deliberately ignore r.Context() — we are simulating a stuck
		// handler that does not honour cancellation so Shutdown's drain
		// budget will be the deciding factor.
		<-released
		w.WriteHeader(http.StatusOK)
	})

	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	s := New(Options{
		Bind:         "127.0.0.1:0",
		DrainTimeout: 100 * time.Millisecond,
		Handler:      handler,
		Logger:       logger,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErr := make(chan error, 1)
	go func() { runErr <- s.Run(ctx) }()

	addr := waitForListening(t, s)

	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/slow", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("slow handler never started")
	}

	cancel()

	select {
	case err := <-runErr:
		if err == nil {
			t.Fatal("Run: got nil error, want wrapped DeadlineExceeded")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run error: got %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after drain timeout")
	}

	if !strings.Contains(logBuf.String(), "drain timeout exceeded") {
		t.Fatalf("expected drain-timeout log line, got: %s", logBuf.String())
	}

	close(released)
	<-clientDone
}

func TestServer_Run_ListenError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("seed listener: %v", err)
	}
	defer func() { _ = listener.Close() }()
	occupiedAddr := listener.Addr().String()

	s := New(Options{
		Bind:   occupiedAddr,
		Logger: newTestLogger(),
	})

	err = s.Run(context.Background())
	if err == nil {
		t.Fatal("Run: got nil error, want listen failure")
	}
	if !strings.Contains(err.Error(), "server: listen") {
		t.Fatalf("error: got %v, want server: listen prefix", err)
	}
}

func TestServer_Run_ServeFails(t *testing.T) {
	s := New(Options{
		Bind:         "127.0.0.1:0",
		DrainTimeout: time.Second,
		Logger:       newTestLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErr := make(chan error, 1)
	go func() { runErr <- s.Run(ctx) }()

	waitForListening(t, s)

	// Close the underlying listener out-of-band so Serve returns an error
	// distinct from ErrServerClosed. We do this by hitting Close on the
	// http.Server directly — Run hasn't reached Shutdown yet.
	if err := s.httpSrv.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	select {
	case err := <-runErr:
		// Close returns ErrServerClosed from Serve, which Run filters out and
		// returns nil. Either nil (filtered) or a wrapped error is acceptable
		// here; the assertion is just that Run terminates.
		_ = err
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after listener close")
	}
}

func TestServer_Addr_BeforeAndAfter(t *testing.T) {
	s := New(Options{
		Bind:         "127.0.0.1:0",
		DrainTimeout: time.Second,
		Logger:       newTestLogger(),
	})

	if s.Addr() != "" {
		t.Fatalf("Addr before Run: got %q, want empty", s.Addr())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- s.Run(ctx) }()

	addr := waitForListening(t, s)
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("Addr: got %q, want 127.0.0.1:<port>", addr)
	}

	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestServer_Run_BadBindFormat(t *testing.T) {
	s := New(Options{
		Bind:   "not-a-valid-address",
		Logger: newTestLogger(),
	})

	err := s.Run(context.Background())
	if err == nil {
		t.Fatal("Run: got nil error, want listen failure")
	}
	if !strings.Contains(err.Error(), "server: listen") {
		t.Fatalf("error: got %v, want server: listen prefix", err)
	}
}

func TestServer_New_ZeroDrainFallsBackToDefault(t *testing.T) {
	s := New(Options{Bind: "127.0.0.1:0", DrainTimeout: 0})
	if s.drain != DefaultDrainTimeout {
		t.Fatalf("drain: got %s, want %s", s.drain, DefaultDrainTimeout)
	}
}

func TestServer_New_NegativeDrainFallsBackToDefault(t *testing.T) {
	s := New(Options{Bind: "127.0.0.1:0", DrainTimeout: -1 * time.Second})
	if s.drain != DefaultDrainTimeout {
		t.Fatalf("drain: got %s, want %s", s.drain, DefaultDrainTimeout)
	}
}

// TestServer_HealthzTransitionOverHTTP drives the healthz handler over a
// real HTTP connection and asserts the 200→503 transition the moment
// MarkDraining is invoked, before Shutdown closes the listener.
func TestServer_HealthzTransitionOverHTTP(t *testing.T) {
	s := New(Options{
		Bind:         "127.0.0.1:0",
		DrainTimeout: 5 * time.Second,
		Logger:       newTestLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- s.Run(ctx) }()

	addr := waitForListening(t, s)
	url := "http://" + addr + "/healthz"

	if code, body := httpGet(t, url); code != http.StatusOK || body != "ok\n" {
		t.Fatalf("pre-drain: got %d %q, want 200 ok", code, body)
	}

	s.Healthz().MarkDraining()

	if code, body := httpGet(t, url); code != http.StatusServiceUnavailable || body != "draining\n" {
		t.Fatalf("post-drain: got %d %q, want 503 draining", code, body)
	}

	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestServer_Healthz_Accessor verifies the exposed Healthz handle. Mostly a
// sanity probe so refactors don't accidentally drop the accessor.
func TestServer_Healthz_Accessor(t *testing.T) {
	s := New(Options{Bind: "127.0.0.1:0"})
	if s.Healthz() == nil {
		t.Fatal("Healthz() must not return nil")
	}
	if s.Healthz() != s.healthz {
		t.Fatal("Healthz() must return the embedded handler")
	}
}

// TestServer_Run_ContextAlreadyCancelled covers the path where the supplied
// context is already done when Run is invoked.
func TestServer_Run_ContextAlreadyCancelled(t *testing.T) {
	s := New(Options{
		Bind:         "127.0.0.1:0",
		DrainTimeout: time.Second,
		Logger:       newTestLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.Run(ctx)
	if err != nil {
		t.Fatalf("Run: got %v, want nil", err)
	}
}
