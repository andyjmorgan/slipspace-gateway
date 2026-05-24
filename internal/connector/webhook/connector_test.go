package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
)

// ---------- helpers ----------

// allowAllResolver bypasses the SSRF guard in tests by claiming every
// host resolves to a public-routable IP.
func allowAllResolver(_ context.Context, _ string) ([]net.IP, error) {
	return []net.IP{net.ParseIP("198.51.100.1")}, nil
}

func mustNewWithServer(t *testing.T, h http.Handler) *Connector {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Setenv("TEST_HOOK_SECRET", "fixture-secret")
	c, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{ //nolint:gosec // G101: SecretRef is env: indirection, not a literal
			Type: "webhook", Name: "test",
			URL: srv.URL, SecretRef: "env:TEST_HOOK_SECRET", TimeoutMS: 5000,
		},
		AllowPrivateNetworks: true,
		NowUnix:              func() int64 { return 1715000000 },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func writeFixture(t *testing.T, body []byte) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "1715000000000000001-42.ndjson.zst")
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// computeExpectedSig is the reference HMAC the receiver would compute
// to verify the request. Used by tests to assert what the connector
// emits.
func computeExpectedSig(secret string, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", ts)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// ---------- New ----------

func TestNew_RejectsWrongType(t *testing.T) {
	_, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{Type: "s3", Name: "x"},
	})
	if err == nil || !strings.Contains(err.Error(), "want webhook") {
		t.Errorf("got %v", err)
	}
}

func TestNew_RejectsMissingFields(t *testing.T) {
	cases := []struct {
		name string
		cfg  contractsconfig.Connector
		want string
	}{
		{"no name", contractsconfig.Connector{ //nolint:gosec // G101: fixture
			Type: "webhook", URL: "https://x", SecretRef: "env:S", TimeoutMS: 1000,
		}, "Name"},
		{"no url", contractsconfig.Connector{ //nolint:gosec // G101: fixture
			Type: "webhook", Name: "x", SecretRef: "env:S", TimeoutMS: 1000,
		}, "URL"},
		{"no secret ref", contractsconfig.Connector{
			Type: "webhook", Name: "x", URL: "https://x", TimeoutMS: 1000,
		}, "SecretRef"},
		{"no timeout", contractsconfig.Connector{ //nolint:gosec // G101: fixture
			Type: "webhook", Name: "x", URL: "https://x", SecretRef: "env:S",
		}, "TimeoutMS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("S", "x")
			_, err := New(context.Background(), Options{Config: tc.cfg, AllowPrivateNetworks: true})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v", err)
			}
		})
	}
}

func TestNew_RejectsMissingSecret(t *testing.T) {
	_, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{ //nolint:gosec // G101: fixture
			Type: "webhook", Name: "x", URL: "https://x",
			SecretRef: "env:DEFINITELY_NOT_SET_HOOK", TimeoutMS: 1000,
		},
		AllowPrivateNetworks: true,
	})
	if err == nil || !strings.Contains(err.Error(), "load secret_ref") {
		t.Errorf("got %v", err)
	}
}

func TestNew_RejectsEmptySecret(t *testing.T) {
	t.Setenv("EMPTY_HOOK_SECRET", "")
	_, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{ //nolint:gosec // G101: fixture
			Type: "webhook", Name: "x", URL: "https://x",
			SecretRef: "env:EMPTY_HOOK_SECRET", TimeoutMS: 1000,
		},
		AllowPrivateNetworks: true,
	})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("got %v", err)
	}
}

func TestNew_DefaultsClientAndResolver(t *testing.T) {
	t.Setenv("H", "k")
	c, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{ //nolint:gosec // G101: fixture
			Type: "webhook", Name: "x", URL: "https://x",
			SecretRef: "env:H", TimeoutMS: 1000,
		},
		AllowPrivateNetworks: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.client == nil {
		t.Error("client should default")
	}
	if c.resolver == nil {
		t.Error("resolver should default")
	}
	if c.nowUnix == nil {
		t.Error("nowUnix should default")
	}
}

// ---------- Name + Type ----------

func TestConnector_NameAndType(t *testing.T) {
	c := mustNewWithServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	if c.Name() != "test" {
		t.Errorf("Name = %q", c.Name())
	}
	if c.Type() != "webhook" {
		t.Errorf("Type = %q", c.Type())
	}
}

// ---------- Upload happy path ----------

func TestUpload_PostsSignedBody(t *testing.T) {
	const secret = "shhh-its-a-secret" //nolint:gosec // G101: fixture HMAC key, not a real credential
	const payload = "ndjson-zst-bytes-go-here"
	var (
		mu    sync.Mutex
		seen  []*http.Request
		seenB [][]byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, r)
		seenB = append(seenB, b)
		mu.Unlock()
		w.WriteHeader(202)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("TEST_HOOK_SECRET", secret)
	c, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{ //nolint:gosec // G101: env: indirection
			Type: "webhook", Name: "ship-it", URL: srv.URL,
			SecretRef: "env:TEST_HOOK_SECRET", TimeoutMS: 5000,
		},
		AllowPrivateNetworks: true,
		NowUnix:              func() int64 { return 1715000000 },
	})
	if err != nil {
		t.Fatal(err)
	}
	src := writeFixture(t, []byte(payload))
	seg := cc.SealedSegment{
		Path:       src,
		DeliveryID: "deliv-abc",
		Connector:  "ship-it",
		TsMinNs:    time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC).UnixNano(),
	}
	if err := c.Upload(context.Background(), seg); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if len(seen) != 1 {
		t.Fatalf("expected exactly 1 request, got %d", len(seen))
	}
	r := seen[0]
	if got := string(seenB[0]); got != payload {
		t.Errorf("body mismatch: got %q, want %q", got, payload)
	}
	if got := r.Method; got != "POST" {
		t.Errorf("method = %q", got)
	}
	if got := r.Header.Get("Content-Type"); got != "application/x-ndjson" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := r.Header.Get("Content-Encoding"); got != "zstd" {
		t.Errorf("Content-Encoding = %q", got)
	}
	if got := r.Header.Get("X-Sluice-Delivery-Id"); got != "deliv-abc" {
		t.Errorf("X-Sluice-Delivery-Id = %q", got)
	}
	if got := r.Header.Get("X-Sluice-Connector"); got != "ship-it" {
		t.Errorf("X-Sluice-Connector = %q", got)
	}
	wantSig := fmt.Sprintf("t=1715000000,v1=%s", computeExpectedSig(secret, 1715000000, []byte(payload)))
	if got := r.Header.Get("X-Sluice-Signature"); got != wantSig {
		t.Errorf("X-Sluice-Signature = %q\nwant %q", got, wantSig)
	}
	if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "sluice-gateway/") {
		t.Errorf("User-Agent = %q", got)
	}
}

func TestUpload_OmitsOptionalHeadersWhenSegmentEmpty(t *testing.T) {
	c := mustNewWithServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Sluice-Delivery-Id") != "" {
			t.Errorf("expected no Delivery-Id, got %q", r.Header.Get("X-Sluice-Delivery-Id"))
		}
		if r.Header.Get("X-Sluice-Connector") != "" {
			t.Errorf("expected no Connector header, got %q", r.Header.Get("X-Sluice-Connector"))
		}
		w.WriteHeader(200)
	}))
	src := writeFixture(t, []byte("x"))
	if err := c.Upload(context.Background(), cc.SealedSegment{Path: src}); err != nil {
		t.Fatalf("Upload: %v", err)
	}
}

// ---------- Response code classification ----------

func TestUpload_StatusClassification(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		retryable bool
		permanent bool
	}{
		{"200", 200, false, false},
		{"204", 204, false, false},
		{"299", 299, false, false},
		{"301", 301, true, false}, // unexpected 3xx → retryable
		{"400", 400, false, true},
		{"401", 401, false, true},
		{"403", 403, false, true},
		{"404", 404, false, true},
		{"429", 429, true, false},
		{"500", 500, true, false},
		{"502", 502, true, false},
		{"503", 503, true, false},
		{"504", 504, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status := tc.status
			c := mustNewWithServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			src := writeFixture(t, []byte("x"))
			err := c.Upload(context.Background(), cc.SealedSegment{Path: src})
			switch {
			case tc.retryable:
				if !cc.IsRetryable(err) {
					t.Errorf("status %d: want Retryable, got %v", status, err)
				}
			case tc.permanent:
				if !cc.IsPermanent(err) {
					t.Errorf("status %d: want Permanent, got %v", status, err)
				}
			default:
				if err != nil {
					t.Errorf("status %d: want nil, got %v", status, err)
				}
			}
		})
	}
}

// ---------- Error paths ----------

func TestUpload_EmptyPathPermanent(t *testing.T) {
	c := mustNewWithServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	err := c.Upload(context.Background(), cc.SealedSegment{})
	if !cc.IsPermanent(err) {
		t.Errorf("want Permanent, got %v", err)
	}
}

func TestUpload_MissingFilePermanent(t *testing.T) {
	c := mustNewWithServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	err := c.Upload(context.Background(), cc.SealedSegment{Path: "/no/such/file"})
	if !cc.IsPermanent(err) {
		t.Errorf("want Permanent, got %v", err)
	}
}

func TestUpload_TransportErrorRetryable(t *testing.T) {
	// Server that closes the connection without writing a response —
	// surfaces as a transport error in http.Client.Do.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("hijacker not available")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}))
	t.Cleanup(srv.Close)
	t.Setenv("S", "k")
	c, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{ //nolint:gosec // G101: env: indirection
			Type: "webhook", Name: "x", URL: srv.URL,
			SecretRef: "env:S", TimeoutMS: 1000,
		},
		AllowPrivateNetworks: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	src := writeFixture(t, []byte("x"))
	err = c.Upload(context.Background(), cc.SealedSegment{Path: src})
	if !cc.IsRetryable(err) {
		t.Errorf("want Retryable, got %v", err)
	}
}

func TestUpload_ContextCancelledRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("S", "k")
	c, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{ //nolint:gosec // G101: env: indirection
			Type: "webhook", Name: "x", URL: srv.URL,
			SecretRef: "env:S", TimeoutMS: 5000,
		},
		AllowPrivateNetworks: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	src := writeFixture(t, []byte("x"))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = c.Upload(ctx, cc.SealedSegment{Path: src})
	if !cc.IsRetryable(err) {
		t.Errorf("want Retryable, got %v", err)
	}
}

// ---------- SSRF guard ----------

func TestSSRF_AllowsPublicAddresses(t *testing.T) {
	hits := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits = true
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("S", "k")
	// The httptest server binds to 127.0.0.1, which the new
	// transport-level Control hook refuses. AllowPrivateNetworks is
	// the supported test bypass — the fake resolver only spoofs the
	// application-level pre-flight guard, not the kernel-side
	// dial control. Production paths set neither.
	c, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{ //nolint:gosec // G101: env: indirection
			Type: "webhook", Name: "x", URL: srv.URL,
			SecretRef: "env:S", TimeoutMS: 5000,
		},
		Resolver:             allowAllResolver,
		AllowPrivateNetworks: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	src := writeFixture(t, []byte("x"))
	if err := c.Upload(context.Background(), cc.SealedSegment{Path: src}); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if !hits {
		t.Error("server should have received request")
	}
}

func TestSSRF_RefusesLoopback(t *testing.T) {
	t.Setenv("S", "k")
	loopbackResolver := func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	c, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{ //nolint:gosec // G101: env: indirection
			Type: "webhook", Name: "x", URL: "https://api.example/sluice",
			SecretRef: "env:S", TimeoutMS: 5000,
		},
		Resolver: loopbackResolver,
	})
	if err != nil {
		t.Fatal(err)
	}
	src := writeFixture(t, []byte("x"))
	err = c.Upload(context.Background(), cc.SealedSegment{Path: src})
	if !cc.IsPermanent(err) {
		t.Errorf("want Permanent for loopback, got %v", err)
	}
}

func TestSSRF_RefusesPrivate(t *testing.T) {
	t.Setenv("S", "k")
	privateResolver := func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.0.0.5")}, nil
	}
	c, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{ //nolint:gosec // G101: env: indirection
			Type: "webhook", Name: "x", URL: "https://api.example/sluice",
			SecretRef: "env:S", TimeoutMS: 5000,
		},
		Resolver: privateResolver,
	})
	if err != nil {
		t.Fatal(err)
	}
	src := writeFixture(t, []byte("x"))
	err = c.Upload(context.Background(), cc.SealedSegment{Path: src})
	if !cc.IsPermanent(err) {
		t.Errorf("want Permanent for private, got %v", err)
	}
}

func TestSSRF_RefusesLinkLocal(t *testing.T) {
	t.Setenv("S", "k")
	llResolver := func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("169.254.169.254")}, nil
	}
	c, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{ //nolint:gosec // G101: env: indirection
			Type: "webhook", Name: "x", URL: "https://api.example/sluice",
			SecretRef: "env:S", TimeoutMS: 5000,
		},
		Resolver: llResolver,
	})
	if err != nil {
		t.Fatal(err)
	}
	src := writeFixture(t, []byte("x"))
	err = c.Upload(context.Background(), cc.SealedSegment{Path: src})
	if !cc.IsPermanent(err) {
		t.Errorf("want Permanent for link-local, got %v", err)
	}
}

func TestSSRF_DNSFailureRetryable(t *testing.T) {
	t.Setenv("S", "k")
	failingResolver := func(_ context.Context, _ string) ([]net.IP, error) {
		return nil, errors.New("dns: refused")
	}
	c, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{ //nolint:gosec // G101: env: indirection
			Type: "webhook", Name: "x", URL: "https://api.example/sluice",
			SecretRef: "env:S", TimeoutMS: 5000,
		},
		Resolver: failingResolver,
	})
	if err != nil {
		t.Fatal(err)
	}
	src := writeFixture(t, []byte("x"))
	err = c.Upload(context.Background(), cc.SealedSegment{Path: src})
	if !cc.IsRetryable(err) {
		t.Errorf("want Retryable for DNS failure, got %v", err)
	}
}

func TestIsDenied_Coverage(t *testing.T) {
	cases := []struct {
		name string
		ip   net.IP
		want bool
	}{
		{"nil", nil, true},
		{"loopback", net.ParseIP("127.0.0.1"), true},
		{"private 10", net.ParseIP("10.0.0.1"), true},
		{"private 172", net.ParseIP("172.16.0.1"), true},
		{"private 192", net.ParseIP("192.168.1.1"), true},
		{"link-local", net.ParseIP("169.254.0.1"), true},
		{"unspecified", net.ParseIP("0.0.0.0"), true},
		{"multicast", net.ParseIP("224.0.0.1"), true},
		{"link-local mcast", net.ParseIP("224.0.0.5"), true},
		{"public v4", net.ParseIP("198.51.100.1"), false},
		{"public v6", net.ParseIP("2001:db8::1"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDenied(tc.ip); got != tc.want {
				t.Errorf("isDenied(%v) = %v, want %v", tc.ip, got, tc.want)
			}
		})
	}
}

// ---------- SecretLoader ----------

func TestDefaultSecretLoader_Env(t *testing.T) {
	t.Setenv("TEST_HOOK_REF", "value-from-env")
	got, err := DefaultSecretLoader("env:TEST_HOOK_REF")
	if err != nil {
		t.Fatal(err)
	}
	if got != "value-from-env" {
		t.Errorf("got %q", got)
	}
}

func TestDefaultSecretLoader_EnvMissing(t *testing.T) {
	_, err := DefaultSecretLoader("env:THIS_VAR_IS_NEVER_SET_HOOK_X")
	if err == nil {
		t.Error("expected error")
	}
}

func TestDefaultSecretLoader_File(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "secret")
	if err := os.WriteFile(p, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := DefaultSecretLoader("file:" + p)
	if err != nil {
		t.Fatal(err)
	}
	if got != "file-secret" {
		t.Errorf("got %q", got)
	}
}

func TestDefaultSecretLoader_FileMissing(t *testing.T) {
	_, err := DefaultSecretLoader("file:/no/such/file/at/all")
	if err == nil {
		t.Error("expected error")
	}
}

func TestDefaultSecretLoader_BadPrefix(t *testing.T) {
	_, err := DefaultSecretLoader("plain")
	if err == nil {
		t.Error("expected error")
	}
}

// ---------- defaultResolver ----------

func TestDefaultResolver_ResolvesLocalhost(t *testing.T) {
	// Sanity: the production resolver returns IPs for "localhost"
	// (loopback). Exercises the wrapper around net.DefaultResolver.
	ips, err := defaultResolver(context.Background(), "localhost")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(ips) == 0 {
		t.Error("expected at least one IP for localhost")
	}
}

func TestNew_EnvAllowPrivateFlipsRuntimeGuard(t *testing.T) {
	// SLUICE_WEBHOOK_ALLOW_PRIVATE=1 must flip allowPrivate even when
	// the caller leaves Options.AllowPrivateNetworks false. The e2e
	// harness relies on this so its loopback httptest.Server passes
	// the runtime SSRF re-resolve.
	t.Setenv("SLUICE_WEBHOOK_ALLOW_PRIVATE", "true")
	t.Setenv("S", "k")
	c, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{ //nolint:gosec // G101: env: indirection
			Type: "webhook", Name: "x", URL: "https://api.example/sluice",
			SecretRef: "env:S", TimeoutMS: 1000,
		},
		Resolver: func(_ context.Context, _ string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !c.allowPrivate {
		t.Error("env override should flip allowPrivate on")
	}
}

func TestDefaultResolver_BadHost(t *testing.T) {
	_, err := defaultResolver(context.Background(), "invalid..host..with..dots")
	if err == nil {
		t.Error("expected resolution failure")
	}
}

// TestSSRFDialControl exercises the post-resolve, pre-connect hook
// directly. The hook is the TOCTOU-safe layer of the SSRF guard:
// it sees the actual IP the kernel is about to connect to (after
// the dialer's own DNS resolve), so a rebinding DNS server that
// fooled the application-level ssrfGuard cannot fool this layer.
func TestSSRFDialControl(t *testing.T) {
	cases := []struct {
		name    string
		address string
		wantErr bool
	}{
		{"public ipv4 ok", "198.51.100.1:443", false},
		{"public ipv6 ok", "[2606:4700:4700::1111]:443", false},
		{"loopback rejected", "127.0.0.1:80", true},
		{"loopback ipv6 rejected", "[::1]:80", true},
		{"rfc1918 10/8 rejected", "10.0.0.1:443", true},
		{"rfc1918 192.168/16 rejected", "192.168.1.1:443", true},
		{"link-local 169.254 rejected", "169.254.169.254:80", true},
		{"unspecified 0.0.0.0 rejected", "0.0.0.0:80", true},
		{"multicast rejected", "224.0.0.1:80", true},
		{"non-IP host rejected", "example.com:443", true},
		{"malformed address rejected", "no-port", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ssrfDialControl("tcp", tc.address, nil)
			if tc.wantErr && err == nil {
				t.Errorf("ssrfDialControl(%q) = nil, want error", tc.address)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ssrfDialControl(%q) = %v, want nil", tc.address, err)
			}
		})
	}
}

// TestNewSSRFTransport_AllowPrivateSkipsHook covers the
// allowPrivate=true branch: the dialer must not carry a Control
// hook (so test/dev httptest.Server bound to loopback works).
func TestNewSSRFTransport_AllowPrivateSkipsHook(t *testing.T) {
	tr, ok := newSSRFTransport(true).(*http.Transport)
	if !ok {
		t.Fatalf("newSSRFTransport returned %T, want *http.Transport", tr)
	}
	// We can't reach the inner dialer via http.Transport's public
	// surface, so the behavioural check is: hitting a loopback
	// httptest.Server through the transport succeeds. If Control was
	// installed, the dial would error before the request fired.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: tr, Timeout: 2 * time.Second}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("allowPrivate transport must reach loopback; got %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d want 200", resp.StatusCode)
	}
}

// TestNewSSRFTransport_DenyPrivateBlocksLoopback proves the
// production-side transport refuses connect to loopback even when
// the URL host parses as the IP directly (no DNS rebinding needed
// to demonstrate — IP-literal URL is enough).
func TestNewSSRFTransport_DenyPrivateBlocksLoopback(t *testing.T) {
	tr := newSSRFTransport(false)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: tr, Timeout: 2 * time.Second}
	resp, err := client.Get(srv.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatalf("denyPrivate transport must refuse loopback dial; got nil error")
	}
	if !strings.Contains(err.Error(), "refused denied address") {
		t.Errorf("error %v should mention refused denied address", err)
	}
}
