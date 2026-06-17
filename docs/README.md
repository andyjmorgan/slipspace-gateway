# sluice-gateway documentation

Operator and developer reference for sluice-gateway. Each page is self-contained, with cross-references where one surface depends on another.

If you're new to the project, the suggested reading order is **Configuration → Providers → Routing → Auth → Rules → Actions → Resilience → Observability → Connectors → Spool → Admin console → Deployment**. The local-dev and auxiliary-binaries pages are useful any time you're running things by hand.

Just want it running? The **[quickstart compose bundle](../deploy/quickstart/)** stands up the gateway (optionally with the admin console and/or the Arbiter) from the published images — set provider keys in `.env`, `docker compose up`, done.

In a hurry? The **[FAQ](faq.md)** is the task-oriented shortcut — routing a model, rewriting request/response bodies, Anthropic batches, Azure OpenAI, load-balancing, live rule edits — each answer grounded in a real config example.

---

## Map by audience

### Operators deploying and configuring the gateway

| Doc | What it covers |
|---|---|
| [Quickstart compose bundle](../deploy/quickstart/) | Turnkey `docker compose` stacks from the published images — gateway + console, gateway only, gateway + Arbiter — configured from `.env` |
| [Configuration model](configuration-model.md) | YAML loader (every `*.yaml` merged by top-level key), top-level blocks, configurations + bindings + api_keys |
| [Providers](providers.md) | `providers` block schema — base URL, per-protocol auth, passthrough families, the OpenAI-compat surface |
| [Routing](routing.md) | How inbound paths map to a protocol and select a target via bindings, passthrough matching, X-Sluice headers |
| [Authentication](auth.md) | Managed vs passthrough modes, header discovery, upstream credential resolution |
| [Rules](rules.md) | Rules engine + every condition type + behavior=continue/exit |
| [Actions](actions.md) | Every rule action — `changeProvider`, `setHeader`, `useResiliencePolicy`, `returnStatusCode`, ... |
| [Resilience policies](resilience.md) | Failover, load_balance (LBWF + strict_weights), circuit breaker, attempt records |
| [Observability](observability.md) | Every OTel meter, runtime + process collectors, structured logs, snapshotter |
| [Connectors](connectors.md) | The `connectors:` top-level block — s3 / azure_blob / webhook reference, per-type auth modes, key layout |
| [Connector bindings](connector-bindings.md) | Per-configuration sampling, filter, body cap, oversize behaviour |
| [Spool](spool.md) | Disk-backed buffer between OnComplete and the connector destinations — layout, lifecycle, loss policy |
| [Admin console](admin-console.md) | Enabling, password, every API route, every SPA page, live messages, body capture |
| [Environment variables](environment-variables.md) | Every `SLUICE_*` env var with default, type, validation, effect |
| [Deployment](deployment.md) | Topology, container image, K8s shape, multi-pod considerations, graceful drain |
| [Arbiter](arbiter.md) | The optional central `cmd/arbiter` service — two-listener topology, YAML config, deploy, shutdown, HMAC trust, invariant #4 compliance |
| [Arbiter API](arbiter-api.md) | Console query API — dashboard, messages, events, sessions, facets, keyset pagination |
| [Arbiter webhook](arbiter-webhook.md) | The Record ingest contract — `POST /api/v1/ingest/record`, headers, hex HMAC-SHA256, response codes |
| [Arbiter database schema](arbiter-database-schema.md) | TimescaleDB ERD, per-table columns/indexes, the single-writer `span_event` projection, the lazy `record` blob, the CAGG metrics plane, migration history |

### Developers running and testing locally

| Doc | What it covers |
|---|---|
| [Local development](local-development.md) | Every `make` target, dev compose, `config-dev/` bundle, the five test layers |
| [Auxiliary binaries](auxiliary-binaries.md) | `sluice-cli`, `sluice-mockllm` — flags, subcommands, when to use each |
| [Models](models.md) | The shared `models/` package — `DynamicProperties`, `PolymorphicRegistry`, unknown-field round-trip (invariant #1) |
| [Provider models](provider-models.md) | The `protocols/` wire types per provider — OpenAI audio/file/refusal/Responses, Anthropic thinking blocks + signatures, Gemini thinking + grounding |
| [Pipeline](pipeline.md) | The typed-message middleware pipeline — `Message` sum type, `Middleware`/`Chain`, body-capture + re-marshal stages, selection internals |
| [GenAI conformance audit](genai-conformance-audit.md) | Per-protocol wire-conformance audit of the gen_ai span + content extraction against the OTel GenAI semconv |

---

## Map by surface

### "I want to ..."

| Goal | Start here |
|---|---|
| Rewrite a field in the request or response body | [FAQ → Rewrite a request field](faq.md#how-do-i-rewrite-a-field-in-the-request-body), [FAQ → Rewrite the response body](faq.md#how-do-i-rewrite-the-response-body) |
| Proxy Anthropic message batches | [FAQ → Passthrough surfaces](faq.md#how-do-i-support-anthropic-message-batches-and-other-passthrough-surfaces) |
| Add Azure OpenAI as a provider | [FAQ → Azure OpenAI](faq.md#how-do-i-add-azure-openai-as-a-provider) |
| Run Sluice from the published images in minutes | [Quickstart compose bundle](../deploy/quickstart/) |
| Run the gateway alongside the Arbiter | [Quickstart → gateway + Arbiter](../deploy/quickstart/#2-run-a-stack), [Arbiter](arbiter.md) |
| Boot a gateway against a local mock LLM | [Local development → Quickest path](local-development.md#quickest-path-to-a-running-gateway) |
| Wire a new upstream provider | [Providers → Schema](providers.md#yaml-schema), [Configuration model → providers block](configuration-model.md#providers-block) |
| Route requests to a different provider by model name | [Rules → modelName condition](rules.md#modelname), [Actions → changeProvider](actions.md#changeprovider) |
| Set up failover between two providers | [Resilience → 30-second failover](resilience.md#30-second-failover) |
| Load-balance across two providers | [Resilience → 30-second weighted load-balance](resilience.md#30-second-weighted-load-balance) |
| Trip the circuit breaker on a noisy provider | [Resilience → Tripping the breaker](resilience.md#tripping-the-breaker) |
| Enable the admin console | [Admin console → Enabling the console](admin-console.md#enabling-the-console), [Admin console → Setting the password](admin-console.md#setting-the-password) |
| Generate an API key | [Auxiliary binaries → `sluice-cli` → `key new`](auxiliary-binaries.md#key-new) |
| Validate a YAML bundle before deploy | [Auxiliary binaries → `sluice-cli` → `config validate`](auxiliary-binaries.md#config-validate) |
| Run the smoke suite against a live deploy | [Local development → Testing layers → Smoke](local-development.md#testing-layers), [Deployment → Smoke tests](deployment.md#smoke-tests-against-a-live-deploy) |
| Add a tag to every matching request | [Actions → `addTag`](actions.md#addtag), [Rules → `tag` condition](rules.md#tag) |
| Use a client's own upstream token (passthrough) | [Authentication → Passthrough mode](auth.md#passthrough-mode) |
| Add OTLP push to a Honeycomb / Tempo endpoint | [Environment variables → Observability](environment-variables.md#observability--otlp-prometheus-logging), [Observability → OTel pipeline](observability.md#otel-pipeline) |
| Capture request and response bodies for one event | [Admin console → Body capture](admin-console.md#body-capture), [Environment variables → Live feed + body capture](environment-variables.md#live-feed-and-body-capture) |
| Ship every request to an S3 bucket | [Connectors → s3 connector](connectors.md#s3-connector), [Connector bindings → Worked examples](connector-bindings.md#worked-examples) |
| Pipe a 5% sample of errors to a webhook | [Connectors → webhook connector](connectors.md#webhook-connector), [Connector bindings → Sampling](connector-bindings.md#sampling) |
| Understand where records sit when a destination is down | [Spool → Lifecycle](spool.md#lifecycle), [Spool → Loss policy](spool.md#loss-policy) |
| Ship every request record to an Arbiter | [Arbiter](arbiter.md), [Arbiter webhook → Record ingest](arbiter-webhook.md), [Connectors → webhook connector](connectors.md#webhook-connector) |
| Query captured requests across gateways in one console | [Arbiter API](arbiter-api.md), [Arbiter database schema](arbiter-database-schema.md) |

---

## Conventions used across the docs

- **YAML examples show literal values.** The loader does **not** expand `${VAR}` placeholders — see [Configuration model → Why no `${VAR}` substitution](configuration-model.md#why-no-var-substitution). Substitute via your secret manager before the file gets mounted.
- **Diagrams are mermaid.** No ASCII art. Renders natively on GitHub.
- **Anchors are lowercase-hyphen.** `[Section name](#section-name)` works for every numbered section in every doc.
- **Code pointers cite paths.** When a doc references a Go symbol, it's followed by the file path in `contracts/` or `internal/` so you can `grep` for the canonical implementation.
- **Defaults and validation come from `internal/config/env.go` and the `contracts/*/validate.go` files.** If a doc contradicts code, the code is right — open an issue or PR against the doc.

---

## Where the source-of-truth design notes live

The repo docs cover **what is** — the implemented surface. The longer-form **why** for milestone-level design decisions (the v1.0/v1.1/v1.2 milestone plans, the pipeline + middleware design, the .NET-to-Go translation table, the OpenAI-compat quirks note) lives in DonkeyWork project `522d9204-c3b6-4719-b0c9-8ef91b968314`. See [`CLAUDE.md`](../CLAUDE.md) at the repo root for the index of those notes.
