package reconciler

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane"
)

var errTest = errors.New("test error")

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNew(t *testing.T) {
	if _, err := New(Options{GatewayID: "gw"}); err == nil {
		t.Fatal("want error for missing endpoint")
	}
	if _, err := New(Options{Endpoint: "x:1"}); err == nil {
		t.Fatal("want error for missing gateway id")
	}

	r, err := New(Options{Endpoint: "x:1", GatewayID: "gw"})
	if err != nil {
		t.Fatal(err)
	}
	if r.opts.Interval != 20*time.Second {
		t.Fatalf("default interval = %v, want 20s", r.opts.Interval)
	}

	r2, err := New(Options{Endpoint: "x:1", GatewayID: "gw", Interval: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if r2.opts.Interval != 5*time.Second {
		t.Fatalf("explicit interval overwritten: %v", r2.opts.Interval)
	}
}

func TestTokenCreds(t *testing.T) {
	c := tokenCreds{token: "abc", secure: true}
	md, err := c.GetRequestMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if md["authorization"] != "Bearer abc" {
		t.Fatalf("authorization = %q", md["authorization"])
	}
	if !c.RequireTransportSecurity() {
		t.Fatal("secure=true must require transport security")
	}
	if (tokenCreds{secure: false}).RequireTransportSecurity() {
		t.Fatal("secure=false must not require transport security")
	}
}

func TestDial(t *testing.T) {
	cases := []struct {
		name string
		opts Options
	}{
		{"insecure no token", Options{Endpoint: "127.0.0.1:1", GatewayID: "g"}},
		{"insecure with token", Options{Endpoint: "127.0.0.1:1", GatewayID: "g", Token: "t"}},
		{"tls default config", Options{Endpoint: "127.0.0.1:1", GatewayID: "g", TLS: true, Token: "t"}},
		{"tls custom config", Options{Endpoint: "127.0.0.1:1", GatewayID: "g", TLS: true, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Reconciler{opts: tc.opts}
			conn, err := r.dial()
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			_ = conn.Close()
		})
	}
}

func TestLogHelpers_NilLoggerSafe(t *testing.T) {
	r := &Reconciler{opts: Options{Endpoint: "e", GatewayID: "g"}} // nil logger
	r.logInfo("info")
	r.logWarn("warn", errTest)
	r.logError("error", errTest)
}

func startCP(t *testing.T, token string) (*stubRegistry, string) {
	t.Helper()
	reg := newStubRegistry()
	srv := controlplane.NewGRPCServer(reg, nil, controlplane.GRPCServerOptions{Token: token})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return reg, lis.Addr().String()
}

func TestRun_RegisterAndHeartbeat(t *testing.T) {
	reg, addr := startCP(t, "s3cret")

	r, err := New(Options{
		Endpoint:  addr,
		Token:     "s3cret",
		GatewayID: "gw-1",
		Version:   "v1",
		Interval:  20 * time.Millisecond,
		Logger:    discardLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	first := waitForGateway(t, reg, "gw-1")
	// Confirm a subsequent heartbeat advances LastSeen.
	deadline := time.Now().Add(2 * time.Second)
	for {
		gws, _ := reg.List(context.Background())
		if len(gws) == 1 && gws[0].LastSeen.After(first.LastSeen) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no heartbeat advanced LastSeen")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRun_DialErrorIsNonFatal(t *testing.T) {
	// "%zz" fails grpc target parsing synchronously, so dial() returns an
	// error and Run logs once and exits without ever touching the loop.
	r := &Reconciler{opts: Options{
		Endpoint:  "%zz",
		GatewayID: "g",
		Interval:  time.Second,
		Logger:    discardLogger(),
	}}
	done := make(chan struct{})
	go func() { r.Run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return on dial error")
	}
}

func TestRun_AuthFailureIsNonFatal(t *testing.T) {
	reg, addr := startCP(t, "right-token")

	r, err := New(Options{
		Endpoint:  addr,
		Token:     "wrong-token",
		GatewayID: "gw-denied",
		Interval:  15 * time.Millisecond,
		Logger:    discardLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	// Let it attempt register + at least one heartbeat (both rejected).
	time.Sleep(120 * time.Millisecond)
	cancel()
	<-done

	gws, _ := reg.List(context.Background())
	if len(gws) != 0 {
		t.Fatalf("auth-rejected gateway must never register: %+v", gws)
	}
}

func waitForGateway(t *testing.T, reg *stubRegistry, id string) controlplane.Gateway {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		gws, _ := reg.List(context.Background())
		for _, g := range gws {
			if g.ID == id {
				return g
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("gateway %q never registered", id)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
