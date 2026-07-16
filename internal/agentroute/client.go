package agentroute

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/contracts/advise"
)

// Webhook headers the client signs advisory requests with — the same
// HMAC-trust convention the Record webhook uses, so the Arbiter verifies both
// against one gateway registry.
const (
	headerGatewayID = "X-Slipspace-Gateway-Id"
	headerSignature = "X-Slipspace-Signature"
)

// maxVerdictBytes caps the advisory response body; a verdict is a few hundred
// bytes, so anything larger is malformed.
const maxVerdictBytes = 64 << 10

// Client posts advisory requests to one advisor endpoint. All calls are made
// off the request path by Service; the timeout exists to reclaim the
// goroutine, not to protect request latency.
type Client struct {
	// endpoint is the advisor's full URL.
	endpoint string

	// gatewayID names this gateway in the Arbiter's registry.
	gatewayID string

	// secret is the shared HMAC key (read from the advisor's
	// hmac_secret_file at startup).
	secret []byte

	// httpc is the underlying HTTP client, timeout applied.
	httpc *http.Client
}

// NewClient builds an advisory client.
func NewClient(endpoint, gatewayID string, secret []byte, timeout time.Duration) *Client {
	return &Client{
		endpoint:  endpoint,
		gatewayID: gatewayID,
		secret:    secret,
		httpc:     &http.Client{Timeout: timeout},
	}
}

// Advise posts one classification request and returns the verdict. Any error
// is the caller's signal to fail open (treat as "continue").
func (c *Client) Advise(ctx context.Context, req advise.Request) (advise.Verdict, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return advise.Verdict{}, fmt.Errorf("agentroute: marshal advise request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return advise.Verdict{}, fmt.Errorf("agentroute: build advise request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(headerGatewayID, c.gatewayID)
	httpReq.Header.Set(headerSignature, c.sign(body))

	resp, err := c.httpc.Do(httpReq)
	if err != nil {
		return advise.Verdict{}, fmt.Errorf("agentroute: advise call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return advise.Verdict{}, fmt.Errorf("agentroute: advise status %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxVerdictBytes))
	if err != nil {
		return advise.Verdict{}, fmt.Errorf("agentroute: read verdict: %w", err)
	}
	var v advise.Verdict
	if err := json.Unmarshal(raw, &v); err != nil {
		return advise.Verdict{}, fmt.Errorf("agentroute: decode verdict: %w", err)
	}
	return v, nil
}

// sign computes the hex HMAC-SHA256 of body under the shared secret.
func (c *Client) sign(body []byte) string {
	mac := hmac.New(sha256.New, c.secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
