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
	"net/url"
	"os"
	"strings"
	"syscall"
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

// HostResolver is the seam tests substitute to bypass / fake DNS.
// Real production uses net.DefaultResolver.LookupIPAddr.
type HostResolver func(ctx context.Context, host string) ([]net.IP, error)

// SecretLoader is the env: / file: indirection. Same shape as the S3
// + Azure connectors.
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
	// Config carries the validated YAML view. Required, Type==webhook.
	Config contractsconfig.Connector

	// SecretLoader resolves the secret_ref. Defaults to
	// DefaultSecretLoader.
	SecretLoader SecretLoader

	// Client is the HTTP transport. Defaults to a fresh *http.Client
	// with timeout = Config.TimeoutMS. Tests inject a fake httptest.
	Client httpDoer

	// Resolver is the DNS seam for the SSRF guard. Defaults to
	// net.DefaultResolver.LookupIPAddr.
	Resolver HostResolver

	// AllowPrivateNetworks disables both layers of the SSRF guard: the
	// pre-request resolver check (ssrfGuard) AND the dial-time Control
	// hook on the default transport that re-checks the actual connect
	// IP. TEST-ONLY — production code must leave false. The contracts
	// validator (rejectLocalOrPrivateHost in connectors_validate.go)
	// already refuses literal localhost / RFC1918 / link-local URLs at
	// config-load; this flag bypasses the runtime layers that defend
	// against DNS that resolves to a private IP at request time.
	AllowPrivateNetworks bool

	// NowUnix supplies the signing timestamp. Defaults to time.Now.
	// Tests pin to make signatures deterministic.
	NowUnix func() int64
}

// Connector implements connector.Connector against an HTTP webhook
// receiver.
type Connector struct {
	name         string
	url          string
	secret       []byte
	client       httpDoer
	resolver     HostResolver
	allowPrivate bool
	timeout      time.Duration
	nowUnix      func() int64
}

var _ connector.Connector = (*Connector)(nil)

// envAllowPrivate is the test-only env var that flips
// Options.AllowPrivateNetworks on for every webhook connector
// constructed via New. The e2e harness sets this on the spawned
// gateway process so its httptest.Server webhook receiver (which
// binds to loopback) doesn't trip the runtime SSRF guard. Production
// never sets it.
const envAllowPrivate = "SLUICE_WEBHOOK_ALLOW_PRIVATE"

// New constructs a Connector. Validates Type==webhook and the per-type
// required fields the contracts validator would have already enforced.
func New(_ context.Context, opts Options) (*Connector, error) {
	if opts.Config.Type != contractsconfig.ConnectorTypeWebhook {
		return nil, fmt.Errorf("webhook: Options.Config.Type = %q, want webhook", opts.Config.Type)
	}
	if opts.Config.Name == "" {
		return nil, errors.New("webhook: Options.Config.Name is required")
	}
	if opts.Config.URL == "" {
		return nil, errors.New("webhook: Options.Config.URL is required")
	}
	if opts.Config.SecretRef == "" {
		return nil, errors.New("webhook: Options.Config.SecretRef is required")
	}
	if opts.Config.TimeoutMS <= 0 {
		return nil, errors.New("webhook: Options.Config.TimeoutMS must be > 0")
	}

	loader := opts.SecretLoader
	if loader == nil {
		loader = DefaultSecretLoader
	}
	secret, err := loader(opts.Config.SecretRef)
	if err != nil {
		return nil, fmt.Errorf("webhook: load secret_ref: %w", err)
	}
	if secret == "" {
		return nil, errors.New("webhook: resolved secret is empty")
	}

	timeout := time.Duration(opts.Config.TimeoutMS) * time.Millisecond

	allowPrivate := opts.AllowPrivateNetworks
	if !allowPrivate {
		if v := strings.ToLower(strings.TrimSpace(os.Getenv(envAllowPrivate))); v == "1" || v == "true" {
			allowPrivate = true
		}
	}

	client := opts.Client
	if client == nil {
		client = &http.Client{
			Timeout:   timeout,
			Transport: newSSRFTransport(allowPrivate),
		}
	}

	resolver := opts.Resolver
	if resolver == nil {
		resolver = defaultResolver
	}

	nowUnix := opts.NowUnix
	if nowUnix == nil {
		nowUnix = func() int64 { return time.Now().Unix() }
	}

	return &Connector{
		name:         opts.Config.Name,
		url:          opts.Config.URL,
		secret:       []byte(secret),
		client:       client,
		resolver:     resolver,
		allowPrivate: allowPrivate,
		timeout:      timeout,
		nowUnix:      nowUnix,
	}, nil
}

// Name implements connector.Connector.
func (c *Connector) Name() string { return c.name }

// Type implements connector.Connector. Always "webhook".
func (c *Connector) Type() string { return contractsconfig.ConnectorTypeWebhook }

// Upload POSTs the segment file to the configured URL with HMAC
// signing + the standard Sluice headers.
func (c *Connector) Upload(ctx context.Context, seg cc.SealedSegment) error {
	if seg.Path == "" {
		return &cc.Permanent{Err: errors.New("webhook: SealedSegment.Path is empty")}
	}

	// SSRF guard: re-resolve URL host and refuse private/loopback/
	// link-local destinations unless the operator explicitly opted in.
	if err := c.ssrfGuard(ctx); err != nil {
		return err
	}

	// Buffer body in memory. Segment sizes are bounded by Spool
	// rotation (default 64 MiB, webhook rotation defaults to 4 MiB
	// per the design note) so this is acceptable.
	body, err := os.ReadFile(seg.Path) //nolint:gosec // path produced by Manager
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &cc.Permanent{Err: fmt.Errorf("webhook: open segment: %w", err)}
		}
		return &cc.Retryable{Err: fmt.Errorf("webhook: open segment: %w", err)}
	}

	ts := c.nowUnix()
	signature := c.sign(ts, body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, strings.NewReader(string(body)))
	if err != nil {
		return &cc.Permanent{Err: fmt.Errorf("webhook: build request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("Content-Encoding", "zstd")
	req.Header.Set("X-Sluice-Signature", fmt.Sprintf("t=%d,v1=%s", ts, signature))
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

// sign produces the hex SHA-256 HMAC of "<ts>.<body>" with the
// configured shared secret.
func (c *Connector) sign(ts int64, body []byte) string {
	mac := hmac.New(sha256.New, c.secret)
	_, _ = fmt.Fprintf(mac, "%d.", ts)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// ssrfGuard re-resolves the URL host and refuses unsafe addresses.
// Skipped when allowPrivate is true (test/dev opt-out).
func (c *Connector) ssrfGuard(ctx context.Context) error {
	if c.allowPrivate {
		return nil
	}
	u, err := url.Parse(c.url)
	if err != nil {
		// Should be unreachable — config-load validates.
		return &cc.Permanent{Err: fmt.Errorf("webhook: parse url: %w", err)}
	}
	host := u.Hostname()
	if host == "" {
		return &cc.Permanent{Err: errors.New("webhook: url has no host")}
	}
	ips, err := c.resolver(ctx, host)
	if err != nil {
		// DNS failures are retryable — could be transient (resolver
		// flap, network blip).
		return &cc.Retryable{Err: fmt.Errorf("webhook: resolve %q: %w", host, err)}
	}
	for _, ip := range ips {
		if isDenied(ip) {
			return &cc.Permanent{Err: fmt.Errorf("webhook: refused denied address %s for %q", ip, host)}
		}
	}
	return nil
}

// classifyHTTPStatus maps response code → Retryable / Permanent / nil.
func classifyHTTPStatus(status int) error {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == 429:
		return &cc.Retryable{Err: fmt.Errorf("webhook: %d Too Many Requests", status)}
	case status >= 500:
		return &cc.Retryable{Err: fmt.Errorf("webhook: %d server error", status)}
	case status >= 400:
		return &cc.Permanent{Err: fmt.Errorf("webhook: %d client error", status)}
	default:
		// 1xx / 3xx are unexpected on a POST to a customer endpoint —
		// classify as Retryable so we don't deadletter on a quirky
		// load balancer.
		return &cc.Retryable{Err: fmt.Errorf("webhook: unexpected status %d", status)}
	}
}

// classifyTransportError maps client.Do errors into the typed pair.
// All transport failures are Retryable — they're transient by nature.
func classifyTransportError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &cc.Retryable{Err: err}
	}
	// Anything else (connection refused, TLS handshake fail, EOF, etc.)
	// is retryable too. The spool's MaxAttempts caps the blast radius.
	return &cc.Retryable{Err: err}
}

// newSSRFTransport builds an http.RoundTripper whose Dialer rejects
// any connect attempt against a denied IP — closing the DNS-rebinding
// TOCTOU window between ssrfGuard (which resolves and inspects IPs in
// the application) and the transport's own resolve+connect. With this
// Control hook, even a DNS server that returns a public IP to the
// guard and a private IP to the dialer cannot reach the latter — the
// kernel-side connect is refused by the hook before the syscall fires.
//
// When allowPrivate is true (test/dev opt-out), the hook is omitted
// and the dialer behaves like a standard net.Dialer.
func newSSRFTransport(allowPrivate bool) http.RoundTripper {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	if !allowPrivate {
		dialer.Control = ssrfDialControl
	}
	// Clone the stdlib default and swap in our dialer so we keep TLS
	// defaults, proxy-from-env, idle-connection management, etc.
	tr := http.DefaultTransport.(*http.Transport).Clone() //nolint:errcheck,forcetypeassert // stdlib invariant
	tr.DialContext = dialer.DialContext
	return tr
}

// ssrfDialControl is the post-resolve, pre-connect hook called by
// net.Dialer with the actual destination address. It parses the IP
// out of the address string and refuses denied ranges.
func ssrfDialControl(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("webhook ssrf-dial: split address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// At this stage the dialer has resolved the hostname and is
		// connecting to an IP — a non-IP host here would indicate a
		// stdlib bug. Refuse rather than guess.
		return fmt.Errorf("webhook ssrf-dial: address %q has non-IP host", address)
	}
	if isDenied(ip) {
		return fmt.Errorf("webhook ssrf-dial: refused denied address %s (network=%s)", ip, network)
	}
	return nil
}

// defaultResolver wraps net.DefaultResolver into the HostResolver
// shape.
func defaultResolver(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, len(addrs))
	for i := range addrs {
		out[i] = addrs[i].IP
	}
	return out, nil
}

// isDenied returns true for IPs the SSRF guard refuses: loopback,
// private RFC1918, link-local, unspecified (0.0.0.0/8), multicast.
func isDenied(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalMulticast() {
		return true
	}
	return false
}
