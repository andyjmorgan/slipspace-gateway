# Connectors

A **connector** is a destination the spool ships sealed segments to. Each connector is a reusable definition under the top-level `connectors:` YAML block; configurations attach connectors via `connector_bindings:` and can apply different sampling / filter / size-cap overrides per binding.

Three connector types ship today: `s3`, `azure_blob`, and `webhook`. This page is the YAML reference for every per-type field, the auth modes each accepts, and the operational caveats that don't fit on the field table.

**Two fundamentally different runtimes hide behind one YAML block.** `s3` and `azure_blob` are *disk-backed spool connectors*: each evaluated record lands on the connector spool, is batched into sealed `.ndjson.zst` segments, and an upload worker ships each segment with a retry budget, circuit breaker, and deadletter path. `webhook` is **not** spool-backed — it is a *real-time per-record pusher* ([`internal/telemetry/pusher`](../internal/telemetry/pusher/)) that POSTs each `cc.Record` as JSON the moment the request completes, from a bounded worker pool, dropping on a full queue. Transient failures retry in-memory with capped exponential backoff, but there is no disk queue, no segment, and no deadletter for webhook. Keep this split in mind throughout: the spool semantics on [spool.md](spool.md) apply to `s3`/`azure_blob` only.

For *how* records reach a connector — the binding evaluation — see [connector-bindings.md](connector-bindings.md). For *what happens once spool-backed records are written to disk* — the spool runtime — see [spool.md](spool.md). For the webhook push contract end-to-end (the telemetry-service ingest side included), see [telemetry-webhook.md](telemetry-webhook.md).

Source of truth: [`contracts/config/connectors.go`](../contracts/config/connectors.go) (struct shapes) and [`contracts/config/connectors_validate.go`](../contracts/config/connectors_validate.go) (per-type required fields). Spool-backed implementations: [`internal/connector/s3/`](../internal/connector/s3/), [`internal/connector/azureblob/`](../internal/connector/azureblob/), built by [`internal/connector/factory/factory.go`](../internal/connector/factory/factory.go). The webhook pusher lives in [`internal/telemetry/pusher/pusher.go`](../internal/telemetry/pusher/pusher.go) and is wired directly in [`cmd/gateway/main.go`](../cmd/gateway/main.go) (`setupPushers`) — it is **not** a `connector.Connector` and never reaches the factory (the factory rejects `webhook` as a safety net).

---

## Table of contents

1. [Mental model](#mental-model)
2. [Top-level YAML shape](#top-level-yaml-shape)
3. [Common fields](#common-fields)
4. [Rotation knobs](#rotation-knobs)
5. [secret_ref indirection](#secret_ref-indirection)
6. [s3 connector](#s3-connector)
7. [azure_blob connector](#azure_blob-connector)
8. [webhook connector](#webhook-connector)
9. [Cross-references](#cross-references)

---

## Mental model

```mermaid
flowchart LR
    subgraph YAML[policy.yaml]
        Conn[connectors:<br/>top-level slice]
        Bindings[configurations[N].<br/>connector_bindings]
    end
    Bindings -- references by name --> Conn
    Conn -- s3 / azure_blob --> Spool[spool track<br/>per connector]
    Conn -- webhook --> Pusher[real-time pusher<br/>bounded worker pool]
    Spool -- Upload sealed segment --> S3[(S3 / MinIO /<br/>S3-compatible)]
    Spool -- Upload sealed segment --> Az[(Azure Blob<br/>Storage)]
    Pusher -- POST cc.Record JSON --> WH[(HTTPS record<br/>receiver)]
```

One `connectors:` entry per logical destination. Many configurations may bind the same connector with different sampling / filter overrides; the connector itself owns rotation/transport policy and auth. The dispatch fork happens per record in [`cmd/gateway/reporter.go`](../cmd/gateway/reporter.go) (`dispatchRecord`): `s3`/`azure_blob` bindings enqueue onto the disk spool, `webhook` bindings hand the record to the matching pusher. Different connector instances are independent — each spool-backed connector has its own spool track, retry budget, and circuit breaker; each webhook connector has its own pusher with its own bounded queue and dropped/sent/failed counters.

---

## Top-level YAML shape

```yaml
# policy.yaml
connectors:
  - name: <unique-name>
    type: s3 | azure_blob | webhook
    # ... type-specific fields below
    rotation:
      max_bytes: 67108864       # uncompressed byte cap on the active segment
      max_age_seconds: 60       # time the active segment may stay open
    # ... auth or secret_ref, depending on type
```

`connectors:` is a flat slice. Each entry must declare a unique `name:` (validation rejects duplicates) and a `type:` from the recognised set. Per-type required fields are enforced by `Connector.Validate()`; cross-type field mixing is a config-load error.

The slice may be empty or absent — operators who don't need persistent capture run with no connectors and the spool is not constructed.

---

## Common fields

These fields apply to every connector entry regardless of type.

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string | yes | Operator-visible identifier `ConnectorBinding.Connector` references. Must be unique across the `connectors:` slice. Names containing slashes or path separators are not validated against but will surface confusing spool subdirectories — stick to `[a-z0-9-]+`. |
| `type` | enum | yes | One of `s3`, `azure_blob`, `webhook`. Anything else aborts load with `ErrConnectorValidation`. |
| `rotation` | object | no | Per-connector override of the spool's default rotation policy. Both sub-fields default when unset. See [Rotation knobs](#rotation-knobs). |

---

## Rotation knobs

`rotation:` overrides the spool's default segment rotation policy on a per-connector basis. See [spool.md → Rotation policy](spool.md#rotation-policy) for the full semantics.

```yaml
connectors:
  - name: example
    type: s3
    rotation:
      max_bytes: 4194304        # 4 MiB
      max_age_seconds: 5
```

| Field | Type | Default | Effect |
|---|---|---|---|
| `max_bytes` | int (uncompressed bytes) | `67108864` (64 MiB) | The active segment seals when its accumulated uncompressed byte count reaches this. Zero falls back to the default. |
| `max_age_seconds` | int (seconds) | `60` | The active segment seals when this much wall-clock time has elapsed since open. Zero falls back to the default. |

Either trigger alone seals; whichever fires first rotates. `rotation:` only affects spool-backed connectors (`s3`, `azure_blob`) — it governs how long records batch on disk before a segment uploads. Webhook connectors ignore `rotation:` entirely (each record is POSTed immediately, never batched into a segment), so the knob is meaningless for them; tune `timeout_ms` and the binding's `max_body_bytes` instead.

---

## secret_ref indirection

Every secret a connector reads is named indirectly via a `secret_ref` mini-language. Two prefixes are accepted:

| Form | Meaning |
|---|---|
| `env:NAME` | Read the named environment variable at connector-construction time. The variable must be set; an empty value is a load-time error. |
| `file:/absolute/path` | Read the file at the absolute path. Trailing CR/LF is trimmed. |

Anything else (literal credential, `kubernetes:`, etc.) is rejected at config-load with `secret_ref must start with env: or file:`.

The indirection keeps credential material out of the YAML file on disk — if the file is captured for audit, all that's exposed is the name of the env var or the file path. Webhook signing secrets, static cloud credentials, and SAS tokens all flow through the same mini-language.

For workload-identity modes on s3 and azure_blob, no `secret_ref` is needed — the SDK's default credential chain resolves the identity at runtime.

---

## s3 connector

Ships records to S3 or any S3-compatible backend (MinIO, SeaweedFS, Garage, Ceph RGW, …). Object keys are partitioned for predictable listing.

### Fields

```yaml
connectors:
  - name: prod-audit
    type: s3
    bucket: sluice-audit
    region: us-east-1
    prefix: events/
    endpoint_url: ""              # default: real AWS S3
    use_path_style: false         # default: virtual-hosted (AWS); set true for MinIO
    rotation:
      max_bytes: 67108864
      max_age_seconds: 60
    auth:
      mode: workload_identity     # or "static", "assume_role"
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `bucket` | string | yes | S3 bucket name. Pre-create the bucket; the connector does not. |
| `region` | string | yes | AWS region (e.g. `us-east-1`). Required even for S3-compatible backends — the AWS SDK refuses to construct a client without one. For MinIO use any plausible value (`us-east-1` works). |
| `prefix` | string | no | Key prefix beneath the bucket. Leave empty for bucket-root. Trailing slash optional — the connector handles both shapes. |
| `endpoint_url` | string (URL) | no | Point at an S3-compatible backend instead of real AWS S3. URL must parse; scheme is left to the operator (typically `https://`). |
| `use_path_style` | bool | no | `false` (default) selects virtual-hosted addressing (`<bucket>.s3.region.amazonaws.com`); `true` selects path-style (`<endpoint>/<bucket>/<key>`). AWS prefers virtual-hosted; MinIO defaults to path-style. |
| `auth` | object | depends | Required when no `workload_identity` SDK chain is available; see [s3 auth modes](#s3-auth-modes). |

Cloud-storage-only fields (`bucket`, `region`, `endpoint_url`, `use_path_style`) and webhook-only fields (`url`, `secret_ref`, `gateway_id`, `timeout_ms`) are mutually exclusive — mixing them aborts the load.

### Object key layout

The S3 connector partitions keys by instance and date so a per-pod, date-range scan against the bucket is cheap ([`internal/connector/s3/connector.go`](../internal/connector/s3/connector.go) `objectKey`):

```
[<prefix>/]records/instance=<id>/date=YYYY-MM-DD/hour=HH/<unix_ns>-<seq>-<unix_ns>-<seq>.ndjson.zst
```

- `<prefix>` — the optional `prefix:` field, slash-trimmed; omitted entirely when unset (the key then starts at `records/`).
- `records/` — a fixed segment so audit objects share one root regardless of prefix.
- `instance=<id>` — the gateway pod's stable `InstanceID` (`"local"` when unset). Concurrent replicas never collide because the instance partition differs.
- `date=YYYY-MM-DD` / `hour=HH` — UTC. These currently come from the **upload wall-clock time**, not the captured records. The connector prefers the segment's earliest record timestamp (`TsMinNs`) and falls back to its own clock when that is zero ([`internal/connector/s3/connector.go`](../internal/connector/s3/connector.go) `objectKey`) — but `TsMinNs` is not yet plumbed through: the spool builds every `SealedSegment` without setting it ([`internal/spool/track.go`](../internal/spool/track.go) `uploadOne`), so it is always zero and the clock fallback is always taken. The partition therefore reflects when the segment uploaded, which can lag record capture by up to a rotation window plus any retry delay.
- `<unix_ns>-<seq>-<delivery_id>` — the **doubled** filename stem. The on-disk segment is named `<unix_ns>-<seq>.ndjson.zst` ([`internal/spool/segment.go`](../internal/spool/segment.go)); the connector strips the extension to recover the stem `<unix_ns>-<seq>`, then appends `-<delivery_id>` before re-adding `.ndjson.zst`. The catch: `<delivery_id>` **is** that same stem — `deliveryIDFromFilename` derives it by stripping `.ndjson.zst` off the filename ([`internal/spool/track.go`](../internal/spool/track.go)) — so the emitted object name repeats it verbatim: `<unix_ns>-<seq>-<unix_ns>-<seq>.ndjson.zst`. `<delivery_id>` is stable across upload retries, so a retried upload writes to the same key — S3's put-object semantics make this an idempotent overwrite, no duplicate object.

### s3 auth modes

`auth.mode` selects the credential resolution policy.

| Mode | Required fields | Behaviour |
|---|---|---|
| `workload_identity` (default, also empty) | none — every credential ref must be unset | Uses the AWS SDK's default credential chain: environment, shared file, EC2 IMDS, EKS pod identity, etc. The right choice for k8s with IRSA / EKS Pod Identity. |
| `static` | `access_key_id_ref`, `secret_access_key_ref` | Long-lived access key. Discouraged in production; use only when workload identity is unavailable. |
| `assume_role` | `role_arn`; optional `external_id_ref` | AWS STS AssumeRole on top of the base credential chain. `external_id_ref` is recommended for cross-account trust. |

```yaml
# static
auth:
  mode: static
  access_key_id_ref: env:S3_ACCESS_KEY_ID
  secret_access_key_ref: env:S3_SECRET_ACCESS_KEY

# assume_role
auth:
  mode: assume_role
  role_arn: arn:aws:iam::123456789012:role/sluice-audit-writer
  external_id_ref: env:S3_AUDIT_EXTERNAL_ID
```

The validator enforces that fields belonging to other modes (`sas_token_ref`, `account_key_ref`) are absent on s3 connectors, and rejects credential refs on `workload_identity` (the SDK chain handles it).

### Caveats

- **Bucket pre-creation is the operator's job.** The connector does not call `CreateBucket`.
- **Retryable failures:** transport errors, 5xx, 429, throttling, slowdown. The spool's per-segment retry budget caps the blast radius.
- **Permanent failures:** 4xx other than 429 (auth, permission denied, malformed request). The segment goes straight to deadletter.
- **No object metadata customisation in v1.** The `PutObject` call sets `ContentType: application/zstd` ([`internal/connector/s3/connector.go`](../internal/connector/s3/connector.go) `Upload`) and nothing else — no `Content-Encoding`, no SSE-KMS key selection, no object tags, no metadata headers. Those are a v1.2+ feature. The object body is the raw zstd-compressed ndjson segment; consumers decompress with zstd and read it line-delimited.

---

## azure_blob connector

Ships records to Azure Blob Storage. The fields and key layout mirror s3 but use Azure-native terminology.

### Fields

```yaml
connectors:
  - name: prod-audit-azure
    type: azure_blob
    account: sluiceprod
    container: audit
    prefix: events/
    rotation:
      max_bytes: 67108864
      max_age_seconds: 60
    auth:
      mode: workload_identity     # or "sas_token", "account_key"
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `account` | string | yes | Storage account name (without the `.blob.core.windows.net` suffix). |
| `container` | string | yes | Blob container name. Pre-create the container. |
| `prefix` | string | no | Blob name prefix beneath the container. |
| `auth` | object | depends | See [azure_blob auth modes](#azure_blob-auth-modes). |

Like s3, mixing cloud-storage fields with webhook fields aborts. The s3-specific fields (`bucket`, `region`, `endpoint_url`, `use_path_style`) are explicitly rejected on azure_blob entries.

### Blob key layout

Byte-for-byte the same shape as s3 ([`internal/connector/azureblob/connector.go`](../internal/connector/azureblob/connector.go) `blobName`):

```
[<prefix>/]records/instance=<id>/date=YYYY-MM-DD/hour=HH/<unix_ns>-<seq>-<unix_ns>-<seq>.ndjson.zst
```

The same partition fields and the same doubled `<unix_ns>-<seq>-<delivery_id>` filename derivation as s3 ([`internal/connector/azureblob/connector.go`](../internal/connector/azureblob/connector.go) `blobName`) — including that the `date=` / `hour=` partition currently comes from the upload clock (`TsMinNs` is not plumbed through) and that `delivery_id` equals the segment stem, so the stem repeats in the blob name. See [Object key layout](#object-key-layout) above for the full explanation. Azure Blob Storage's put-blob semantics make retried uploads idempotent overwrites. The upload streams the raw zstd segment via `UploadStream`; no `Content-Type` or blob metadata is set in v1.

### azure_blob auth modes

| Mode | Required fields | Behaviour |
|---|---|---|
| `workload_identity` (default, also empty) | none | Uses `azidentity.DefaultAzureCredential` — Managed Identity, Workload Identity Federation, az-cli, etc. Right choice for AKS with workload identity. |
| `sas_token` | `sas_token_ref` | Container or account-level SAS token. Time-bounded; rotate before expiry. |
| `account_key` | `account_key_ref` | Storage account access key. Highest blast radius if leaked; prefer one of the other modes. |

```yaml
# sas_token
auth:
  mode: sas_token
  sas_token_ref: env:AZURE_SAS_TOKEN

# account_key
auth:
  mode: account_key
  account_key_ref: env:AZURE_STORAGE_ACCOUNT_KEY
```

The validator rejects s3-only refs (`access_key_id_ref`, `role_arn`, etc.) on azure_blob connectors.

### Caveats

- Same operator responsibilities as s3: pre-create the container, manage credential rotation, monitor the destination.
- 429 / 5xx / throttling → retryable. 4xx other than 429 → permanent.
- No blob metadata or tag customisation in v1; same v1.2+ deferral as s3.

---

## webhook connector

Pushes each evaluated `cc.Record` to an HTTPS endpoint as an individual POST, in real time, the moment the request completes. This is the gateway's *channel-3* record sink — the authoritative per-request log (headers, bodies, rule chain, resilience attempts) — and its canonical receiver is the central telemetry service's `/api/v1/ingest/record`, though any endpoint that verifies the signature can consume it.

**Webhook is not spool-backed.** Unlike `s3`/`azure_blob`, there is no disk queue, no `.ndjson.zst` segment, no circuit breaker, and no deadletter. Records flow from a bounded in-memory worker pool ([`internal/telemetry/pusher/pusher.go`](../internal/telemetry/pusher/pusher.go)); transient failures (network errors, `408`/`429`/`5xx`) retry in-worker with capped exponential backoff (5 attempts, 250ms doubling to a 5s cap — safe because ingest upserts on `correlation_id`), and a full queue drops the record on the floor (bumping a `dropped` counter) rather than ever blocking the request path (invariant #2). The full end-to-end push contract — including the telemetry-service ingest side, the gateway registry, and secret management — is documented in [telemetry-webhook.md](telemetry-webhook.md); this section covers the gateway-side YAML and wire shape only.

### Fields

```yaml
connectors:
  - name: telemetry-records
    type: webhook
    url: https://telemetry.example.com/api/v1/ingest/record
    secret_ref: env:SLUICE_TELEMETRY_SECRET
    gateway_id: prod-gateway-1
    timeout_ms: 5000
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `url` | string (URL) | yes | The endpoint each record is POSTed to. Scheme must be `http` or `https`. Loopback / private / link-local hosts are rejected at config-load unless the test-only override is set (see [SSRF guard](#ssrf-guard)). |
| `secret_ref` | secret_ref | yes | HMAC-SHA256 signing key. Resolved via [secret_ref indirection](#secret_ref-indirection) at startup; a missing env var / unreadable file fails boot loudly rather than silently dropping every push. |
| `gateway_id` | string | no | Sent as the `X-Sluice-Gateway-Id` header so a receiver that keys HMAC secrets per gateway (the telemetry-service registry) can look the secret up. Optional for a generic receiver that verifies the signature alone; **required** when pushing to the telemetry service. |
| `timeout_ms` | int (ms) | yes | Per-push HTTP timeout. Must be in `(0, 60000]`. |

`rotation:` and `auth:` are not used by webhook connectors, and cloud-storage fields (`bucket`, `region`, `account`, `container`, `endpoint_url`, `use_path_style`) are explicitly rejected at config-load ([`contracts/config/connectors_validate.go`](../contracts/config/connectors_validate.go) `validateWebhook`).

### Delivery contract

For each evaluated record that passes its binding's sampling / filter / body-cap, the pusher marshals the `cc.Record` to JSON and POSTs that single object to `url` ([`internal/telemetry/pusher/pusher.go`](../internal/telemetry/pusher/pusher.go) `send`). The body is **one JSON record**, not a compressed ndjson segment. Headers:

| Header | Value | Notes |
|---|---|---|
| `Content-Type` | `application/json` | The body is a single JSON-encoded `cc.Record`. |
| `X-Sluice-Gateway-Id` | the `gateway_id` field | Identifies the sending gateway to the receiver's registry so it can select the right HMAC secret. Empty when `gateway_id` is unset. |
| `X-Sluice-Signature` | `<hex_hmac_sha256>` | Hex-encoded HMAC-SHA256 of the **raw request body** under the resolved secret. No timestamp, no `t=`/`v1=` prefix — just the bare hex digest. |

The standard Go `User-Agent` is sent (no custom build-version header). Body bodies above the binding's `max_body_bytes` (no per-type default cap — [`contracts/config/connectors.go`](../contracts/config/connectors.go) `DefaultMaxBodyBytes` returns 0 for every connector type including webhook; the 1 MiB webhook default was removed) are stripped or the record dropped *before* it reaches the pusher. The only ceiling is the bodycapture middleware's 10 MiB inbound read limit; see [connector-bindings.md](connector-bindings.md).

### Signature verification

The signature is the hex HMAC-SHA256 of the exact raw request body under the shared secret — nothing else is mixed in. Verification, in pseudocode:

```python
sig_hex = req.headers["X-Sluice-Signature"]                      # bare hex digest
expected = hmac.new(secret, req.body, hashlib.sha256).hexdigest()
if not hmac.compare_digest(expected, sig_hex):
    return 401
# record is one JSON cc.Record:
record = json.loads(req.body)
```

The telemetry service does exactly this ([`internal/telemetry/registry/registry.go`](../internal/telemetry/registry/registry.go)): it selects the secret by `X-Sluice-Gateway-Id`, recomputes the HMAC over the body, and `hmac.Equal`-compares it to the supplied hex. There is **no** timestamp component and **no** replay window — those are not implemented. A receiver that wants replay protection must add its own freshness check from a field inside the record (e.g. the record's `ts_ns`), not from the signature.

### Outcome handling

There is no segment lifecycle, so there is no `uploading/` → `deadletter/` state machine. The pusher classifies each POST and retries transient failures with capped exponential backoff (default 5 attempts, 250ms base doubling to a 5s cap) before declaring a record lost ([`internal/telemetry/pusher/pusher.go`](../internal/telemetry/pusher/pusher.go)):

| Outcome | Treatment |
|---|---|
| `2xx` | Success. Bumps the `sent` counter. |
| transport error (DNS, TLS, EOF, timeout), `408`, `429`, `5xx` | Transient failure. Bumps the `failed` counter (per attempt), logs at debug, retries with backoff. Exhausting all attempts loses the record: `lost`++, warn-logged, reported as reason `exhausted`. |
| any other non-`2xx` (remaining 4xx, 1xx, 3xx) | Permanent rejection (e.g. bad HMAC). `failed`++, never retried — the record is lost immediately (`lost`++, reason `rejected`). |
| JSON marshal error | `failed`++, lost before any HTTP call (reason `encode`). Never retried. |
| queue full at `Enqueue` time | Dropped before the worker pool ever sees it. Bumps the `dropped` counter (reason `queue_full`, distinct from `failed`). |

`dropped`, `sent`, `failed`, and `lost` are exposed as `Pusher.Dropped()` / `Sent()` / `Failed()` / `Lost()`, and the gateway wires the pusher's loss hooks to the `gateway.telemetry.push.dropped.total` (by `connector` + `reason`) and `gateway.telemetry.push.failures.total` (by `connector` + `kind`) meters so loss is monitorable. Retrying is safe — the telemetry service's record ingest is an idempotent upsert keyed by `correlation_id` — and retries run inside the worker that owns the record, so the bounded queue stays the only buffer: delivery is at-most-once-per-success, lossy under sustained pressure, and never stalls the request path on a slow or wedged receiver.

The worker pool and queue use built-in defaults ([`internal/telemetry/pusher/pusher.go`](../internal/telemetry/pusher/pusher.go)): **2** workers drain a bounded channel of **1024** records, with a **5 s** per-push timeout fallback (in practice overridden by the connector's required `timeout_ms`). The `dropped` counter bumps whenever `Enqueue` finds those 1024 slots full, so the buffer depth **is** the drop-on-full threshold — a receiver that can't keep up with the inbound record rate starts shedding once 1024 records are in flight.

### SSRF guard

The webhook URL is validated **once, at config-load** ([`contracts/config/connectors_validate.go`](../contracts/config/connectors_validate.go) `validateWebhook` → `rejectLocalOrPrivateHost`): the scheme is enforced to `http`/`https`, and a literal-IP host in a loopback, RFC1918-private, link-local, unspecified (`0.0.0.0`), or multicast range aborts the load. The pusher's HTTP client does **no** per-call DNS re-resolution check, so a DNS *name* that resolves to a private IP is not caught at dial time — the config-load guard only inspects literal IPs in the URL.

The guard has one escape hatch:

| Env var | Default | Notes |
|---|---|---|
| `SLUICE_WEBHOOK_ALLOW_PRIVATE` | unset | Setting to `1` or `true` makes config-load validation accept loopback / RFC1918 / link-local hosts for **every** webhook connector in the process. **Test-only.** The e2e harness sets it so its `httptest.Server` (bound to 127.0.0.1) can be wired as a webhook receiver. **Never set this in production.** |

It is a process-global flag by design, so a misconfiguration cannot quietly downgrade only one connector's posture.

### Caveats

- **At-most-once-per-success, lossy under sustained pressure.** A full queue drops the record at `Enqueue` (reason `queue_full`). Transient failures (network error, 408/429/5xx) are retried with capped exponential backoff (default 5 attempts) before the record is declared lost (reason `exhausted`); a permanent non-2xx (other 4xx, e.g. bad HMAC) is lost immediately (reason `rejected`). There is no on-disk durability — use `s3`/`azure_blob` when you need an audit trail that survives a prolonged outage.
- **One record per request.** Each POST carries a single `cc.Record`; the receiver does not get batched ndjson. High request rates produce a high POST rate — size the receiver accordingly.
- **No client-supplied idempotency header.** The record body carries its own identifiers (`correlation_id`, `ts_ns`); a receiver that needs dedupe uses those, not a transport header.

---

## Cross-references

- [connector-bindings.md](connector-bindings.md) — the per-configuration knobs that decide which records reach each connector.
- [spool.md](spool.md) — the disk-backed runtime for `s3`/`azure_blob`; rotation, retry, breaker, recovery.
- [telemetry-webhook.md](telemetry-webhook.md) — the webhook push contract end-to-end, including the telemetry-service ingest side, the gateway registry, and HMAC secret management.
- [configuration-model.md](configuration-model.md) — where the `connectors:` block sits in the YAML file allow-list.
- [environment-variables.md](environment-variables.md) — `SLUICE_SPOOL_ROOT`, `SLUICE_WEBHOOK_ALLOW_PRIVATE`.
- [`contracts/config/connectors.go`](../contracts/config/connectors.go) — struct shapes.
- [`contracts/config/connectors_validate.go`](../contracts/config/connectors_validate.go) — per-type validators.
- [`internal/connector/s3/`](../internal/connector/s3/), [`internal/connector/azureblob/`](../internal/connector/azureblob/) — spool-backed connector implementations; [`internal/connector/factory/factory.go`](../internal/connector/factory/factory.go) builds them.
- [`internal/telemetry/pusher/pusher.go`](../internal/telemetry/pusher/pusher.go) — the webhook real-time pusher; wired in [`cmd/gateway/main.go`](../cmd/gateway/main.go) (`setupPushers`) and dispatched in [`cmd/gateway/reporter.go`](../cmd/gateway/reporter.go) (`dispatchRecord`).
