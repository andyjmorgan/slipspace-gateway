# sluice-gateway documentation

Operator and developer reference for sluice-gateway. Each page is self-contained, with cross-references where one surface depends on another.

If you're new to the project, the suggested reading order is **Configuration → Providers → Routing → Auth → Rules → Actions → Resilience → Observability → Connectors → Spool → Admin console → Deployment**. The local-dev and auxiliary-binaries pages are useful any time you're running things by hand.

In a hurry? The **[FAQ](faq.md)** is the task-oriented shortcut — routing a model, rewriting request/response bodies, Anthropic batches, Azure OpenAI, load-balancing, live rule edits — each answer grounded in a real config example.

---

## Map by audience

### Operators deploying and configuring the gateway

| Doc | What it covers |
|---|---|
| [Configuration model](configuration-model.md) | YAML loader, file allow-list, top-level keys, configurations + api_keys binding |
| [Providers](providers.md) | `providers.yaml` schema, endpoints, per-endpoint auth overrides, the OpenAI-compat surface |
| [Routing](routing.md) | How inbound paths get matched to a (provider, endpoint), prefix rules, X-Sluice headers |
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

### Developers running and testing locally

| Doc | What it covers |
|---|---|
| [Local development](local-development.md) | Every `make` target, dev compose, `config-dev/` bundle, the five test layers |
| [Auxiliary binaries](auxiliary-binaries.md) | `sluice-cli`, `sluice-mockllm`, `sluice-api` — flags, subcommands, when to use each |

---

## Map by surface

### "I want to ..."

| Goal | Start here |
|---|---|
| Rewrite a field in the request or response body | [FAQ → Rewrite a request field](faq.md#how-do-i-rewrite-a-field-in-the-request-body), [FAQ → Rewrite the response body](faq.md#how-do-i-rewrite-the-response-body) |
| Proxy Anthropic message batches | [FAQ → Passthrough surfaces](faq.md#how-do-i-support-anthropic-message-batches-and-other-passthrough-surfaces) |
| Add Azure OpenAI as a backend | [FAQ → Azure OpenAI](faq.md#how-do-i-add-azure-openai-as-a-backend) |
| Boot a gateway against a local mock LLM | [Local development → Quickest path](local-development.md#quickest-path-to-a-running-gateway) |
| Wire a new upstream provider | [Providers → Schema](providers.md#yaml-schema), [Configuration model → providers block](configuration-model.md#providers-block) |
| Route requests to a different provider by model name | [Rules → modelName condition](rules.md#modelname), [Actions → changeProvider](actions.md#changeprovider) |
| Set up failover between two providers | [Resilience → 30-second failover](resilience.md#30-second-failover) |
| Load-balance across two backends | [Resilience → 30-second weighted load-balance](resilience.md#30-second-weighted-load-balance) |
| Trip the circuit breaker on a noisy backend | [Resilience → Tripping the breaker](resilience.md#tripping-the-breaker) |
| Enable the admin console | [Admin console → Enabling the console](admin-console.md#enabling-the-console), [Admin console → Setting the password](admin-console.md#setting-the-password) |
| Generate an API key | [Auxiliary binaries → `sluice-cli` → `key new`](auxiliary-binaries.md#key-new) |
| Validate a YAML bundle before deploy | [Auxiliary binaries → `sluice-cli` → `config validate`](auxiliary-binaries.md#config-validate) |
| Run the smoke suite against a live deploy | [Local development → Testing layers → Smoke](local-development.md#testing-layers), [Deployment → Smoke tests](deployment.md#smoke-tests-against-a-live-deploy) |
| Add a tag to every matching request | [Actions → `addTag`](actions.md#addtag), [Rules → `tag` condition](rules.md#tag) |
| Use a client's own upstream token (passthrough) | [Authentication → Passthrough mode](auth.md#passthrough-mode) |
| Add OTLP push to a Honeycomb / Tempo endpoint | [Environment variables → Observability](environment-variables.md#observability), [Observability → OTel pipeline](observability.md#otel-pipeline) |
| Capture request and response bodies for one event | [Admin console → Body capture](admin-console.md#body-capture), [Environment variables → Live feed + body capture](environment-variables.md#live-feed--body-capture) |
| Ship every request to an S3 bucket | [Connectors → s3 connector](connectors.md#s3-connector), [Connector bindings → Worked examples](connector-bindings.md#worked-examples) |
| Pipe a 5% sample of errors to a webhook | [Connectors → webhook connector](connectors.md#webhook-connector), [Connector bindings → Sampling](connector-bindings.md#sampling) |
| Understand where records sit when a destination is down | [Spool → Lifecycle](spool.md#lifecycle), [Spool → Loss policy](spool.md#loss-policy) |

---

## Conventions used across the docs

- **YAML examples show literal values.** The loader does **not** expand `${VAR}` placeholders — see [Configuration model → Why no `${VAR}` substitution](configuration-model.md#why-no-var-substitution). Substitute via your secret manager before the file gets mounted.
- **Diagrams are mermaid.** No ASCII art. Renders natively on GitHub.
- **Anchors are lowercase-hyphen.** `[Section name](#section-name)` works for every numbered section in every doc.
- **Code pointers cite paths.** When a doc references a Go symbol, it's followed by the file path in `contracts/` or `internal/` so you can `grep` for the canonical implementation.
- **Defaults and validation come from `internal/config/env.go` and the `contracts/*/validate.go` files.** If a doc contradicts code, the code is right — open an issue or PR against the doc.

---

## Where the source-of-truth design notes live

The repo docs cover **what is** — the implemented surface. The longer-form **why** for milestone-level design decisions (the v1.0/v1.1/v1.2 milestone plans, the pipeline + middleware design, the .NET-to-Go translation table, the OpenAI-compat quirks note) lives in DonkeyWork project `793a6cba-bd53-4b7e-8913-20fee7cb5f87`. See [`CLAUDE.md`](../CLAUDE.md) at the repo root for the index of those notes.
