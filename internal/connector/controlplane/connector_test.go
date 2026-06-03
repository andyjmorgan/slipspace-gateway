package controlplane

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
)

func writeFixture(t *testing.T, body []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "1715000000000000001-42.ndjson.zst")
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustNew(t *testing.T, url string) *Connector {
	t.Helper()
	t.Setenv("TEST_CP_TOKEN", "fleet-token")
	c, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{ //nolint:gosec // G101: SecretRef is env: indirection, not a literal
			Type: contractsconfig.ConnectorTypeControlPlane, Name: "cp-audit",
			URL: url, SecretRef: "env:TEST_CP_TOKEN", TimeoutMS: 5000,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestUpload_PostsWithBearerToken(t *testing.T) {
	var gotAuth, gotEncoding, gotDelivery, gotConnector string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotEncoding = r.Header.Get("Content-Encoding")
		gotDelivery = r.Header.Get("X-Sluice-Delivery-Id")
		gotConnector = r.Header.Get("X-Sluice-Connector")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := mustNew(t, srv.URL)
	if c.Name() != "cp-audit" || c.Type() != contractsconfig.ConnectorTypeControlPlane {
		t.Errorf("Name/Type = %q/%q", c.Name(), c.Type())
	}
	src := writeFixture(t, []byte("zstd-bytes-here"))
	seg := cc.SealedSegment{Path: src, DeliveryID: "deliv-1", Connector: "cp-audit"}

	if err := c.Upload(context.Background(), seg); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if gotAuth != "Bearer fleet-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer fleet-token")
	}
	if gotEncoding != "zstd" {
		t.Errorf("Content-Encoding = %q, want zstd", gotEncoding)
	}
	if gotDelivery != "deliv-1" || gotConnector != "cp-audit" {
		t.Errorf("delivery/connector headers = %q/%q", gotDelivery, gotConnector)
	}
	if string(gotBody) != "zstd-bytes-here" {
		t.Errorf("body = %q, want the segment bytes verbatim", gotBody)
	}
}

func TestUpload_StatusClassification(t *testing.T) {
	cases := []struct {
		status    int
		wantNil   bool
		retryable bool
	}{
		{http.StatusOK, true, false},
		{http.StatusAccepted, true, false},
		{http.StatusTooManyRequests, false, true},
		{http.StatusServiceUnavailable, false, true},
		{http.StatusUnauthorized, false, false},
		{http.StatusForbidden, false, false},
		{http.StatusBadRequest, false, false},
		{http.StatusMovedPermanently, false, true},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
		}))
		c := mustNew(t, srv.URL)
		src := writeFixture(t, []byte("x"))
		err := c.Upload(context.Background(), cc.SealedSegment{Path: src})
		srv.Close()

		switch {
		case tc.wantNil:
			if err != nil {
				t.Errorf("status %d: want nil, got %v", tc.status, err)
			}
		case tc.retryable:
			var rt *cc.Retryable
			if !errors.As(err, &rt) {
				t.Errorf("status %d: want Retryable, got %v", tc.status, err)
			}
		default:
			var p *cc.Permanent
			if !errors.As(err, &p) {
				t.Errorf("status %d: want Permanent, got %v", tc.status, err)
			}
		}
	}
}

func TestUpload_EmptyPathPermanent(t *testing.T) {
	c := mustNew(t, "http://127.0.0.1:1")
	var p *cc.Permanent
	if err := c.Upload(context.Background(), cc.SealedSegment{}); !errors.As(err, &p) {
		t.Fatalf("empty path: want Permanent, got %v", err)
	}
}

func TestUpload_MissingFilePermanent(t *testing.T) {
	c := mustNew(t, "http://127.0.0.1:1")
	var p *cc.Permanent
	err := c.Upload(context.Background(), cc.SealedSegment{Path: filepath.Join(t.TempDir(), "gone.zst")})
	if !errors.As(err, &p) {
		t.Fatalf("missing file: want Permanent, got %v", err)
	}
}

func TestUpload_TransportErrorRetryable(t *testing.T) {
	// A closed server's address yields a connection-refused transport error.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.URL
	srv.Close()

	c := mustNew(t, addr)
	src := writeFixture(t, []byte("x"))
	var rt *cc.Retryable
	if err := c.Upload(context.Background(), cc.SealedSegment{Path: src}); !errors.As(err, &rt) {
		t.Fatalf("transport error: want Retryable, got %v", err)
	}
}

func TestNew_Validation(t *testing.T) {
	base := contractsconfig.Connector{Type: contractsconfig.ConnectorTypeControlPlane, Name: "cp", URL: "http://cp:8484", SecretRef: "env:TEST_CP_TOKEN", TimeoutMS: 5000} //nolint:gosec // G101: SecretRef is env: indirection, not a literal
	t.Setenv("TEST_CP_TOKEN", "tok")

	mutate := func(f func(*contractsconfig.Connector)) contractsconfig.Connector {
		cfg := base
		f(&cfg)
		return cfg
	}
	cases := map[string]contractsconfig.Connector{
		"wrong type":    mutate(func(c *contractsconfig.Connector) { c.Type = "webhook" }),
		"no name":       mutate(func(c *contractsconfig.Connector) { c.Name = "" }),
		"no url":        mutate(func(c *contractsconfig.Connector) { c.URL = "" }),
		"no secret_ref": mutate(func(c *contractsconfig.Connector) { c.SecretRef = "" }),
		"no timeout":    mutate(func(c *contractsconfig.Connector) { c.TimeoutMS = 0 }),
	}
	for name, cfg := range cases {
		if _, err := New(context.Background(), Options{Config: cfg}); err == nil {
			t.Errorf("%s: want error, got nil", name)
		}
	}

	// Empty resolved token is rejected.
	t.Setenv("TEST_CP_EMPTY", "")
	bad := mutate(func(c *contractsconfig.Connector) { c.SecretRef = "env:TEST_CP_EMPTY" })
	if _, err := New(context.Background(), Options{Config: bad}); err == nil {
		t.Error("empty token: want error, got nil")
	}

	if _, err := New(context.Background(), Options{Config: base}); err != nil {
		t.Errorf("valid config: %v", err)
	}
}

func TestDefaultSecretLoader(t *testing.T) {
	t.Setenv("CP_TOK", "v")
	if v, err := DefaultSecretLoader("env:CP_TOK"); err != nil || v != "v" {
		t.Errorf("env: = %q, %v", v, err)
	}
	if _, err := DefaultSecretLoader("env:CP_MISSING"); err == nil {
		t.Error("missing env: want error")
	}
	p := writeFixture(t, []byte("file-token\n"))
	if v, err := DefaultSecretLoader("file:" + p); err != nil || v != "file-token" {
		t.Errorf("file: = %q, %v", v, err)
	}
	if _, err := DefaultSecretLoader("file:/no/such/path"); err == nil {
		t.Error("missing file: want error")
	}
	if _, err := DefaultSecretLoader("plain"); err == nil {
		t.Error("bare ref: want error")
	}
}
