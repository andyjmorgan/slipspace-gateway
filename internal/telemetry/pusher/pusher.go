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
// Delivery is retried: transient failures (network errors, 408/429/5xx) get
// capped exponential backoff up to MaxAttempts before the record is declared
// lost — a single 502 blip must not permanently lose an audit record, because
// ingest is an idempotent upsert keyed by correlation_id (a re-push of a
// record that actually landed overwrites in place). Retries run inside the
// worker that owns the record, so the bounded channel stays the only queue:
// a wedged endpoint parks the workers, the channel fills, and new records
// drop with a counter bump — memory stays capped and the request path never
// blocks. Permanent failures (other 4xx: bad HMAC, bad request) are not
// retried; they can never succeed.
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

// Drop reasons reported through Options.OnDropped. Every value is a record
// permanently lost after Enqueue accepted it — except DropQueueFull, which is
// the Enqueue-side drop. Fixed vocabulary so the metric label stays bounded.
const (
	// DropQueueFull: Enqueue found the bounded channel full.
	DropQueueFull = "queue_full"
	// DropEncode: the record could not be marshalled or the request could
	// not be built. Never retried — it can never succeed.
	DropEncode = "encode"
	// DropRejected: the ingest endpoint returned a non-retryable status
	// (4xx other than 408/429), e.g. a bad HMAC. Never retried.
	DropRejected = "rejected"
	// DropExhausted: every retry attempt failed; the record is lost.
	DropExhausted = "exhausted"
)

// Failure kinds reported through Options.OnFailure, one per failed send
// attempt (a single record can report several before succeeding or dropping).
const (
	// FailureNetwork: the HTTP round trip itself errored (dial, timeout, …).
	FailureNetwork = "network"
	// FailureStatus: the round trip completed with a non-2xx status.
	FailureStatus = "status"
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
	// MaxAttempts caps the delivery attempts per record (first try included).
	// Defaults to 5. Transient failures (network error, 408/429/5xx) retry up
	// to this cap with backoff; other failures never retry.
	MaxAttempts int
	// BackoffBase is the wait before the first retry; each subsequent retry
	// doubles it. Defaults to 250ms.
	BackoffBase time.Duration
	// BackoffMax caps the per-retry wait. Defaults to 5s.
	BackoffMax time.Duration
	// OnDropped, when non-nil, is invoked once per permanently lost record
	// with one of the Drop* reasons — the hook the gateway wires to its
	// records-dropped meter. Must be cheap and non-blocking.
	OnDropped func(reason string)
	// OnFailure, when non-nil, is invoked once per failed send attempt with
	// one of the Failure* kinds — the hook the gateway wires to its
	// push-failures meter. Must be cheap and non-blocking.
	OnFailure func(kind string)
}

// Pusher ships records to the telemetry service from a bounded worker pool.
type Pusher struct {
	endpoint  string
	gatewayID string
	secret    []byte
	client    httpDoer
	timeout   time.Duration
	log       *slog.Logger

	maxAttempts int
	backoffBase time.Duration
	backoffMax  time.Duration
	onDropped   func(reason string)
	onFailure   func(kind string)

	ch      chan cc.Record
	wg      sync.WaitGroup
	dropped atomic.Int64
	sent    atomic.Int64
	failed  atomic.Int64
	lost    atomic.Int64

	// draining is closed by Close before the channel closes. Workers select
	// on it during backoff waits so a shutdown drain retries back-to-back
	// instead of sleeping out the schedule — keeping Close fast and bounded.
	draining  chan struct{}
	closeOnce sync.Once
}

const (
	defaultWorkers     = 2
	defaultBuffer      = 1024
	defaultTimeout     = 5 * time.Second
	defaultMaxAttempts = 5
	defaultBackoffBase = 250 * time.Millisecond
	defaultBackoffMax  = 5 * time.Second
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
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}
	backoffBase := opts.BackoffBase
	if backoffBase <= 0 {
		backoffBase = defaultBackoffBase
	}
	backoffMax := opts.BackoffMax
	if backoffMax <= 0 {
		backoffMax = defaultBackoffMax
	}

	p := &Pusher{
		endpoint:    opts.Endpoint,
		gatewayID:   opts.GatewayID,
		secret:      []byte(opts.Secret),
		client:      client,
		timeout:     timeout,
		log:         logger,
		maxAttempts: maxAttempts,
		backoffBase: backoffBase,
		backoffMax:  backoffMax,
		onDropped:   opts.OnDropped,
		onFailure:   opts.OnFailure,
		ch:          make(chan cc.Record, buffer),
		draining:    make(chan struct{}),
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
		p.dropHook(DropQueueFull)
		return false
	}
}

// Dropped returns the number of records dropped due to a full queue.
func (p *Pusher) Dropped() int64 { return p.dropped.Load() }

// Sent returns the number of records successfully delivered.
func (p *Pusher) Sent() int64 { return p.sent.Load() }

// Failed returns the number of send attempts that errored (telemetry loss
// risk, not a request-path failure). One record can fail several attempts
// before succeeding or being declared lost.
func (p *Pusher) Failed() int64 { return p.failed.Load() }

// Lost returns the number of records permanently lost after Enqueue accepted
// them: encode failures, non-retryable rejections, and exhausted retries.
// Queue-full drops are counted by Dropped instead.
func (p *Pusher) Lost() int64 { return p.lost.Load() }

// Close stops accepting records, drains the in-flight queue, and waits for the
// workers — bounded by ctx. After Close, Enqueue still returns false (drops).
// In-flight retries keep their remaining attempts but skip the backoff waits,
// so the drain cost is bounded by attempts × HTTP timeout, not the schedule.
func (p *Pusher) Close(ctx context.Context) {
	p.closeOnce.Do(func() {
		close(p.draining)
		close(p.ch)
	})
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
		p.deliver(rec)
	}
}

// sendOutcome classifies one delivery attempt for the retry loop.
type sendOutcome int

const (
	// sendOK: delivered, 2xx.
	sendOK sendOutcome = iota
	// sendRetry: transient failure (network error, 408/429/5xx) — retryable.
	sendRetry
	// sendRejected: permanent non-2xx (other 4xx, e.g. bad HMAC) — never retried.
	sendRejected
	// sendEncode: the record can't be marshalled or the request can't be
	// built — never retried, no wire attempt was made.
	sendEncode
)

// deliver pushes one record with capped exponential backoff on transient
// failures. Records that exhaust the schedule or hit a permanent failure are
// declared lost (counter + hook); errors never propagate — telemetry delivery
// is best-effort by design, just no longer single-shot. Re-pushing a record
// the service already stored is safe: ingest upserts on correlation_id.
func (p *Pusher) deliver(rec cc.Record) {
	for attempt := 1; ; attempt++ {
		switch p.send(rec) {
		case sendOK:
			return
		case sendEncode:
			p.lose(rec, DropEncode, attempt)
			return
		case sendRejected:
			p.lose(rec, DropRejected, attempt)
			return
		case sendRetry:
			if attempt >= p.maxAttempts {
				p.lose(rec, DropExhausted, attempt)
				return
			}
			// Back off before the next attempt — unless we are draining,
			// in which case the remaining attempts run back-to-back so
			// Close stays bounded by HTTP timeouts, not the schedule.
			t := time.NewTimer(p.backoffFor(attempt))
			select {
			case <-t.C:
			case <-p.draining:
				t.Stop()
			}
		}
	}
}

// backoffFor returns the wait before the retry that follows attempt n
// (1-based): base doubled per attempt, capped at backoffMax.
func (p *Pusher) backoffFor(attempt int) time.Duration {
	d := p.backoffBase
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= p.backoffMax {
			return p.backoffMax
		}
	}
	return min(d, p.backoffMax)
}

// lose records a permanent post-Enqueue loss: counter, hook, and one Warn —
// this is exactly the audit-record loss the dropped-records meter exists to
// surface, so it must never be debug-only.
func (p *Pusher) lose(rec cc.Record, reason string, attempts int) {
	p.lost.Add(1)
	p.dropHook(reason)
	p.log.Warn("telemetry pusher: record lost",
		"correlation_id", rec.CorrelationID, "reason", reason, "attempts", attempts)
}

// dropHook invokes the OnDropped callback when wired.
func (p *Pusher) dropHook(reason string) {
	if p.onDropped != nil {
		p.onDropped(reason)
	}
}

// failHook invokes the OnFailure callback when wired.
func (p *Pusher) failHook(kind string) {
	if p.onFailure != nil {
		p.onFailure(kind)
	}
}

// send performs one delivery attempt and classifies the result. Every non-OK
// outcome bumps the failed counter; network/status failures also report
// through the OnFailure hook.
func (p *Pusher) send(rec cc.Record) sendOutcome {
	raw, err := json.Marshal(rec)
	if err != nil {
		p.failed.Add(1)
		return sendEncode
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(raw))
	if err != nil {
		p.failed.Add(1)
		return sendEncode
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerGatewayID, p.gatewayID)
	req.Header.Set(headerSignature, p.sign(raw))

	resp, err := p.client.Do(req)
	if err != nil {
		p.failed.Add(1)
		p.failHook(FailureNetwork)
		p.log.Debug("telemetry pusher: send failed", "correlation_id", rec.CorrelationID, "err", err)
		return sendRetry
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		p.failed.Add(1)
		p.failHook(FailureStatus)
		p.log.Debug("telemetry pusher: non-2xx", "correlation_id", rec.CorrelationID, "status", resp.StatusCode)
		if retryableStatus(resp.StatusCode) {
			return sendRetry
		}
		return sendRejected
	}
	p.sent.Add(1)
	return sendOK
}

// retryableStatus reports whether a non-2xx status is worth retrying:
// timeouts (408), throttles (429), and server-side errors (5xx). Other 4xx
// are deterministic rejections (bad HMAC, bad request) that can never succeed.
func retryableStatus(code int) bool {
	return code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500
}

// sign returns the hex HMAC-SHA256 of body under the gateway secret — the value
// the telemetry registry verifies.
func (p *Pusher) sign(body []byte) string {
	mac := hmac.New(sha256.New, p.secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
