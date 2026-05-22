// Package webhook implements a Connector that POSTs sealed segments
// to a customer-supplied HTTPS endpoint.
//
// Request shape:
//
//   - Method: POST
//   - Body:   the sealed segment's bytes as-is (ndjson.zst).
//   - Headers:
//     Content-Type:           application/x-ndjson
//     Content-Encoding:       zstd
//     X-Sluice-Signature:     t=<unix>,v1=<hex-sha256-of-"<unix>.<body>">
//     X-Sluice-Delivery-Id:   stable across retries (segment's DeliveryID)
//     X-Sluice-Batch-Seq:     segment seq number, ordering hint
//     User-Agent:             sluice-gateway/<version>
//
// Response handling:
//
//   - 2xx                 → nil
//   - 4xx (except 429)    → *cc.Permanent (move to deadletter)
//   - 5xx, 429, timeout,
//     ctx cancelled       → *cc.Retryable (backoff + retry)
//
// SSRF defence:
//
//   - At config-load (in contracts/config): the URL must be http/https,
//     localhost / RFC1918 / link-local are rejected.
//   - At call time (here): DNS re-resolution of the URL host. If the
//     resolved IPs land on the deny-list (loopback, private, link-local,
//     0.0.0.0/8, multicast), the request is refused as *cc.Permanent.
//     Options.AllowPrivateNetworks=true disables the guard for tests
//     pointing at httptest.Server / Azurite / SeaweedFS.
package webhook
