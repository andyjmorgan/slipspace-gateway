# Telemetry Record webhook

The Record webhook is **channel 3** of the telemetry design: the full, authoritative
per-request digital record — request/response bodies, headers, the post-rule tag set,
the fired-rule chain, and the resilience attempt log — shipped from a gateway to the
central telemetry service in real time. It is the counterpart to the two OTLP feeds
(`gen_ai.*` spans and `sluice.*` meters); see [telemetry-service.md](telemetry-service.md)
for the service that terminates all three.

Unlike the durable `s3` / `azure_blob` connectors, the webhook is **not** disk-backed.
It is a best-effort, non-blocking, per-record HMAC POST. A wedged or slow receiver can
only ever cost *dropped telemetry* — never client latency, never a blocked request path
(invariant #2). For the durable archival path, see [connectors.md](connectors.md) and
[spool.md](spool.md).

## Table of contents

- [Two halves: pusher and ingest](#two-halves-pusher-and-ingest)
- [Ingest contract](#ingest-contract)
  - [Endpoint](#endpoint)
  - [Headers](#headers)
  - [Signature scheme](#signature-scheme)
  - [Body](#body)
  - [Response codes](#response-codes)
  - [Idempotency](#idempotency)
  - [Example](#example)
- [Gateway-side pusher](#gateway-side-pusher)
  - [Non-blocking semantics](#non-blocking-semantics)
  - [Counters](#counters)
- [Webhook connector config](#webhook-connector-config)
- [Cross-references](#cross-references)

## Two halves: pusher and ingest

The webhook has a sender and a receiver, both maintained in this repo:

- **Gateway side — the pusher** ([internal/telemetry/pusher/pusher.go](../internal/telemetry/pusher/pusher.go)).
  A bounded in-memory worker pool that marshals each `cc.Record` to JSON, signs it,
  and POSTs it. Dependency-light on purpose so the data plane pulls no Postgres / gRPC /
  OTLP weight.
- **Service side — the ingest handler** ([internal/telemetry/ingest/record.go](../internal/telemetry/ingest/record.go),
  `RecordHandler`). Verifies the HMAC against the registered gateway secret
  ([internal/telemetry/registry/registry.go](../internal/telemetry/registry/registry.go)),
  decodes the record, and upserts it into Postgres.

The wire contract between them is documented below. A third-party receiver can also
terminate the webhook — the only hard requirement is verifying the signature.

## Ingest contract

### Endpoint

```
POST /api/v1/ingest/record
```

Mounted on the telemetry service's HTTP listener (`http_bind`, default `0.0.0.0:8686`)
in [internal/telemetry/server/server.go](../internal/telemetry/server/server.go) at
`mux.Handle("POST /api/v1/ingest/record", …)`. This is the same listener that serves the
operator console; the OTLP feeds bind separately on `otlp_bind` (default `0.0.0.0:8687`).

One record per request. The body is the bare `cc.Record` JSON
([contracts/connector/record.go](../contracts/connector/record.go)) — not an array, not
an ndjson batch, not a sealed `.ndjson.zst` segment.

### Headers

| Header | Required | Meaning |
|---|---|---|
| `X-Sluice-Gateway-Id` | yes | The registered gateway the push claims to be from. The service looks the HMAC secret up by this id. |
| `X-Sluice-Signature` | yes | Hex-encoded HMAC-SHA256 of the **raw request body** (see below). |
| `Content-Type` | no | The pusher sends `application/json`; the handler does not enforce it. |

Constants live in [internal/telemetry/ingest/record.go](../internal/telemetry/ingest/record.go)
(`HeaderGatewayID`, `HeaderSignature`) and mirror the pusher's
([internal/telemetry/pusher/pusher.go](../internal/telemetry/pusher/pusher.go)).

### Signature scheme

`X-Sluice-Signature` is the **plain hex HMAC-SHA256 of the raw POST body** under the
gateway's shared secret. There is **no timestamp**, no `t=…,v1=…` envelope, no replay
window, and no signed-header canonicalization — just:

```
X-Sluice-Signature = hex( HMAC_SHA256(secret, raw_body) )
```

Sender ([pusher.go](../internal/telemetry/pusher/pusher.go), `Pusher.sign`):

```go
mac := hmac.New(sha256.New, p.secret)
mac.Write(body)
return hex.EncodeToString(mac.Sum(nil))
```

Receiver ([registry.go](../internal/telemetry/registry/registry.go), `Registry.Verify`):
it computes the HMAC over the same raw bytes, hex-decodes the supplied signature, and
compares with `hmac.Equal` (constant-time). The id-lookup and signature-mismatch failures
return distinct sentinels (`ErrUnknownGateway`, `ErrBadSignature`) internally, but the
handler collapses both to `401` so a caller can never learn *which* check failed.

### Body

The body is a single `cc.Record`. The handler requires only one field to be present:
`correlation_id`. The record is fanned into Postgres as:

- one `request_events` row (the gateway columns: configuration, provider, model,
  protocol/endpoint, method, status, latency, session, fired-rule chain, resilience
  attempts, post-rule tags), via `Store.UpsertGatewayRecord`;
- one `request_payloads` row per present captured item — request body, response body,
  assembled SSE rollup, request headers, response headers — via `Store.UpsertPayload`.
  Items the record marks omitted (oversize-stripped, or absent) contribute no row.

See [telemetry-database-schema.md](telemetry-database-schema.md) for the row shapes and
[telemetry-service.md](telemetry-service.md) for how this merges with the OTLP feed on
`correlation_id`.

### Response codes

| Status | Body | When |
|---|---|---|
| `200 OK` | `{"stored": N}` | Stored. `N` counts the event row plus each payload row written (1 + number of captured items). |
| `400 Bad Request` | `{"error":"malformed record"}` | Body is not valid `cc.Record` JSON. |
| `400 Bad Request` | `{"error":"missing correlation_id"}` | Record decoded but `correlation_id` is empty. |
| `401 Unauthorized` | `{"error":"missing gateway id or signature"}` | Either header is absent. |
| `401 Unauthorized` | `{"error":"signature rejected"}` | Unknown gateway id **or** signature mismatch (deliberately indistinguishable). |
| `413 Request Entity Too Large` | `{"error":"record too large"}` | Body exceeds the 16 MiB cap. |
| `500 Internal Server Error` | `{"error":"verify"}` / `{"error":"store event"}` / `{"error":"store payload"}` | HMAC verify raised a non-sentinel error, or a Postgres upsert failed. |

The body is bounded by `http.MaxBytesReader` at `maxRecordBytes = 16 << 20` (16 MiB).
The gateway caps captured request bodies at 10 MiB inbound; the extra headroom covers the
JSON envelope (base64 of bytes, headers, rule chain) around them. A runaway record is
rejected, not buffered without limit.

The success count is computed in `RecordHandler.ServeHTTP`: `stored` starts at `1` for the
event row and increments once per payload row written. A payload upsert that fails aborts
the request with `500` after the event row has already been written — the next push of the
same record converges it (see idempotency).

### Idempotency

Both upserts are idempotent, so a re-push (pusher retry at a higher layer, or an operator
replay) converges rather than duplicating:

- **Event row** — `ON CONFLICT (correlation_id) DO UPDATE`
  ([internal/telemetry/store/events.go](../internal/telemetry/store/events.go)). The
  Record feed and the OTLP feed share this row keyed by `correlation_id`; each feed owns
  its own columns on conflict, so neither clobbers the other's.
- **Payload rows** — `ON CONFLICT (correlation_id, kind, instance_id, seq) DO UPDATE`
  ([internal/telemetry/store/payloads.go](../internal/telemetry/store/payloads.go)). The
  composite key is the stable cross-instance ordering key (invariant #8) — never receive
  order.

### Example

Sign and POST a record by hand (the gateway secret is `s3cr3t`, the gateway id is
`edge-1`):

```bash
BODY='{"correlation_id":"req-abc123","configuration":"default","provider":"anthropic","model":"claude-sonnet-4-5","protocol":"messages","request":{"method":"POST"},"response":{"status":200}}'

SIG=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac 's3cr3t' -hex | sed 's/^.*= //')

curl -sS -X POST http://localhost:8686/api/v1/ingest/record \
  -H 'Content-Type: application/json' \
  -H 'X-Sluice-Gateway-Id: edge-1' \
  -H "X-Sluice-Signature: $SIG" \
  --data-raw "$BODY"
# => {"stored":1}
```

The signature must be computed over the exact bytes sent. Any reserialization that
changes whitespace or key order between signing and sending breaks verification.

## Gateway-side pusher

`pusher.New` ([internal/telemetry/pusher/pusher.go](../internal/telemetry/pusher/pusher.go))
builds and starts a worker pool. One pusher is constructed per `webhook` connector at
gateway boot ([cmd/gateway/main.go](../cmd/gateway/main.go), `setupPushers`); the HMAC
secret is resolved from the connector's `secret_ref` at startup so a missing secret fails
boot loudly rather than silently dropping every record.

`Options` fields:

| Option | Source | Default |
|---|---|---|
| `Endpoint` | connector `url` | — (required) |
| `GatewayID` | connector `gateway_id` | — (sent as `X-Sluice-Gateway-Id`) |
| `Secret` | resolved connector `secret_ref` | — (required) |
| `Workers` | not config-exposed | `2` |
| `Buffer` | not config-exposed | `1024` |
| `Timeout` | connector `timeout_ms` | `5s` |

`Workers` and `Buffer` are not surfaced in the connector YAML today; the gateway always
takes their defaults. The per-send timeout comes from `timeout_ms`.

### Non-blocking semantics

`Pusher.Enqueue` offers a record to the bounded channel with a non-blocking `select`:

```go
select {
case p.ch <- rec:
    return true
default:
    p.dropped.Add(1)
    return false
}
```

If the queue is full it **drops the record on the floor** and bumps a counter — the
request path never waits (invariant #2). Workers fire *after* the client response, marshal
each record, sign it, and POST it with a per-send context timeout. A non-2xx response or a
transport error is logged at debug and counted as failed; there is **no retry, no
deadletter, no circuit breaker, no backoff** — telemetry delivery is best-effort by design.
`Close(ctx)` stops accepting new records, drains the in-flight queue, and waits for the
workers bounded by `ctx`; after close, `Enqueue` continues to return `false` (drops).

### Counters

The pusher exposes three monotonic counters for operators to scrape via the gateway's own
meters:

| Method | Meaning |
|---|---|
| `Dropped()` | Records dropped because the in-memory queue was full. A rising value means the receiver is too slow or down. |
| `Sent()` | Records the receiver accepted with a 2xx. |
| `Failed()` | Records that errored on send — JSON marshal failure, transport error, or a non-2xx response. Telemetry loss, **not** a request-path failure. |

## Webhook connector config

A `webhook` connector ([contracts/config/connectors.go](../contracts/config/connectors.go),
`Connector`) carries four webhook-specific fields:

```yaml
# policy.yaml
connectors:
  - name: central-telemetry
    type: webhook
    url: https://telemetry.example.com/api/v1/ingest/record
    secret_ref: env:SLUICE_TELEMETRY_HMAC_SECRET
    gateway_id: edge-1
    timeout_ms: 5000
```

| Field | Required | Notes |
|---|---|---|
| `url` | yes | Receiver endpoint each record is POSTed to. For the telemetry service this is `…/api/v1/ingest/record`. |
| `secret_ref` | yes | `env:NAME` or `file:/path` indirection to the HMAC signing key (no inline secrets — invariant on YAML trust). Resolved at boot by `resolveSecretRef` in [cmd/gateway/main.go](../cmd/gateway/main.go). |
| `gateway_id` | conditional | Sent as `X-Sluice-Gateway-Id`. **Required** when pushing to the telemetry service (it keys secrets by gateway). Optional for a generic receiver that verifies the signature alone. |
| `timeout_ms` | yes | Per-call HTTP timeout, `0 < timeout_ms <= 60000`. Becomes the pusher's `Timeout`. |

The `auth`, `rotation`, `bucket`, `region`, `account`, `container`, … fields on `Connector`
are for the spool-backed `s3` / `azure_blob` types and are ignored for `webhook`.

A webhook binding's `max_body_bytes` defaults to **1 MiB** (`WebhookDefaultMaxBodyBytes`),
not the unlimited default that `s3` / `azure_blob` get — webhook receivers process a
delivery synchronously, so an unbounded body can stall them. Set `max_body_bytes: 0` on the
binding to opt out of that protective cap. See [connector-bindings.md](connector-bindings.md)
for the per-binding sampling / filter / body-cap overrides.

The webhook connector also passes through the SSRF guard at config-validation time:
private-network `url` targets are rejected unless `SLUICE_WEBHOOK_ALLOW_PRIVATE` is set
(see [connectors.md](connectors.md) and [environment-variables.md](environment-variables.md)).

## Cross-references

- [telemetry-service.md](telemetry-service.md) — the service that terminates this webhook plus the two OTLP feeds.
- [telemetry-service-api.md](telemetry-service-api.md) — the console query API over the stored events.
- [telemetry-database-schema.md](telemetry-database-schema.md) — the `request_events` / `request_payloads` row shapes.
- [connectors.md](connectors.md) — the durable `s3` / `azure_blob` connectors and the SSRF guard.
- [connector-bindings.md](connector-bindings.md) — per-binding sampling, filter, and body-cap overrides.
- [spool.md](spool.md) — the disk-backed buffer the durable connectors drain through (the webhook bypasses it entirely).
