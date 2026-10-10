package arbiter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	detectv1 "github.com/andyjmorgan/slipspace-gateway/gen/slipspace/detect/v1"
)

// maxDetectorResponseBytes bounds a detector's response body to keep a
// misbehaving detector from exhausting memory.
const maxDetectorResponseBytes = 1 << 20

// httpDoer is the slice of *http.Client the detector client needs; an interface
// so tests inject a stub without a live server.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// detectorClient calls a detector's /detect endpoint over protojson/HTTP — the
// async transport this release (REF-008). The same DetectRequest/DetectResponse
// types serve the future inline gRPC path unchanged.
type detectorClient struct {
	http httpDoer
}

func newDetectorClient(doer httpDoer) *detectorClient {
	if doer == nil {
		doer = &http.Client{Transport: detectorTransport()}
	}
	return &detectorClient{http: doer}
}

// detectorTransport is the transport for the default detector client. Detectors
// serve /detect from uvicorn with its 5 s default keep-alive idle timeout — far
// below the stdlib transport's 90 s IdleConnTimeout. With connection pooling on,
// a scan landing after an idle gap reuses a connection uvicorn has already
// closed, and the POST fails with EOF (surfaced as UNREACHABLE); Go will not
// auto-retry a POST whose body may already be on the wire. Scans are sporadic
// and intra-cluster, so a fresh connection per call costs nothing — disabling
// keep-alives sidesteps the stale-reuse race entirely, independent of any
// detector's keep-alive tuning.
func detectorTransport() *http.Transport {
	return &http.Transport{
		DisableKeepAlives:   true,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

// Detect posts one DetectRequest to endpoint and decodes the DetectResponse.
// The caller bounds latency via ctx (the 90 s per-call budget). Unknown
// response fields are discarded so an additively-evolved detector does not
// break an older Arbiter (ADR-007).
func (c *detectorClient) Detect(ctx context.Context, endpoint string, req *detectv1.DetectRequest) (*detectv1.DetectResponse, error) {
	body, err := protojson.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("arbiter: marshal detect request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("arbiter: build detect request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("arbiter: call detector %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxDetectorResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("arbiter: read detector response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("arbiter: detector %s returned status %d", endpoint, resp.StatusCode)
	}

	var out detectv1.DetectResponse
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("arbiter: unmarshal detect response: %w", err)
	}
	return &out, nil
}
