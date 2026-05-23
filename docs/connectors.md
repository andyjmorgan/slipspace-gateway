# Connectors

A **connector** is a destination the spool ships sealed segments to. Each connector is a reusable definition under the top-level `connectors:` YAML block; configurations attach connectors via `connector_bindings:` and can apply different sampling / filter / size-cap overrides per binding.

Three connector types ship today: `s3`, `azure_blob`, and `webhook`. This page is the YAML reference for every per-type field, the auth modes each accepts, and the operational caveats that don't fit on the field table.

For *how* records reach a connector — the binding evaluation — see [connector-bindings.md](connector-bindings.md). For *what happens once records are written to disk* — the spool runtime — see [spool.md](spool.md).

Source of truth: [`contracts/config/connectors.go`](../contracts/config/connectors.go) (struct shapes) and [`contracts/config/connectors_validate.go`](../contracts/config/connectors_validate.go) (per-type required fields). Implementations: [`internal/connector/s3/`](../internal/connector/s3/), [`internal/connector/azureblob/`](../internal/connector/azureblob/), [`internal/connector/webhook/`](../internal/connector/webhook/).

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
    Conn --> Spool[spool track<br/>per connector]
    Spool -- Upload --> Dest{Destination}
    Dest --> S3[(S3 / MinIO /<br/>S3-compatible)]
    Dest --> Az[(Azure Blob<br/>Storage)]
    Dest --> WH[(HTTPS webhook<br/>receiver)]
```

One `connectors:` entry per logical destination. Many configurations may bind the same connector with different sampling / filter overrides; the connector itself owns rotation policy, auth, and transport details. Different connector instances pointing at the same physical destination (two S3 buckets, two webhooks) are independent — each has its own spool track, its own retry budget, its own circuit breaker.

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

Either trigger alone seals; whichever fires first rotates. Webhook tracks typically want both lower (e.g. 4 MiB / 5 s) for near-real-time delivery; S3/Azure tracks usually keep the defaults to amortise upload overhead.

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

Cloud-storage-only fields (`bucket`, `region`, `endpoint_url`, `use_path_style`) and webhook-only fields (`url`, `secret_ref`, `timeout_ms`) are mutually exclusive — mixing them aborts the load.

### Object key layout

The S3 connector partitions keys by date so a date-range scan against the bucket is cheap:

```
<prefix>/dt=YYYY-MM-DD/hour=HH/<instance_id>/<delivery_id>.ndjson.zst
```

`dt`, `hour` come from the segment's earliest record timestamp; `instance_id` is the gateway pod's hostname; `delivery_id` is the ULID minted at seal time, stable across upload retries. This means a retried upload writes to the same key — S3's put-object semantics make this an idempotent overwrite, no duplicate object.

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
- **No object metadata customisation in v1.** The connector sets `Content-Type: application/x-ndjson` and `Content-Encoding: zstd` and that's it. No SSE-KMS key selection, no object tags, no metadata headers — a v1.2+ feature.

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

Same shape as s3:

```
<prefix>/dt=YYYY-MM-DD/hour=HH/<instance_id>/<delivery_id>.ndjson.zst
```

The same partition fields. Azure Blob Storage's put-blob semantics make retried uploads idempotent overwrites.

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

Ships sealed segments to an HTTPS endpoint as POST requests. Useful when the consumer is your own service (logging pipeline, SIEM, custom enrichment) rather than a blob store.

### Fields

```yaml
connectors:
  - name: siem-ingest
    type: webhook
    url: https://siem.internal.example.com/sluice/ingest
    secret_ref: env:SIEM_WEBHOOK_SECRET
    timeout_ms: 30000
    rotation:
      max_bytes: 4194304        # 4 MiB
      max_age_seconds: 5
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `url` | string (URL) | yes | The customer-supplied endpoint that receives POSTs. Scheme must be `http` or `https`. Loopback / private / link-local addresses are rejected at config-load and re-checked at call time (see [SSRF guard](#ssrf-guard)). |
| `secret_ref` | secret_ref | yes | HMAC-SHA256 signing key. Resolved via [secret_ref indirection](#secret_ref-indirection). An empty resolved value aborts construction. |
| `timeout_ms` | int (ms) | yes | Per-call HTTP timeout. Must be in `(0, 60000]`. |

Cloud-storage fields (`bucket`, `region`, `account`, `container`, etc.) and the `auth:` block are explicitly rejected on webhook connectors.

### Delivery contract

For each sealed segment, the connector POSTs the raw `.ndjson.zst` bytes to `url` with these headers:

| Header | Value | Notes |
|---|---|---|
| `Content-Type` | `application/x-ndjson` | The decompressed media type. |
| `Content-Encoding` | `zstd` | The body is zstd-compressed. |
| `X-Sluice-Signature` | `t=<unix_ts>,v1=<hex_hmac_sha256>` | HMAC of `"<ts>.<body>"` keyed by the resolved secret. The receiver should reject signatures with timestamp drift beyond a reasonable window (5 min is conventional). |
| `X-Sluice-Delivery-Id` | The segment's ULID | **Stable across retries.** The receiver should dedupe on this. |
| `X-Sluice-Connector` | The connector `name` | Diagnostic — tells the receiver which connector produced the delivery. |
| `User-Agent` | `sluice-gateway/<version>` | Build version. |

### Signature verification

The HMAC is computed over `"<unix_ts>.<raw_body_bytes>"` with the shared secret. Verification, in pseudocode:

```python
ts, sig = parse_header(req.headers["X-Sluice-Signature"])  # "t=<ts>,v1=<hex>"
expected = hmac.new(secret, f"{ts}.".encode() + req.body, hashlib.sha256).hexdigest()
if not hmac.compare_digest(expected, sig):
    return 401
if abs(time.time() - int(ts)) > 300:
    return 401
```

Both checks are mandatory. Skipping the timestamp window lets an attacker replay an intercepted delivery indefinitely; skipping the signature lets anyone post arbitrary data to the endpoint.

### Status code mapping

| Status | Treatment |
|---|---|
| `2xx` | Success. Segment removed from `uploading/`. |
| `429` | Retryable. The per-segment retry budget applies. |
| `5xx` | Retryable. Same. |
| `4xx` other than `429` | Permanent. Segment moves to `deadletter/`. |
| `1xx`, `3xx` | Retryable. These are unexpected on a POST to a customer endpoint; classified as transient to ride out quirky load balancers without immediate deadletter. |
| transport error (DNS, TLS, EOF, timeout) | Retryable. The spool's retry schedule covers transient network blips. |

### SSRF guard

Webhook URLs are validated twice — once at config-load, once at every call.

- **Config-load:** the URL is parsed and its scheme is enforced to `http` or `https`. Literal IPs in private (RFC1918), link-local, or loopback ranges abort the load.
- **Per-call:** before each upload, the URL host is re-resolved via DNS and every returned IP is checked against the denylist (loopback, private RFC1918, link-local, unspecified `0.0.0.0/8`, multicast). Hitting a denied address fails with `*Permanent` so the segment deadletters immediately — DNS rebinding cannot pivot a previously-valid URL into a private one.

The runtime guard has one escape hatch:

| Env var | Default | Notes |
|---|---|---|
| `SLUICE_WEBHOOK_ALLOW_PRIVATE` | unset | Setting to `1` or `true` disables the per-call SSRF re-resolve guard for **every** webhook connector in the process. **Test-only.** The e2e harness sets this so its `httptest.Server` (which binds loopback) is reachable. **Never set this in production.** |

There is no per-connector opt-out — it's a process-global escape hatch by design, so a misconfiguration cannot quietly downgrade only one connector's security posture.

### Caveats

- **No retries with body changes.** The retry budget re-sends the exact same body; if the receiver returns a retryable error and then a permanent one on retry, the segment deadletters with whatever the final error was. There is no body mutation between attempts.
- **No incremental delivery.** Each segment is one HTTP request. Segments above a few MiB compressed (which is rare with zstd on ndjson) will block the receiver for the duration; tune `max_bytes` smaller for webhook destinations.
- **No support for receiver-provided idempotency keys.** The `X-Sluice-Delivery-Id` is the only dedupe identifier; receivers must use it.

---

## Cross-references

- [connector-bindings.md](connector-bindings.md) — the per-configuration knobs that decide which records reach each connector.
- [spool.md](spool.md) — the disk-backed runtime; rotation, retry, breaker, recovery.
- [configuration-model.md](configuration-model.md) — where the `connectors:` block sits in the YAML file allow-list.
- [environment-variables.md](environment-variables.md) — `SLUICE_SPOOL_ROOT`, `SLUICE_WEBHOOK_ALLOW_PRIVATE`.
- [`contracts/config/connectors.go`](../contracts/config/connectors.go) — struct shapes.
- [`contracts/config/connectors_validate.go`](../contracts/config/connectors_validate.go) — per-type validators.
- [`internal/connector/s3/`](../internal/connector/s3/), [`internal/connector/azureblob/`](../internal/connector/azureblob/), [`internal/connector/webhook/`](../internal/connector/webhook/) — implementations.
