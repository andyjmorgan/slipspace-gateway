# Sluice quickstart — Docker Compose

Turnkey Docker Compose stacks that run Sluice from the **published GHCR images**
(no source checkout, no build toolchain) and proxy the real OpenAI / Anthropic /
Gemini APIs. Pick the stack you want, drop your keys in `.env`, and `up`.

Three stacks:

| Stack | File | What you get |
|---|---|---|
| **Gateway + admin console** | `compose.admin.yaml` | Data plane + the management console |
| **Gateway only** | `compose.minimal.yaml` | Just the data plane (no console) |
| **Gateway + telemetry** | `compose.telemetry.yaml` | Data plane + console + the central telemetry service (+ Postgres) |

All three share `config/` and one `.env`.

---

## Prerequisites

- Docker with the Compose plugin (`docker compose version`).
- API keys for whichever providers you'll call.

## 1. Configure

```sh
cd deploy/quickstart
cp .env.example .env
# edit .env — set OPENAI_API_KEY / ANTHROPIC_API_KEY / GEMINI_API_KEY
```

`.env` is gitignored, so your keys never get committed. The only value you
*must* change is at least one provider key; everything else has a working
default (including `SLUICE_CLIENT_API_KEY`, the key your SDK sends).

> **How credentials reach the gateway.** Sluice reads fully-literal trusted YAML
> — it does not interpolate `${VAR}` itself. So each stack runs a tiny `init`
> step that renders `config/policy.template.yaml` (substituting your `.env`
> keys) into the gateway's config volume *before* the gateway starts. You never
> edit YAML by hand.

## 2. Run a stack

**Gateway + admin console**
```sh
docker compose -f compose.admin.yaml up -d --wait
```

**Gateway only (no console)**
```sh
docker compose -f compose.minimal.yaml up -d --wait
```

**Gateway + telemetry**
```sh
docker compose -f compose.telemetry.yaml up -d --wait
```

> Switching between stacks reuses one Compose project (`sluice-quickstart`). Add
> `--remove-orphans` when you switch, or `down` the previous stack first.

## 3. Send a request

Point any OpenAI/Anthropic/Gemini SDK (or curl) at the data plane on `:8585`,
authenticating with your `SLUICE_CLIENT_API_KEY`:

```sh
curl http://localhost:8585/v1/chat/completions \
  -H "Authorization: Bearer sk_quickstart_demo_key" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"ping"}]}'
```

Routing is model-keyed: `gpt-*`/`o1*`/`o3*` → OpenAI, `claude-*` → Anthropic,
`gemini-*` → Gemini. The native Anthropic Messages (`/v1/messages`) and Gemini
(`/v1beta/models/{model}:generateContent`) paths work too, as do the
provider-native auth headers (`x-api-key`, `x-goog-api-key`).

## 4. Consoles

| URL | Stack | Login |
|---|---|---|
| `http://localhost:8081/admin` | admin, telemetry | `admin` / `SLUICE_ADMIN_PASSWORD` |
| `http://localhost:8686` | telemetry | `admin` / `sluice-telemetry` (the bcrypt default in `config/telemetry.yaml`) |

(The gateway's own console is under `/admin`; the telemetry console is at the
root of `:8686`.)

In the telemetry stack the gateway pushes gen_ai spans + sluice meters to the
telemetry service over OTLP (`:8687`); the telemetry console aggregates them
fleet-wide.

## 5. Tear down

```sh
docker compose -f compose.<stack>.yaml down       # keep volumes (spool, pg)
docker compose -f compose.<stack>.yaml down -v    # also wipe volumes
```

---

## Ports

| Port | Listener | Stacks |
|---|---|---|
| `8585` | Data plane (proxy) | all |
| `8081` | Admin console (SPA + `/api/v1`) | admin, telemetry |
| `8686` | Telemetry console + Record webhook ingest | telemetry |
| `8687` | Telemetry OTLP gRPC | telemetry |

Only `:8585` is meant to face clients. Keep the management ports private.

## Before you expose any of this

The defaults are tuned for a quick local trial, **not** the public internet:

- Change `SLUICE_CLIENT_API_KEY` and `SLUICE_ADMIN_PASSWORD` in `.env`.
- Change the telemetry console password: replace `console.password_hash` in
  `config/telemetry.yaml` (generate with
  `htpasswd -bnBC 10 "" 'your-password' | tr -d ':\n' | sed 's/^\$2y/\$2a/'`).
- Pin `SLUICE_IMAGE_TAG` to a release (e.g. `v1.1.18`) instead of `latest`.

---

## Full Record / audit feed (telemetry stack, optional)

By default the gateway → telemetry link is **OTLP only** (dashboard + per-request
rows + metric panels). To *also* ship the full per-request **Record** — request
and response bodies, headers, the fired-rule chain — so the telemetry console's
message **inspector** has bodies to show, add the HMAC Record webhook:

1. In `.env`, set a shared secret and allow the private webhook target:
   ```sh
   SLUICE_TELEMETRY_HMAC_SECRET=change-me-shared-hmac-secret
   SLUICE_WEBHOOK_ALLOW_PRIVATE=true
   ```
   (`telemetry` is a compose service name → a private IP; the gateway's SSRF
   guard blocks private webhook targets unless this is set.)

2. In `config/policy.template.yaml`, add a connector + binding to the `default`
   configuration:
   ```yaml
   connectors:
     - name: central-telemetry
       type: webhook
       url: http://telemetry:8686/api/v1/ingest/record
       secret_ref: env:SLUICE_TELEMETRY_HMAC_SECRET
       gateway_id: quickstart-gw
       timeout_ms: 5000
   ```
   and under `configurations.default`:
   ```yaml
       connector_bindings:
         - { connector: central-telemetry, sampling: 1.0 }
   ```

3. In `config/telemetry.yaml`, register the gateway so its pushes verify
   (id + secret must match step 1/2):
   ```yaml
   gateways:
     - id: quickstart-gw
       hmac_secret: change-me-shared-hmac-secret
   ```

The webhook is best-effort and non-blocking — a slow/wedged receiver only ever
costs dropped telemetry, never client latency. See
[`docs/telemetry-webhook.md`](../../docs/telemetry-webhook.md).

---

## Verifying the bundle (no provider keys)

`compose.smoke.yaml` is a verification overlay: it swaps the real upstreams for
the in-stack `sluice-mockllm` (canned, SDK-valid responses) so the whole bundle
can be exercised with no provider credentials.

```sh
docker compose -f compose.admin.yaml -f compose.smoke.yaml up -d --wait
# stage canned responses on the mock (POST http://localhost:5555/control/responses),
# then run the smoke suite against the local stack:
SLUICE_BASE_URL=http://localhost:8585 SLUICE_API_KEY=sk_quickstart_demo_key \
  make -C ../.. smoke
```

For a real end-to-end check, run `make smoke` against `compose.admin.yaml` with
no overlay once your real keys are in `.env`.

## How it maps to a real deploy

This bundle is the compose-shaped version of the Kubernetes topology in
[`docs/deployment.md`](../../docs/deployment.md): the same image, the same config
directory, the same listener ports, the same OTLP wiring. Scale the gateway to a
Deployment, mount `config/` from a ConfigMap/Secret, and the shape carries over.
