package controlplane

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
	"github.com/andyjmorgan/sluice-gateway/internal/connector"
	"github.com/andyjmorgan/sluice-gateway/internal/version"
)

// httpDoer is the http.Client surface tests inject. Real *http.Client
// satisfies it.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// SecretLoader is the env: / file: indirection, identical to the webhook
// connector's loader. The control-plane connector resolves the bootstrap token
// the same way every other secret is resolved, so the published config carries
// a ref (conventionally env:SLUICE_CP_TOKEN) and never the token itself.
type SecretLoader func(ref string) (string, error)

// DefaultSecretLoader resolves env: / file: refs at runtime.
func DefaultSecretLoader(ref string) (string, error) {
	switch {
	case strings.HasPrefix(ref, "env:"):
		name := strings.TrimPrefix(ref, "env:")
		val, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("env var %q not set", name)
		}
		return val, nil
	case strings.HasPrefix(ref, "file:"):
		p := strings.TrimPrefix(ref, "file:")
		b, err := os.ReadFile(p) //nolint:gosec // operator-supplied path
		if err != nil {
			return "", fmt.Errorf("read %q: %w", p, err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	default:
		return "", fmt.Errorf("secret_ref must start with env: or file:, got %q", ref)
	}
}

// Options configures a Connector.
type Options struct {
	// Config carries the validated YAML view. Required, Type==controlplane.
	Config contractsconfig.Connector

	// SecretLoader resolves the secret_ref (the bootstrap token). Defaults to
	// DefaultSecretLoader.
	SecretLoader SecretLoader

	// Client is the HTTP transport. Defaults to a fresh *http.Client with
	// timeout = Config.TimeoutMS. Tests inject a fake.
	Client httpDoer
}

// Connector ships sealed segments to the control plane's segment-ingest
// endpoint.
type Connector struct {
	name    string
	url     string
	token   string
	client  httpDoer
	timeout time.Duration
}

var _ connector.Connector = (*Connector)(nil)

// New constructs a Connector. Validates Type==controlplane and the per-type
// required fields the contracts validator would have already enforced.
func New(_ context.Context, opts Options) (*Connector, error) {
	if opts.Config.Type != contractsconfig.ConnectorTypeControlPlane {
		return nil, fmt.Errorf("controlplane: Options.Config.Type = %q, want controlplane", opts.Config.Type)
	}
	if opts.Config.Name == "" {
		return nil, errors.New("controlplane: Options.Config.Name is required")
	}
	if opts.Config.URL == "" {
		return nil, errors.New("controlplane: Options.Config.URL is required")
	}
	if opts.Config.SecretRef == "" {
		return nil, errors.New("controlplane: Options.Config.SecretRef is required")
	}
	if opts.Config.TimeoutMS <= 0 {
		return nil, errors.New("controlplane: Options.Config.TimeoutMS must be > 0")
	}

	loader := opts.SecretLoader
	if loader == nil {
		loader = DefaultSecretLoader
	}
	token, err := loader(opts.Config.SecretRef)
	if err != nil {
		return nil, fmt.Errorf("controlplane: load secret_ref: %w", err)
	}
	if token == "" {
		return nil, errors.New("controlplane: resolved token is empty")
	}

	timeout := time.Duration(opts.Config.TimeoutMS) * time.Millisecond

	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	return &Connector{
		name:    opts.Config.Name,
		url:     opts.Config.URL,
		token:   token,
		client:  client,
		timeout: timeout,
	}, nil
}

// Name implements connector.Connector.
func (c *Connector) Name() string { return c.name }

// Type implements connector.Connector. Always "controlplane".
func (c *Connector) Type() string { return contractsconfig.ConnectorTypeControlPlane }

// Upload POSTs the segment file to the control plane's ingest endpoint with the
// Bearer token. No SSRF guard and no HMAC — see the package doc.
func (c *Connector) Upload(ctx context.Context, seg cc.SealedSegment) error {
	if seg.Path == "" {
		return &cc.Permanent{Err: errors.New("controlplane: SealedSegment.Path is empty")}
	}

	body, err := os.ReadFile(seg.Path) //nolint:gosec // path produced by Manager
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &cc.Permanent{Err: fmt.Errorf("controlplane: open segment: %w", err)}
		}
		return &cc.Retryable{Err: fmt.Errorf("controlplane: open segment: %w", err)}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, strings.NewReader(string(body)))
	if err != nil {
		return &cc.Permanent{Err: fmt.Errorf("controlplane: build request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("Content-Encoding", "zstd")
	req.Header.Set("Authorization", "Bearer "+c.token)
	if seg.DeliveryID != "" {
		req.Header.Set("X-Sluice-Delivery-Id", seg.DeliveryID)
	}
	if seg.Connector != "" {
		req.Header.Set("X-Sluice-Connector", seg.Connector)
	}
	req.Header.Set("User-Agent", "sluice-gateway/"+version.Version)

	resp, herr := c.client.Do(req)
	if herr != nil {
		return classifyTransportError(herr)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	return classifyHTTPStatus(resp.StatusCode)
}

// classifyHTTPStatus maps response code → Retryable / Permanent / nil, matching
// the spool's connector contract. A 401/403 from the CP is Permanent (the token
// is wrong — retrying won't fix it); 429 / 5xx are Retryable.
func classifyHTTPStatus(status int) error {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == 429:
		return &cc.Retryable{Err: fmt.Errorf("controlplane: %d Too Many Requests", status)}
	case status >= 500:
		return &cc.Retryable{Err: fmt.Errorf("controlplane: %d server error", status)}
	case status >= 400:
		return &cc.Permanent{Err: fmt.Errorf("controlplane: %d client error", status)}
	default:
		return &cc.Retryable{Err: fmt.Errorf("controlplane: unexpected status %d", status)}
	}
}

// classifyTransportError maps a client.Do error into the typed pair. Only ever
// called with a non-nil error; all transport failures are Retryable — they're
// transient by nature, and the spool's MaxAttempts caps the blast radius.
func classifyTransportError(err error) error {
	return &cc.Retryable{Err: err}
}
