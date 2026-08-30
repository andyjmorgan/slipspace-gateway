# Configuration Model

Everything the gateway needs to serve traffic lives in YAML files under `SLIPSPACE_CONFIG_DIR` (default `/etc/slipspace/`). The loader reads **every** `*.yaml` file in that directory, merges the top-level blocks by key into a single `ResolvedConfig`, validates cross-block references, then builds the runtime indexes the data plane reads on every request.

This is the **v2** configuration model. Routing defaults to config data: the inbound request path fixes a *protocol*, the request body carries a *model*, and a configuration's **bindings** map `(protocol, model)` to a provider or a resilience group. A rule can still override the upstream credential per-request with `changeApiKey`, but the three legacy routing actions — `changeProvider`, `changeUrl` and `useResiliencePolicy` — are **inert** in v2: they parse and validate, but the data plane never honours the state they write (see [actions.md](actions.md#changeurl)). A rule-authored `changeProvider` is overwritten on every attempt by `buildAttemptState` (`internal/middleware/resilience/middleware.go:702-718`) re-applying the selected target's own `providerSwitchActions` (`cmd/gateway/destination.go:66-76`); `ChangeProviderAction` survives only as that internal per-attempt primitive. Model-keyed redirect is expressed as a binding, not a rule. Route to a different provider, URL or resilience group with a binding edit, not a rule. There is no path-based route table, no provider `endpoints`/`accepted_paths`/`prefix*` schema, and no top-level `resilience_policies` block — those were v1 concepts retired into bindings, groups, and provider `protocols`. See `internal/config/config_model.go` (the loader) and `contracts/config/model.go` (the schema) for the source of truth.

This page is the operator's reference for that on-disk schema — what files exist, which top-level keys may appear, every field on every type, how the pieces bind together, and what's deliberately out of scope.

---

## Table of contents

1. [Mental model](#mental-model)
2. [File discovery and merging](#file-discovery-and-merging)
3. [Top-level keys](#top-level-keys)
4. [`providers` block](#providers-block)
5. [`groups` block](#groups-block)
6. [`configurations` block](#configurations-block)
7. [`bindings` (inside a configuration)](#bindings-inside-a-configuration)
8. [Passthrough families and bindings](#passthrough-families-and-bindings)
9. [`api_keys` block](#api_keys-block)
10. [`rules` block](#rules-block)
11. [`connectors` block](#connectors-block)
12. [`admin` block](#admin-block)
13. [`telemetry` block](#telemetry-block)
14. [`pricing` block](#pricing-block)
15. [Protocol resolution](#protocol-resolution)
16. [The binding triangle](#the-binding-triangle)
17. [Worked examples](#worked-examples)
18. [Why no `${VAR}` substitution](#why-no-var-substitution)
19. [Validation errors](#validation-errors)
20. [Known limitations](#known-limitations)

---

## Mental model

> **Two shared catalogues, reusable policy bundles, per-client bearer references — and routing carried as data inside the bundles.**

- `providers` is the **connection catalogue** — what upstreams exist, where they live, which protocols they speak (each with its own path + auth), and any opaque passthrough families they expose. A provider holds **no credential** — it is a connection, not a secret.
- `groups` is the **resilience catalogue** — named failover / load-balance units, each an ordered or weighted set of provider targets with a failure policy and circuit breaker. Groups replace v1 `resilience_policies`; a binding references a group instead of a rule firing `useResiliencePolicy`.
- `configurations` are the **policy bundles** — each holds the upstream **credentials** for the providers it uses, the **bindings** that route `(protocol, model)` to a provider or group, optional **passthrough bindings**, a list of transform **rule names**, tags, and connector bindings.
- `api_keys` is the **bearer reference** — a flat list of gateway-issued secrets, each naming the `configuration` it resolves to. Many keys may share one configuration.
- `admin` / `telemetry` are the **management-console gate** and **telemetry knobs** — both optional.

Server-level configuration (bind address, drain timeout, spool root, OTel exporter, Prometheus scrape, log format/level) is **not** in YAML — it lives on the `SLIPSPACE_*` env vars consumed by `LoadEnv`. See [environment-variables.md](environment-variables.md).

The blocks compose by reference, not by nesting: providers and groups are defined once at the top level and referenced by name from a configuration's bindings.

---

## File discovery and merging

```mermaid
flowchart TB
    A[SLIPSPACE_CONFIG_DIR] --> B[os.ReadDir]
    B --> C{regular file<br/>with .yaml ext?}
    C -- no --> S[skip silently]
    C -- yes --> D[yaml.Unmarshal<br/>into configDoc]
    D --> L{backends: key<br/>present?}
    L -- yes --> X[ErrLegacyProvidersKey]
    L -- no --> E[mergeDoc:<br/>claim each block by key]
    E --> F{block already<br/>claimed by<br/>another file?}
    F -- yes --> Y[ErrDuplicateKey]
    F -- no --> G[fold block into<br/>ResolvedConfig]
    G --> H[Validate]
    H --> I[buildIndexes]
    I --> J[ready to serve]
```

### Rules

1. **Directory must exist and be readable.** `Load` fails on the `os.ReadDir` error.
2. **Subdirectories and non-`.yaml` entries are skipped silently.** Only regular files whose extension is `.yaml` are considered (`internal/config/config_model.go::Load`).
3. **No fixed-filename allowlist.** Under v2 the loader merges **any** `*.yaml` filename — there is no pinned set of `providers.yaml` / `policy.yaml` / `admin.yaml`. Operators may split blocks across files however they like, or put everything in one file. The filenames in this doc's examples are a recommended convention, not a requirement.
4. **Directory must contain at least one `.yaml` file.** Zero accepted files raises `ErrEmptyDirectory`.
5. **Each top-level block has a single authoring home.** A block (`providers`, `groups`, `configurations`, `api_keys`, `connectors`, `rules`, `advisors`, `admin`, `telemetry`, `pricing`) set by **two** files aborts the load with `ErrDuplicateKey`, naming both files. This is enforced in `mergeDoc` by a `seen` map of block → filename. A given block may live in any file, but only one.
6. **Files are processed in alphabetical filename order.** Ordering is deterministic but irrelevant to the result — the single-authoring-home rule means no block is ever set twice, so there are no last-writer-wins merge semantics.
7. **The legacy `backends:` key is rejected hard.** The block was renamed `providers:` in the Vocabulary Refactor with no back-compat alias; a file carrying `backends:` aborts with `ErrLegacyProvidersKey` rather than silently ignoring it (`internal/config/config_model.go::Load`).
8. **Trusted contents.** The loader performs no `${VAR}` expansion and no `env:` substitution — decoded values pass through verbatim. See [Why no `${VAR}` substitution](#why-no-var-substitution).

### Loader override

`SLIPSPACE_CONFIG_DIR` selects the directory. The CLI's `--dir` flag overrides this for one-shot validation runs but does not affect the data-plane process.

---

## Top-level keys

The loader recognises **ten** top-level keys (`internal/config/loader.go:13-24`, `keyProviders`…`keyAdvisors`). Any of them may appear in any `*.yaml` file, subject to the single-authoring-home rule above.

| Key | Carries |
|---|---|
| `providers` | Connection catalogue: base URL, required headers, default query, the `protocols` map (per-protocol path + auth), and opaque `passthrough` families. |
| `groups` | Resilience-group catalogue. Each group is an ordered/weighted set of provider targets with a mode, failure policy, and circuit breaker. Bindings reference a group by name. Replaces v1 `resilience_policies`. |
| `configurations` | Named policy bundles. Each holds `credentials`, `bindings`, optional `passthrough_bindings`, `rule_names`, `tags`, and `connector_bindings`. |
| `api_keys` | Flat list of gateway-issued bearers; each points at a configuration by name. |
| `rules` | Top-level rule library (body/header/query rewrites, tags, short-circuits, and the one wired steering action `changeApiKey`). Bindings carry the routing as config data; `changeProvider`, `changeUrl` and `useResiliencePolicy` are **inert** in v2 (they parse but the data plane ignores them). Pure body/header/query transforms are the common case. Configurations reference rules through `rule_names`. |
| `connectors` | Top-level connector destinations (s3, azure_blob, webhook). Configurations attach them through `connector_bindings`. |
| `admin` | Management-console gate. Optional; absent means the console never starts. |
| `telemetry` | Operator-tunable telemetry knobs (today: GenAI content-capture byte caps). Optional; absent resolves to built-in defaults. |
| `pricing` | Per-request USD cost estimation: rate-card overrides over the embedded defaults. Optional; absent means costing off. |
| `advisors` | Agent-aware routing (added v2.3.0): advisor configuration for classifying agents and pinning models per conversation. Optional; absent means agent-aware routing off. |

The pre-rename `backends:` key is rejected with `ErrLegacyProvidersKey`. Any other unrecognised top-level key is silently ignored by the YAML decoder (the `configDoc` struct only has fields for the ten blocks above plus the `backends` trap).

---

## `providers` block

`providers:` is a map from provider name to its connection definition (`contractsconfig.ProvidersConfig`, `contracts/config/model.go:43`). A provider is a **connection** — base URL, transport quirks, protocol-to-path map, auth scheme — not a credential holder. The credential is supplied per provider by the binding configuration's `credentials` map and substituted into the protocol auth format's `{key}` placeholder. See [providers.md](providers.md) for the per-provider deep dive; this section documents the schema only.

```yaml
providers:
  openai:
    base_url: "https://api.openai.com"
    required_headers:
      x-slipspace-source: "gateway"
    query:
      api-version: "preview"
    protocols:
      chat:
        path: "/v1/chat/completions"
        auth:
          header: "Authorization"
          format: "Bearer {key}"
      responses:
        path: "/v1/responses"
        auth:
          header: "Authorization"
          format: "Bearer {key}"
```

### `Provider` fields (`contracts/config/model.go:50`)

| Field | Type | Required | Notes |
|---|---|---|---|
| `base_url` | string | yes | Upstream root URL (e.g. `https://api.openai.com`). A protocol's `path` is appended to this when forwarding. Empty aborts validation. |
| `required_headers` | map[string]string | no | Headers injected on every forwarded request to this provider (e.g. `anthropic-version: 2023-06-01`). |
| `query` | map[string]string | no | Default query-string params appended to every request to this provider (e.g. Azure's `api-version`). A binding or group target may add or override entries; the effective set is provider ∪ override, override winning. |
| `protocols` | map[string]ProviderProtocol | no | Generative wire shapes this provider serves, keyed by protocol name (see [Protocol resolution](#protocol-resolution)). A provider must declare **at least one** `protocols` entry **or** one `passthrough` family, else validation fails. |
| `passthrough` | map[string]PassthroughFamily | no | Opaque/stateful endpoint families this provider exposes (e.g. Anthropic message batches), keyed by family name. Proxied verbatim — never typed, never GenAI-telemetried. "Verbatim" governs forwarding, not audit: raw bodies **are** still captured into connector Records (under `bodycapture.KindPassthrough`) when the resolved configuration has `connector_bindings`. See [Passthrough families and bindings](#passthrough-families-and-bindings). |

### `ProviderProtocol` fields (`contracts/config/model.go:84`)

| Field | Type | Required | Notes |
|---|---|---|---|
| `path` | string | no | Upstream path template appended to `base_url`. May contain `{name}` placeholders the gateway substitutes (e.g. Gemini's `/v1beta/models/{model}:{op}`). |
| `auth` | ProviderAuth | no | Per-protocol credential override. Absent (`nil`) defers to the provider-native default for the wire shape. This is what lets a provider's OpenAI-compat `chat` surface authenticate with `Authorization: Bearer` while its native protocol uses a different header (invariant #6). |

### `ProviderAuth` fields (`contracts/config/model.go:101`)

| Field | Type | Required | Notes |
|---|---|---|---|
| `header` | string | yes (when `auth` is set) | Outgoing HTTP header the credential is injected into (e.g. `Authorization`, `x-api-key`, `x-goog-api-key`, `api-key`). |
| `format` | string | no | Templates the header value with exactly one `{key}` placeholder, substituted with the resolved upstream credential (e.g. `Bearer {key}` or `{key}`). Empty renders the raw credential. |

### Auth validation

`validateAuth` (`internal/config/config_validate.go:115`) enforces, per `auth` block on a protocol or passthrough family:

- A non-empty `format` requires a non-empty `header` (`ErrAuthFormatWithoutHeader`) — a format with no header would be silently ignored.
- A non-empty `format` must contain `{key}` **exactly once** (`ErrInvalidAuthFormat`).

There is no provider-level `auth_header` / `auth_format` in v2: auth is set per protocol (or per passthrough family), and the effective credential header is minted once at the single mint site in the forwarder from `ProviderAuth` (or the provider-native default when `auth` is `nil`). This preserves invariant #6 — one credential-header format per `(provider, protocol)`.

---

## `groups` block

`groups:` is a map from group name to a resilience definition (`contractsconfig.GroupsConfig`, `contracts/config/model.go:142`). A group is a named failover / load-balance unit the orchestrator routes across. Targets are arbitrary providers, so a group cuts across providers — the only constraint is **protocol-preserving**: every target must serve the binding's protocol (no mid-failover translation). Groups replace v1 `resilience_policies`; a binding references a group through `Binding.Group` instead of a configuration carrying a single global `resilience_name`.

```yaml
groups:
  chat-ha:
    mode: failover
    failure_status_codes: [502, 503, 504]
    circuit_breaker:
      enabled: true
      failure_threshold: 5
      sampling_duration_seconds: 30
      cooldown_seconds: 15
      half_open_success_threshold: 2
    targets:
      - provider: openai
      - provider: anthropic
        alias: claude-sonnet-4-5
```

### `Group` fields (`contracts/config/model.go:149`)

| Field | Type | Required | Notes |
|---|---|---|---|
| `mode` | resilience.ResilienceMode | yes | Orchestration strategy: `failover`, `load_balance`, `load_balance_with_failover`, or `none` (`contracts/resilience/types.go:15`, values at `:23-29`). |
| `failure_status_codes` | []int | no | Upstream HTTP status set treated as a failure for retry / circuit-breaker accounting. Empty falls back to "5xx is a failure". |
| `circuit_breaker` | *CircuitBreakerConfig | no | Group-wide breaker. State is tracked per `(group, provider)` pair — the breaker key is `group-name|provider-name` — so a provider tripped in one group is isolated to that group and is not automatically skipped by other groups that include the same provider. Fields: `enabled`, `failure_threshold`, `failure_rate_threshold`, `sampling_duration_seconds`, `cooldown_seconds`, `half_open_success_threshold`, `minimum_throughput` (`contracts/resilience/types.go:160`). |
| `strict_weights` | bool | no | In `load_balance` mode, makes the first weighted-random pick final — no re-roll onto another target on a retryable failure. Used for canary mirroring where the under-weighted target's failures must surface to the client. Ignored in `failover` mode. |
| `response_header_timeout_seconds` | int | no | When `> 0`, overrides the gateway-wide upstream response-header timeout for every attempt under this group, so a group can fail over off a slow target faster than the default. |
| `targets` | []Target | yes | The providers this group routes across. Must have at least one (`internal/config/config_validate.go:131`). |

### `Target` fields (`contracts/config/model.go:189`)

The atom a binding or group dispatches to: a provider reference plus per-use overrides that compose over the provider's own values (target wins).

| Field | Type | Required | Notes |
|---|---|---|---|
| `provider` | string | yes | Names a provider in the top-level `providers` block. Unknown provider aborts validation. |
| `alias` | string | no | When non-empty, rewrites the request body's model field to this value when this target is selected (the model-name rewrite — placing it on the target lets a group send one logical model under a different upstream id per provider). |
| `query` | map[string]string | no | Adds or overrides query-string params for this target, composed over the provider's `query`. |
| `path` | string | no | Overrides the protocol path for this target (e.g. an Azure deployment-specific path on a shared provider connection). |
| `weight` | int | no | Relative selection weight in `load_balance` mode. Zero is treated as 1 (even weighting); ignored in `failover` mode, where declaration order drives sequencing. |

Validation: a group must declare at least one target, every target must name a `provider`, and that provider must exist (`internal/config/config_validate.go::validateGroups`, provider-existence check at `:139-141` — `if _, ok := r.Providers[t.Provider]; !ok`). The protocol-preserving check happens at the **binding** level — when a binding references a group, every target in that group must serve the binding's protocol (`validateBindings`, `config_validate.go:285`).

---

## `configurations` block

`configurations:` is a map from configuration name to a reusable policy bundle (`contracts/config/model.go:273`). There must be **at least one** entry — an empty map aborts with `ErrNoConfigurations`.

```yaml
configurations:
  production:
    credentials:
      openai: sk-mock-openai
      anthropic: sk-ant-mock-anthropic
    bindings:
      - protocol: chat
        models: ["gpt-*"]
        provider: openai
      - protocol: messages
        provider: anthropic
      - protocol: chat
        group: chat-ha
    rule_names:
      - strip-internal-headers
    tags:
      tier: production
```

### `Configuration` fields (`contracts/config/model.go:273`)

| Field | Type | Required | Notes |
|---|---|---|---|
| `credentials` | map[string]string | no | Provider name → upstream credential this configuration holds for it; `{key}` resolves from here. An **empty-string** value means a no-credential provider (strip the credential and forward — useful for ollama-style upstreams). A provider referenced by a binding **must** have an entry here (even if empty); this is **not** checked at load — selection fails at request time with `selection: configuration holds no credential entry for provider <p>`. A credential naming a provider absent from `providers` aborts at load with `ErrValidation` (`configuration <name> credentials reference unknown provider <p>`, `config_validate.go:223`) — not `ErrUnknownConfiguration`, which is reserved for an api_key naming an unknown configuration. |
| `bindings` | []Binding | no | The generative routing table: `(protocol, model) → provider or group`. Evaluated in order; first match wins. See [`bindings`](#bindings-inside-a-configuration). |
| `passthrough_bindings` | []PassthroughBinding | no | Exposes opaque endpoint families on this configuration. See [Passthrough families and bindings](#passthrough-families-and-bindings). |
| `rule_names` | []string | no | Names of **transform** rules from the top-level `rules:` library this configuration applies (body/header/query rewrites, tags, short-circuits — not routing). Unknown names abort load with `ErrUnknownRuleName`. Evaluation order = list order. |
| `tags` | map[string]string | no | Static configuration-level labels propagated to telemetry for every request under this configuration. **Not** the same channel as rule-attached request tags or binding tags — `configurations[].tags` does not bump `gateway.tags.applied.total`. Use this for static metadata (`tier: production`); use [`addTag`](actions.md#addtag) rule actions or [`Binding.tags`](#bindings-inside-a-configuration) for per-request labels. |
| `connector_bindings` | []ConnectorBinding | no | Attaches one or more top-level connectors with per-binding sampling / filter / size-cap overrides. Empty (or absent) means no records are captured — the body-capture middleware short-circuits when no bindings exist. See [connector-bindings.md](connector-bindings.md). |
| `agent_routing` | *AgentRouting | no | Agent-aware routing (added v2.3.0): per-configuration overrides for classifying agents and pinning models per conversation. |

---

## `bindings` (inside a configuration)

A **binding** is the router expressed as config data: it maps a generative `(protocol, model)` pair to a destination — a single provider or a resilience group (`contracts/config/model.go:218`, doc comment starts `:212`). Selection is `(protocol-from-path, model-from-body) → first matching binding` (`internal/selection/selection.go::Select`).

```yaml
bindings:
  # exact / prefix model match → single provider
  - protocol: chat
    models: ["gpt-4o", "gpt-4o-mini"]
    provider: openai
    alias: gpt-4o-2024-08-06        # rewrite the body model on the way out
  # catch-all for a protocol (empty models) → resilience group
  - protocol: messages
    group: anthropic-ha
    tags: ["surface:messages"]
```

### `Binding` fields (`contracts/config/model.go:218`)

| Field | Type | Required | Notes |
|---|---|---|---|
| `protocol` | string | yes | The generative protocol this binding serves — one of the protocol constants (see [Protocol resolution](#protocol-resolution)). Unknown protocol aborts validation. |
| `models` | []string | no | Client-requested model patterns this binding matches. Exact string, or a single **trailing-`*`** prefix wildcard (interior or multiple `*` is rejected). An **empty** model set is a **catch-all** for the protocol (default-permissive, invariant #1) — never a default-deny. |
| `provider` | string | conditionally | Names the single destination provider. **Mutually exclusive** with `group` — exactly one of the two must be set (`internal/config/config_validate.go:264-266`, `validateBindings`). |
| `group` | string | conditionally | Names a resilience group destination. Mutually exclusive with `provider`. |
| `alias` | string | no | Rewrites the request body model name for the **single-provider** case (sugar for the binding's implicit target alias). **Ignored when `group` is set** — group targets carry their own aliases. |
| `query` | map[string]string | no | Single-provider per-use query override. Ignored when `group` is set. |
| `path` | string | no | Single-provider per-use protocol-path override. Ignored when `group` is set. |
| `tags` | []string | no | Labels applied to a request matching this binding, propagated to telemetry (carries forward the v1 `addTag`-on-route pattern). |

### Matching rules

`matchesModelPatterns` (`internal/selection/selection.go:261`):

- **Empty `models`** matches every model on the protocol (catch-all).
- A pattern ending in `*` is a **prefix** match (`gpt-*` matches `gpt-4o`).
- Otherwise the pattern is compared **exactly**.

`Select` walks the configuration's bindings in order and returns the **first** binding whose `protocol` equals the request protocol and whose `models` match the request model. No fallthrough: when no binding matches, selection returns `ErrNoBinding`, which the data plane maps to a 404 with error code `no_binding` (`cmd/gateway/pipeline.go:172-173`) — the model is simply not served on that protocol by this configuration.

### Binding validation (`internal/config/config_validate.go::validateBindings`)

- `protocol` must be a known protocol.
- Exactly one of `provider` / `group` must be set.
- For a single provider: the provider must exist **and serve the binding's protocol**.
- For a group: the group must exist, and **every** target in it must serve the binding's protocol (protocol-preserving — no mid-failover translation).
- Model patterns must be well-formed (single trailing `*`).
- Within one protocol, at most **one** catch-all binding, and no **duplicate exact** model. (Prefix-overlap collision detection is deferred — two overlapping `gpt-*` / `gpt-4*` prefixes are allowed and resolved by order.)

---

## Passthrough families and bindings

Passthrough is the v2 mechanism for **opaque, stateful** endpoint families that are not model-keyed and not GenAI-shaped — e.g. Anthropic's message-batches surface across create / get / results / cancel. Passthrough requests are proxied **verbatim** with rewrites only: no typed parsing, no GenAI telemetry (`internal/selection/selection.go::MatchPassthrough`). Raw request/response bodies **are** still captured into connector Records when the resolved configuration has `connector_bindings` — body-capture buffers them under `KindPassthrough`; only an unbound configuration skips capture. "Verbatim" governs forwarding, not audit — see `contracts/config/model.go:72-80`.

A family is declared on a **provider** and exposed on a **configuration**:

```yaml
providers:
  anthropic:
    base_url: "https://api.anthropic.com"
    required_headers:
      anthropic-version: "2023-06-01"
    protocols:
      messages: { path: "/v1/messages", auth: { header: x-api-key, format: "{key}" } }
    passthrough:
      message-batches:
        auth: { header: x-api-key, format: "{key}" }
        paths:
          - match: "/v1/messages/batches"
            methods: ["POST", "GET"]
          - match: "/v1/messages/batches/{id}/results"
            methods: ["GET"]

configurations:
  production:
    credentials:
      anthropic: sk-ant-mock-anthropic
    passthrough_bindings:
      - provider: anthropic
        family: message-batches
        tags: ["surface:batches"]
```

### `PassthroughFamily` fields (`contracts/config/model.go:118`)

| Field | Type | Required | Notes |
|---|---|---|---|
| `auth` | ProviderAuth | no | Credential convention for this family; `nil` defers to the provider-native default. Validated by the same `validateAuth` rules as protocol auth. |
| `paths` | []PassthroughPath | yes | Inbound path patterns this family claims; must declare at least one. |

### `PassthroughPath` fields (`contracts/config/model.go:130`)

| Field | Type | Required | Notes |
|---|---|---|---|
| `match` | string | yes | Inbound path pattern, optionally containing `{name}` placeholders (e.g. `/v1/messages/batches/{id}/results`). Captured params are surfaced to the forwarder; a pattern with no placeholders is an exact-string compare. |
| `methods` | []string | yes | HTTP methods this path accepts. Matched case-insensitively. A claimed path with an unaccepted method yields `ErrMethodNotAllowed`. |

### `PassthroughBinding` fields (`contracts/config/model.go:257`)

| Field | Type | Required | Notes |
|---|---|---|---|
| `provider` | string | yes | Provider whose passthrough family is exposed. Must exist. |
| `family` | string | yes | Name of a passthrough family declared on that provider. Must exist on the provider (`config_validate.go:232-234`, inside `validateConfigurations`). |
| `tags` | []string | no | Labels applied to requests matching this family. |

Selection of a passthrough request walks the configuration's `passthrough_bindings`, matching the path against each exposed family's `paths` and the method against that path's `methods` (`MatchPassthrough`). `ErrNoPassthrough` when no family claims the path; `ErrMethodNotAllowed` when one claims the path but not the method.

---

## `api_keys` block

`api_keys:` is a flat list (slice) of gateway-issued keys (`contractsconfig.APIKeysConfig`, `contracts/config/api_keys.go`). Each entry references a configuration by name. Many keys may share one configuration.

```yaml
api_keys:
  - secret: sk_live_alpha_abc123
    name: "internal-dev pipeline"
    configuration: production
    enabled: true
  - secret: sk_live_beta_def456
    name: "smoke-test rig"
    configuration: production
    enabled: true
```

### `APIKey` fields (`contracts/config/api_keys.go`)

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | *uuid.UUID | no | Stable identifier the admin write API addresses the key by. `nil` is allowed in operator-authored YAML; the admin API mints one on create. Duplicate non-nil IDs abort validation. |
| `secret` | string | yes | The bearer token clients present. Conventionally prefixed `sk_live_…` / `sk_dev_…`, but the loader does not enforce a prefix. Empty aborts validation; duplicate secrets abort. Authentication resolves this via an O(1) hash-map lookup in `ResolvedConfig.SecretIndex` (`internal/middleware/auth/resolver.go`), not a constant-time comparison. |
| `name` | string | no | Human-readable label surfaced in logs and reporting events. Unvalidated — carries no auth meaning. |
| `configuration` | string | yes | Name of the configuration this key resolves to. Unknown name aborts load with `ErrUnknownConfiguration`. |
| `enabled` | bool | yes | Toggles the key without removing it. A disabled key fails authentication (the resolver returns `ErrUnauthorized`) — it is rejected at the auth layer, deliberately with the same unauthorized response as an unknown secret so configuration names cannot be probed. |

Lookups use `SecretIndex` (built post-validate); the slice exists for enumeration in the admin console and audit reporting.

---

## `rules` block

`rules:` is the top-level rule library, a flat list of `RuleContract` entries (`contracts/rules`). In v2 bindings carry the routing; a rule can still override the upstream credential per-request with `changeApiKey` (the one wired steering action, honoured at the single mint site `resolveCredentialHeaders`), but the legacy `changeProvider`, `changeUrl` and `useResiliencePolicy` actions are **inert** (they parse but the data plane ignores the state they write — see [actions.md](actions.md#changeurl)). A rule can also apply body / header / query rewrites, tags, and short-circuits. Definitions are unique by `name`; configurations reference them through `Configuration.RuleNames`. See [rules.md](rules.md) for the condition/action grammar.

Each rule must:

- Have a unique `name` across the library (`ErrDuplicateRuleName`, `internal/config/config_validate.go:155`, enforced in `validateLibraries`).
- Pass `RuleContract.Validate()` — the per-rule semantic checks.

> **Note:** v2 validation does **not** check rule `id` uniqueness (the `ErrDuplicateRuleID` sentinel is defined but no longer wired into the validator) and there is no longer any cross-check of `useResiliencePolicy` action names against the `groups` block — the action is inert in v2, so an unknown name is simply a no-op at runtime rather than a load error (see [actions.md](actions.md#useresiliencepolicy)).

---

## `connectors` block

`connectors:` is the top-level destination library, a flat slice of [`Connector`](../contracts/config/connectors.go) entries. Each declares one reusable destination; configurations attach destinations via `Configuration.ConnectorBindings`. See [connectors.md](connectors.md) for the per-type field reference and [spool.md](spool.md) for the disk-backed runtime.

Each connector entry must:

- Have a unique `name` across the slice (`ErrDuplicateConnectorName`, `config_validate.go:170`, enforced in `validateLibraries`).
- Pass `Connector.Validate()` — the per-type required-field check (s3 needs `bucket` + `region`, azure_blob needs `account` + `container`, webhook needs `url` + `secret_ref` + `timeout_ms`).
- Be referenced by a defined `connector_bindings[].connector` name — an unknown reference aborts with `ErrUnknownConnectorReference`.

`connectors:` may be empty or absent — when there are no connectors, the spool is not constructed and the body-capture middleware short-circuits. This is the default for any deployment that does not want persistent capture.

---

## `admin` block

`admin:` is the management-console gate (`contracts/admin/admin.go::Config`). The block is **optional**; absent means the console never starts. When present and `enabled: true`, the gateway starts a SECOND `http.Server` (`cmd/gateway/main.go::startAdmin`) bound to `admin.bind_addr` — or the default `0.0.0.0:8081` (`admin.Config.EffectiveBindAddr`, `DefaultBindAddr`) — separate from the data-plane listener (`SLIPSPACE_HTTP_BIND`). `startAdmin` validates the block first (`resolved.Admin.Validate()`, `cmd/gateway/main.go:555`) — when the console is enabled but no password resolves (or `bind_addr` is malformed) the listener is **not** started; the gateway logs `admin console NOT started: invalid admin config` and keeps serving data-plane traffic. Boot is not failed. It serves the embedded SPA at `/admin/` and the control-plane API under `/admin/api/v1/*` (the `Prefix` const in `internal/admin/mux.go`). The mux mounts the admin tree under the `/admin` prefix via `http.StripPrefix` (`mux.go:376`), so no path stripping is required on the ingress side — the mux strips its own prefix internally before the inner handlers, which are registered at `/api/v1/*` (`mux.go:146-360`).

```yaml
admin:
  enabled: true
  bind_addr: "0.0.0.0:8081"
  password: "operator-secret"
```

### `admin.Config` fields

| Field | Type | Required | Notes |
|---|---|---|---|
| `enabled` | bool | yes | Gates the admin console. `false` = the `/admin/` prefix is never mounted, no admin routes exist anywhere. |
| `bind_addr` | string | no | The admin listener address (host:port). Empty resolves to the effective default `0.0.0.0:8081` (`EffectiveBindAddr`, `DefaultBindAddr`). Validated as host:port with numeric port. The admin console runs as a dedicated second `http.Server` on this address, distinct from the data-plane listener (`SLIPSPACE_HTTP_BIND`). |
| `password` | string | no | Operator credential for HTTP Basic auth. Username is hardcoded `admin` (`contracts/admin/admin.go:19`). May be empty in YAML — at runtime `SLIPSPACE_ADMIN_PASSWORD` wins when set; otherwise this field is used. `ResolvePassword` (`admin.go:85-91`) returns the resolved password string — the env var wins over `admin.password`, and it returns `""` when both are empty. With `enabled: true`, **at least one** of the env var or this field must be non-empty: `Config.Validate` (`admin.go:96-107`) returns `ErrPasswordRequired`, and only when the admin block is `Enabled` and `ResolvePassword()` is empty. Both may be set simultaneously — the env var takes precedence; it is not an exclusive-or. Never serialised to JSON. |

The console's runtime behaviour (live-feed capacity, body-capture budget, snapshot interval) is configured via `SLIPSPACE_ADMIN_*` env vars, not this block — see [environment-variables.md](environment-variables.md).

---

## `telemetry` block

`telemetry:` is optional and carries operator-tunable knobs that shape what the gateway emits to its telemetry signals (`contracts/config/telemetry.go::Telemetry`). Today the only nested block is `content_capture`, which caps the bytes the gateway includes on the GenAI span and `gen_ai.client.inference.operation.details` event when `SLIPSPACE_OTEL_CAPTURE_CONTENT=true`. Absent fields fall back to the built-in defaults, so existing deployments need no change.

```yaml
telemetry:
  content_capture:
    messages_max_bytes: 32768
    system_instructions_max_bytes: 32768
    tool_definitions_max_bytes: 65536
```

### Cap semantics (`contracts/config/telemetry.go::ContentCaptureCaps.Resolve`)

| YAML state | Behaviour |
|---|---|
| key absent | use built-in default (32 KiB / 32 KiB / 64 KiB) |
| key present, value `N < 0` | treated as unset — use built-in default (`resolveCap`, `contracts/config/telemetry.go:107-109`) |
| key present, value `0` | unbounded — no truncation, no drop |
| key present, value `N > 0` | cap at N bytes |

| Field | Default | What it caps |
|---|---|---|
| `messages_max_bytes` | 32 KiB | Every text field under `input.messages` / `output.messages`: text parts' `content`, tool-call response `result`, tool-call `arguments` (whole-document drop on overflow to keep the JSON well-formed), and each tool definition's `description`. |
| `system_instructions_max_bytes` | 32 KiB | Per-part `content` under `gen_ai.system_instructions`. |
| `tool_definitions_max_bytes` | 64 KiB | Combined `parameters` JSON-schema size across every tool definition. Once exceeded, parameters are dropped wholesale from every definition (type/name/description still emitted). |

The caps shape only what reaches the span and the operation-details event — they do not affect the connector spool, which carries the full unredacted body to operator-configured destinations (invariant #4). `SLIPSPACE_OTEL_CAPTURE_CONTENT` is the master switch; the caps only apply when capture is on. See [observability.md](observability.md#genai-spans-and-events).

---

## `pricing` block

`pricing:` is optional and turns on per-request USD cost estimation (`contracts/config/pricing.go::Pricing`). When present (and `enabled` not `false`), the gateway prices each request's extracted charge quantities — token buckets, server-tool call counts, service tier, inference geo — against a rate card and emits the estimate on the `slipspace.cost.usd.total` meter (labelled `slipspace.cost.category`), the gen_ai span/event (`slipspace.cost.usd` + per-category attrs), and the connector Record's `cost` block. Absent block = costing off entirely.

```yaml
pricing:
  enabled: true          # optional; the block's presence is the opt-in
  use_defaults: true     # optional; false hides the embedded rate card
  models:                # operator overrides / additions
    - match: "claude-sonnet-5*"      # exact id, or prefix + trailing *
      provider: anthropic            # optional provider-name restriction
      effective_from: "2026-09-01"   # optional dated entry (UTC midnight)
      per_mtok:                      # USD per million tokens
        input: 3.00
        output: 15.00
        cache_read: 0.30
        cache_write_5m: 3.75
        cache_write_1h: 6.00
        audio_input: 0               # 0 = bills at the plain input rate
        audio_output: 0
      long_context:                  # optional >threshold repricing
        threshold: 200000
        per_mtok: { input: 6.00, output: 22.50 }
      tiers: { batch: 0.5 }          # service_tier → token-cost multiplier
      geos: { us: 1.1 }              # inference_geo → token-cost multiplier
      tool_calls:                    # USD per 1k calls, keyed by the wire
        web_search_requests: 10.00   # counter name in server_tool_use
    - match: "qwen*"                 # self-hosted: zero-rate (matched, $0)
      per_mtok: { input: 0, output: 0 }
```

Semantics:

- **Matching**: longest pattern wins across the merged (operator + embedded) card; an operator entry beats an embedded one on a full tie. Multiple entries may share a `match` with different `effective_from` dates — the latest date not after the request start applies.
- **Rates are explicit, never derived.** A category left zero is free. The embedded defaults (`internal/pricing/defaults.go`, versioned — e.g. `2026-07`) spell out every category for the current frontier models of the three vendors.
- **Unmatched models are unpriced, never guessed**: the Record carries no `cost` block, the event carries `cost_unpriced`, and `slipspace.pricing.unmatched.total` climbs — the operator's cue to add an entry.
- **Cost is an estimate at observation time.** The token counters and Record quantities remain the ground truth, so history is re-priceable; each estimate carries the rate-card version that produced it.
- A response reporting only the flat cache-creation total (no TTL split) is priced entirely at the 5m rate. Tier/geo multipliers apply to token categories only, not tool calls.
- Like `admin`/`telemetry`, the block is **not** editable through the admin write API — a YAML change requires a restart.

---

## Protocol resolution

A request's **protocol** is fixed by its inbound path — there is one canonical base path per wire shape, plus a no-version-segment alias for each of the four fixed protocols (`internal/selection/protocol.go::ProtocolForPath`). The model then rides in the request body (except for Gemini's `generate_content`, where the model is part of the path).

| Inbound path | Protocol constant | Value |
|---|---|---|
| `/v1/chat/completions`, `/chat/completions` | `ProtocolChat` | `chat` |
| `/v1/responses`, `/responses` | `ProtocolResponses` | `responses` |
| `/v1/messages`, `/messages` | `ProtocolMessages` | `messages` |
| `/v1/embeddings`, `/embeddings` | `ProtocolEmbeddings` | `embeddings` |
| `/v1beta/models/{model}:generateContent` (or `:streamGenerateContent`) | `ProtocolGenerateContent` | `generate_content` |

The protocol constants live in `contracts/config/model.go:18` and are re-exported from `internal/selection`. When `ProtocolForPath` does not recognise a path, the data plane falls back to per-configuration **passthrough** matching (`MatchPassthrough`) before returning a 404.

These protocol names are the same strings a provider's `protocols:` map and a binding's `protocol:` field must use — `validateProviders` and `validateBindings` reject any unknown protocol against the `knownProtocols` set (`internal/config/config_validate.go:15`).

---

## The binding triangle

In v2 the word "binding" has a precise meaning — a `Binding` entry inside a configuration. This diagram shows how a request resolves through the catalogues:

```mermaid
flowchart TB
    subgraph providersBlock [providers]
        P[Provider<br/>base_url<br/>protocols: chat/messages/...<br/>passthrough families]
    end
    subgraph groupsBlock [groups]
        G[Group<br/>mode + targets]
    end
    subgraph cfgBlock [configurations]
        C[Configuration<br/>credentials<br/>bindings<br/>passthrough_bindings<br/>rule_names]
    end
    subgraph keysBlock [api_keys]
        K[APIKey<br/>secret + configuration]
    end

    K -- configuration --> C
    C -- bindings[*].provider --> P
    C -- bindings[*].group --> G
    C -- passthrough_bindings[*].provider --> P
    C -- credentials keys --> P
    G -- targets[*].provider --> P

    Req["request: (protocol-from-path, model-from-body)"] -- Authorization: sk_live_... --> K
    Req -- X-Slipspace-Configuration (passthrough auth) --> C
    Req -. first matching binding .-> C
```

What this enforces at load time (`internal/config/config_validate.go`):

- Every `api_keys[].configuration` resolves to a known configuration (`ErrUnknownConfiguration`).
- Every `configurations[].credentials` key names a known provider.
- Every binding sets exactly one of `provider` / `group`, the reference resolves, and the destination serves the binding's protocol (protocol-preserving).
- Every `passthrough_bindings[]` resolves to a `(provider, family)` that exists.
- Every `configurations[].rule_names` entry resolves to a rule in the library (`ErrUnknownRuleName`).
- Every `connector_bindings[].connector` resolves to a defined connector (`ErrUnknownConnectorReference`).

What this gives the data plane post-load (`internal/config/config_model.go::buildIndexes`):

- `SecretIndex`: secret → `*APIKey` for O(1) auth lookup.
- `ConfigurationIndex`: configuration name → pointer-stable `*Configuration` copy for passthrough mode and post-auth re-binding.
- `RuleIndex`: rule name → `*RuleContract`.
- `PerConfigurationRules`: configuration name → ordered slice of `*RuleContract` (the configuration's transform rules in `rule_names` order).
- `ConnectorIndex`: connector name → `*Connector`.
- `PricingTable`: compiled rate card for per-request USD costing.

`SourceFiles` (block name → originating filename, so the admin write path persists a mutated block back to the file it came from) is **not** built here — it is populated during `Load` (`internal/config/config_model.go:164`, `r.SourceFiles = seen`, recorded by `mergeDoc` as each block's origin file).

There is **no** `RouteIndex` in v2 — routing is not index-based. Each request resolves its destination at request time by `ProtocolForPath` then `Select` against the owning configuration's bindings (`internal/selection`).

---

## Worked examples

### Minimal config that boots

The smallest tree that satisfies validation: one provider that serves one protocol, one configuration with the matching credential and a catch-all binding, one API key referencing it.

```yaml
# providers.yaml
providers:
  openai:
    base_url: "https://api.openai.com"
    protocols:
      chat:
        path: "/v1/chat/completions"
        auth:
          header: "Authorization"
          format: "Bearer {key}"
```

```yaml
# policy.yaml
configurations:
  production:
    credentials:
      openai: sk-production-openai-key
    bindings:
      - protocol: chat
        provider: openai      # empty models = catch-all for chat

api_keys:
  - secret: sk_live_minimal_demo_key
    name: "demo client"
    configuration: production
    enabled: true
```

No `groups`, no `rules`, no `admin`. The gateway boots, accepts managed-mode requests with `Authorization: Bearer sk_live_minimal_demo_key`, swaps in `sk-production-openai-key`, and forwards `POST /v1/chat/completions` to `https://api.openai.com/v1/chat/completions`.

### A multi-provider configuration with a resilience group

```yaml
# providers.yaml
providers:
  openai:
    base_url: "https://api.openai.com"
    protocols:
      chat:
        path: "/v1/chat/completions"
        auth: { header: "Authorization", format: "Bearer {key}" }
      responses:
        path: "/v1/responses"
        auth: { header: "Authorization", format: "Bearer {key}" }

  anthropic:
    base_url: "https://api.anthropic.com"
    required_headers:
      anthropic-version: "2023-06-01"
    protocols:
      messages:
        path: "/v1/messages"
        auth: { header: "x-api-key", format: "{key}" }
      # OpenAI-compat chat surface on Anthropic — same provider, different
      # credential convention per protocol (invariant #6).
      chat:
        path: "/v1/chat/completions"
        auth: { header: "Authorization", format: "Bearer {key}" }
    passthrough:
      message-batches:
        auth: { header: "x-api-key", format: "{key}" }
        paths:
          - match: "/v1/messages/batches"
            methods: ["POST", "GET"]
          - match: "/v1/messages/batches/{id}/results"
            methods: ["GET"]
```

```yaml
# groups.yaml  (any filename works — merged by top-level key)
groups:
  chat-ha:
    mode: failover
    failure_status_codes: [502, 503, 504]
    targets:
      - provider: openai
      - provider: anthropic
        alias: claude-sonnet-4-5     # rewrite the body model for the fallback
```

```yaml
# policy.yaml
configurations:
  production:
    credentials:
      openai: sk-mock-openai
      anthropic: sk-ant-mock-anthropic
    bindings:
      # claude-* on the chat protocol → Anthropic's OpenAI-compat surface
      - protocol: chat
        models: ["claude-*"]
        provider: anthropic
      # everything else on chat → the failover group
      - protocol: chat
        group: chat-ha
      # native Anthropic messages
      - protocol: messages
        provider: anthropic
    passthrough_bindings:
      - provider: anthropic
        family: message-batches
    rule_names:
      - strip-internal-headers
    tags:
      tier: production

api_keys:
  - secret: sk_live_alpha_internal_pipeline
    name: "internal-dev pipeline"
    configuration: production
    enabled: true

rules:
  - name: strip-internal-headers
    condition:
      type: header
      keyOperator: Equals
      keyPattern: x-internal-trace
    actions:
      - type: setHeader
        headerName: x-internal-trace
        headerAction: Remove
    behavior: continue
```

```yaml
# admin.yaml (optional)
admin:
  enabled: true
  bind_addr: "0.0.0.0:8081"
  password: "operator-secret"
telemetry:
  content_capture:
    messages_max_bytes: 32768
    system_instructions_max_bytes: 32768
    tool_definitions_max_bytes: 65536
```

What this gives you: a `chat` request with a `claude-*` model lands on Anthropic's OpenAI-compat surface (with `Authorization: Bearer`); any other `chat` model routes through the `chat-ha` failover group (openai primary, anthropic fallback with a model alias); native `messages` requests go straight to Anthropic; and `/v1/messages/batches…` is proxied verbatim through the passthrough family. The `rule_names` entry is a pure transform — it strips a header and does not route.

---

## Why no `${VAR}` substitution

The loader performs no `${VAR}` expansion and no `env:` substitution — decoded values pass through verbatim. This is deliberate.

- **File contents are trusted by construction.** In production the config directory is mounted from a Kubernetes Secret (or a filesystem-permissioned dir on bare metal); the operator picks the substrate and SlipSpace trusts what's on disk.
- **One source of truth per secret.** A `${OPENAI_KEY}` literal would make the file meaningless without the env, and the env var becomes the de-facto source of truth — not what the admin console shows, what the bundler exports, or what the validator sees. Literal strings keep the YAML the canonical artefact.
- **Only file paths are env-overridable.** `SLIPSPACE_CONFIG_DIR` selects the dir; everything inside is read as-is.

The in-YAML indirections are the connector `secret_ref` field (`env:NAME` or `file:/path`, `contracts/config/connectors.go:123-125`) and the connector auth `*_ref` fields (`access_key_id_ref`, `secret_access_key_ref`, `external_id_ref`, `sas_token_ref`, `account_key_ref` — `contracts/config/connectors.go:148-171`), all resolved by the connector factory when the destination is built, plus `advisors.<name>.hmac_secret_file` (`contracts/config/advisors.go:45-49`), a file path read once at startup by `cmd/gateway/agentrouting.go:24`. None of them is expanded by the loader.

The two intentional exceptions are `SLIPSPACE_ADMIN_PASSWORD` (kept out of YAML for production hygiene) and the server-level `SLIPSPACE_*` env vars (not in YAML at all). See [environment-variables.md](environment-variables.md).

---

## Validation errors

The invariants the loader enforces, and the sentinel each violation wraps (`internal/config/errors.go`, `internal/config/config_validate.go`). Many binding/provider/group failures wrap the umbrella `ErrValidation` with a specific message at the call site.

`ResolvedConfig.Validate` first guards the empty tree — `len(r.Configurations) == 0` returns `ErrNoConfigurations` (`internal/config/config_validate.go:29-31`) — then runs six steps in order (`config_validate.go:32-47`): `validateProviders` → `validateGroups` → `validateLibraries` → `Pricing.Validate` → `validateAdvisors` → `validateConfigurations`. So the pricing block's own `Validate` and the advisors/`agent_routing` cross-check (the named advisor must exist in the `advisors` block; `allow_models` must be non-empty) both run **before** configuration and binding validation — a bad rate card or a dangling advisor reference aborts the load before any binding error is reported.

| Sentinel | Triggered by |
|---|---|
| `ErrEmptyDirectory` | Config dir has no `*.yaml` files. |
| `ErrDuplicateKey` | A top-level block (`providers`, `groups`, `configurations`, …) is set by more than one file. |
| `ErrLegacyProvidersKey` | A file carries the pre-rename `backends:` key (renamed to `providers:`, no alias). |
| `ErrParse` | A `*.yaml` file fails to unmarshal. `Load` double-wraps it — `fmt.Errorf("config: load %q: %w: %w", name, ErrParse, uerr)` (`internal/config/config_model.go:154`) — so `cmd/cli`'s reason classifier can branch on `errors.Is(err, config.ErrParse)` and report `parse_error` instead of `other`. |
| `ErrNoConfigurations` | The merged tree has zero entries under `configurations`. |
| `ErrUnknownConfiguration` | An `api_keys[]` entry names a configuration that does not exist. |
| `ErrUnknownRuleName` | A configuration's `rule_names[]` names a rule not in the library. |
| `ErrDuplicateRuleName` | Two rules in the library share the same `name`. |
| `ErrDuplicateConnectorName` | Two connectors share the same `name`. |
| `ErrUnknownConnectorReference` | A `connector_bindings[].connector` names a connector not in the `connectors` block. |
| `ErrAuthFormatWithoutHeader` | An `auth.format` is set without an `auth.header` at the same level. |
| `ErrInvalidAuthFormat` | An `auth.format` does not contain `{key}` exactly once. |
| `ErrValidation` (provider) | `base_url` missing; provider declares no `protocols` and no `passthrough`; unknown protocol name; passthrough path missing `match`/`methods`. |
| `ErrValidation` (group) | Group has no `targets`; a target has no `provider`; a target names an unknown provider. |
| `ErrValidation` (configuration) | A `credentials` key names an unknown provider; an `api_keys[]` secret is empty or duplicated; a duplicate API-key `id`; a `passthrough_bindings[]` names an unknown provider or family. |
| `ErrValidation` (binding) | Unknown protocol; not exactly one of `provider`/`group`; unknown provider/group; destination does not serve the protocol (protocol-preserving); malformed model pattern (non-trailing or multiple `*`); two catch-all bindings on one protocol; a duplicate exact model on one protocol. |

The admin block's `ErrPasswordRequired` / `ErrInvalidBindAddr` are not load-time errors — `ResolvedConfig.Validate` never calls `admin.Config.Validate`. They are raised by `startAdmin` at listener start-up and cause the console listener to be skipped (logged, non-fatal).

> Several v1 sentinels (`ErrPathCollision`, `ErrPrefixRequiredEmpty`, `ErrUnknownResilienceName`, `ErrDuplicateResilienceName`, `ErrDuplicateResilienceID`, `ErrTargetProviderMissingCredential`, `ErrUnexpectedConfigFile`, `ErrWrongFileForKey`, `ErrDuplicateRuleID`) remain defined in `errors.go` but are no longer wired into the v2 validator — they describe routing/resilience concepts that no longer exist. They are inert and should not appear in any v2 error.

---

## Known limitations

These are documented intentionally — don't fix them without checking the milestone first.

1. **Live edits flow through the admin write API only.** The write API carries CRUD for rules, providers, groups, configurations, api_keys, and connectors — `GET`/`POST`/`PUT`/`DELETE` per resource, plus a `PATCH /api/v1/config/api-keys/{id}` that api_keys alone registers (`internal/admin/mux.go:202-203`) for partial updates such as enable/disable without resubmitting the whole key; every mutation clones the live snapshot, validates (`RevalidateAndIndex`), persists atomically, and publishes through `config.Store.Replace` so the next request sees the new config — no restart, no torn reads (see [Atomic snapshot store](#atomic-snapshot-store)). Direct YAML edits on disk (and the `admin`, `telemetry`, `pricing` and `advisors` blocks, none of which have write endpoints) still require a process restart. An `fsnotify`-based reload path for on-disk edits remains an unscheduled backlog item — no fsnotify dependency exists in the tree.

   ### Atomic snapshot store

   `internal/config/store.go::Store` owns the live `ResolvedConfig` behind an `atomic.Pointer`. Every consumer (router, auth resolver, rules evaluator, forwarder, reporter, admin handlers) holds the `*Store`, never the `*ResolvedConfig` itself. Reads call `store.Snapshot()` at request top and operate on that pointer for the rest of the request — a `Replace` landing mid-handler is invisible to the in-flight call. Writers (admin mutations only) follow `Clone → mutate → RevalidateAndIndex → WriteConfig → Store.Replace`; failure at any step leaves the live snapshot untouched. (`WritePolicyYAML` is retained only as a back-compat alias that delegates to `WriteConfig`, which routes each mutated top-level block back to the file recorded in `ResolvedConfig.SourceFiles` — falling back to `providers.yaml` / `policy.yaml` / `admin.yaml` only for a block with no recorded origin, e.g. one first introduced through the admin API (`internal/config/loader.go:26-32`). No write targets a fixed `policy.yaml`. The canonical commit path, `internal/admin/rules_write.go::commitClone` (`internal/admin/rules_write.go:259-268`), calls `WriteConfig(configDir, clone)` directly, and its godoc (`:253-258`) says so — routing each editable block back to its `SourceFiles` origin, not to a fixed `policy.yaml`.) This is load-bearing invariant #9 in [`CLAUDE.md`](../CLAUDE.md).

2. **No `${VAR}` substitution.** See [Why no `${VAR}` substitution](#why-no-var-substitution). The one operator escape hatch is `SLIPSPACE_ADMIN_PASSWORD`.

3. **Each block has a single authoring home.** A given top-level block must live in exactly one file; spreading `providers:` across two files is rejected with `ErrDuplicateKey`. Filenames themselves are unconstrained — any `*.yaml` is merged.

4. **Binding evaluation order is YAML list order.** `Configuration.Bindings` is a list; the first binding whose protocol and model patterns match wins. There is no priority field, and prefix-overlap between bindings is resolved purely by order (the validator only rejects duplicate **exact** models and duplicate catch-alls per protocol).

5. **Rule-name evaluation order is YAML list order.** `Configuration.RuleNames` is a list, not a priority-keyed map.

6. **API keys are flat — no per-key bindings or rules.** All policy lives on the named configuration. To give two keys different behaviour, point them at two different configurations.

7. **Groups are protocol-preserving.** A group cannot fail over across protocols — every target must serve the binding's protocol. Cross-provider protocol translation mid-failover is a v1.2+ item.

---

## See also

- [providers.md](providers.md) — per-provider deep dive (OpenAI / Anthropic / Gemini, OpenAI-compat surfaces, protocol/auth specifics).
- [rules.md](rules.md) — transform-rule grammar (conditions, actions, evaluator algorithm).
- [resilience.md](resilience.md) — resilience modes, targets, and circuit breaker that `groups` orchestrate.
- [connectors.md](connectors.md) — destination types (s3, azure_blob, webhook) and per-type auth modes.
- [connector-bindings.md](connector-bindings.md) — per-configuration sampling / filter / size-cap knobs.
- [spool.md](spool.md) — disk layout, lifecycle, and loss policy for buffered connector deliveries.
- [environment-variables.md](environment-variables.md) — every `SLIPSPACE_*` env var (server bind, spool root, OTel, admin runtime knobs).
