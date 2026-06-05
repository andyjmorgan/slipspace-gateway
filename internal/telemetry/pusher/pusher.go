// Package pusher is the gateway-side client that ships the full per-request
// cc.Record to the central telemetry service in real time — NOT through the
// connector spool. It is channel 3 of the telemetry design (the authoritative
// request log: headers, bodies, rule chain, resilience attempts), the
// counterpart to the gen_ai/sluice OTLP feeds.
//
// It exists to honour the non-blocking invariant (invariant #2): Enqueue is
// non-blocking and fires from a bounded in-memory worker pool *after* the
// client response, dropping on backpressure and bumping a dropped counter. The
// request path never waits on it, and a wedged or slow telemetry service can
// only cost dropped telemetry, never client latency.
//
// The wire shape is the bare cc.Record JSON POSTed to /api/v1/ingest/record,
// signed with the gateway's HMAC secret (hex HMAC-SHA256 of the raw body) under
// X-Sluice-Signature, identified by X-Sluice-Gateway-Id. Kept dependency-light
// on purpose so the data plane pulls no Postgres/gRPC/OTLP weight.
package pusher

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
	"github.com/andyjmorgan/sluice-gateway/internal/safego"
)

// Webhook headers, mirroring the telemetry service's ingest contract.
const (
	headerGatewayID = "X-Sluice-Gateway-Id"
	headerSignature = "X-Sluice-Signature"
)

// httpDoer is the http.Client surface; tests inject a fake.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Options configures a Pusher.
type Options struct {
	// Endpoint is the telemetry record-ingest URL (…/api/v1/ingest/record).
	Endpoint string
	// GatewayID identifies this gateway to the telemetry registry.
	GatewayID string
	// Secret is the shared HMAC secret the telemetry service verifies against.
	Secret string
	// Workers is the number of concurrent senders. Defaults to 2.
	Workers int
	// Buffer is the bounded queue depth. Defaults to 1024. When full, Enqueue
	// drops and bumps the dropped counter.
	Buffer int
	// Timeout is the per-send HTTP timeout. Zero defaults to 5s.
	Timeout time.Duration
	// Client is the HTTP transport. Defaults to an *http.Client with Timeout.
	Client httpDoer
	// Logger is optional; defaults to slog.Default().
	Logger *slog.Logger
}

// Pusher ships records to the telemetry service from a bounded worker pool.
type Pusher struct {
	endpoint  string
	gatewayID string
	secret    []byte
	client    httpDoer
	timeout   time.Duration
	log       *slog.Logger

	ch      chan cc.Record
	wg      sync.WaitGroup
	dropped atomic.Int64
	sent    atomic.Int64
	failed  atomic.Int64

	closeOnce sync.Once
}

const (
	defaultWorkers = 2
	defaultBuffer  = 1024
	defaultTimeout = 5 * time.Second
)

// New builds and starts a Pusher. Call Close to drain and stop it.
func New(opts Options) *Pusher {
	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	buffer := opts.Buffer
	if buffer <= 0 {
		buffer = defaultBuffer
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	p := &Pusher{
		endpoint:  opts.Endpoint,
		gatewayID: opts.GatewayID,
		secret:    []byte(opts.Secret),
		client:    client,
		timeout:   timeout,
		log:       logger,
		ch:        make(chan cc.Record, buffer),
	}
	for range workers {
		p.wg.Add(1)
		// Workers live for the pusher's lifetime and exit when the channel
		// closes; safego adds panic recovery + attribution (the project's
		// no-raw-goroutine invariant). ctx.Background since the lifecycle is
		// the channel, not a request context.
		safego.Go(context.Background(), "telemetry.pusher.worker", p.log, nil, p.worker)
	}
	return p
}

// Enqueue offers a record to the worker pool without blocking. If the queue is
// full it drops the record and bumps the dropped counter — the request path
// never waits. Returns true if the record was accepted.
func (p *Pusher) Enqueue(rec cc.Record) bool {
	select {
	case p.ch <- rec:
		return true
	default:
		p.dropped.Add(1)
		return false
	}
}

// Dropped returns the number of records dropped due to a full queue.
func (p *Pusher) Dropped() int64 { return p.dropped.Load() }

// Sent returns the number of records successfully delivered.
func (p *Pusher) Sent() int64 { return p.sent.Load() }

// Failed returns the number of records that errored on send (telemetry loss,
// not a request-path failure).
func (p *Pusher) Failed() int64 { return p.failed.Load() }

// Close stops accepting records, drains the in-flight queue, and waits for the
// workers — bounded by ctx. After Close, Enqueue still returns false (drops).
func (p *Pusher) Close(ctx context.Context) {
	p.closeOnce.Do(func() { close(p.ch) })
	done := make(chan struct{})
	safego.Go(ctx, "telemetry.pusher.close_wait", p.log, nil, func() { p.wg.Wait(); close(done) })
	select {
	case <-done:
	case <-ctx.Done():
		p.log.Warn("telemetry pusher: close timed out", "queued", len(p.ch))
	}
}

func (p *Pusher) worker() {
	defer p.wg.Done()
	for rec := range p.ch {
		p.send(rec)
	}
}

// send delivers one record. Errors are logged and counted, never propagated —
// telemetry delivery is best-effort by design.
func (p *Pusher) send(rec cc.Record) {
	raw, err := json.Marshal(rec)
	if err != nil {
		p.failed.Add(1)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(raw))
	if err != nil {
		p.failed.Add(1)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerGatewayID, p.gatewayID)
	req.Header.Set(headerSignature, p.sign(raw))

	resp, err := p.client.Do(req)
	if err != nil {
		p.failed.Add(1)
		p.log.Debug("telemetry pusher: send failed", "correlation_id", rec.CorrelationID, "err", err)
		return
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		p.failed.Add(1)
		p.log.Debug("telemetry pusher: non-2xx", "correlation_id", rec.CorrelationID, "status", resp.StatusCode)
		return
	}
	p.sent.Add(1)
}

// sign returns the hex HMAC-SHA256 of body under the gateway secret — the value
// the telemetry registry verifies.
func (p *Pusher) sign(body []byte) string {
	mac := hmac.New(sha256.New, p.secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
