# Configuration Model

Everything the gateway needs to serve traffic lives in three YAML files under `SLUICE_CONFIG_DIR` (default `/etc/sluice/`): `providers.yaml`, `policy.yaml`, and (optionally) `admin.yaml`. The loader enumerates the directory, restricts each file to a specific set of top-level keys, decodes the merged tree into a `ResolvedConfig`, validates cross-block references, then builds the runtime indexes the data plane reads on every request.

This page is the operator's reference for that on-disk schema — what files exist, which top-level keys may appear in each, every field on every type, how the pieces bind together, and what's deliberately out of scope.

---

## Table of contents

1. [Mental model](#mental-model)
2. [File discovery and merging](#file-discovery-and-merging)
3. [Top-level keys](#top-level-keys)
4. [`providers` block](#providers-block)
5. [`configurations` block](#configurations-block)
6. [`api_keys` block](#api_keys-block)
7. [`rules` block](#rules-block)
8. [`resilience_policies` block](#resilience_policies-block)
9. [`admin` block](#admin-block)
10. [The binding triangle](#the-binding-triangle)
11. [Worked examples](#worked-examples)
12. [Why no `${VAR}` substitution](#why-no-var-substitution)
13. [Validation errors](#validation-errors)
14. [Known limitations](#known-limitations)

---

## Mental model

> **Three layers. Static catalogue, reusable policy bundles, per-client bearer references.**

- `providers.yaml` is the **catalogue** — what upstreams exist, where they live, what paths they expose, what headers they take.
- `policy.yaml` is the **policy library plus the bundles** — named `configurations` each pin a credential set, a list of rule names, and an optional resilience policy. Rules and resilience policies are defined once at the top level and referenced by name from configurations.
- `api_keys` (inside `policy.yaml`) is the **bearer reference** — flat list of gateway-issued secrets, each naming the `configuration` it resolves to. Many keys may share one configuration.
- `admin.yaml` is the **management console gate** — off by default; flipping `enabled: true` brings up the second http.Server on its own port.

Server-level configuration (bind address, drain timeout, NATS URL, OTel exporter, Prometheus scrape, log format/level) is **not** in YAML — it lives on the `SLUICE_*` env vars consumed by `LoadEnv`. See [environment-variables.md](environment-variables.md).

---

## File discovery and merging

```mermaid
flowchart LR
    A[SLUICE_CONFIG_DIR] --> B[ListConfigFiles]
    B --> C{filename in<br/>allowed set?}
    C -- no --> X[ErrUnexpectedConfigFile]
    C -- yes --> D[parseTopLevel<br/>via koanf + yaml.v3]
    D --> E{top-level key<br/>allowed in this file?}
    E -- no --> Y[ErrWrongFileForKey]
    E -- yes --> F[merge into<br/>per-key yaml.Node tree]
    F --> G[decode → ResolvedConfig]
    G --> H[Validate]
    H --> I[buildIndexes]
    I --> J[ready to serve]
```

### Rules

1. **Directory must exist and be readable.** The loader fails on `os.ReadDir` error.
2. **Subdirectories are skipped silently.** Only regular files are considered.
3. **Filenames are pinned.** Only three are accepted: `providers.yaml`, `policy.yaml`, `admin.yaml`. Any other filename in the directory aborts the load with `ErrUnexpectedConfigFile` — there is no `*.yaml` glob, no convention-driven discovery, no merge of arbitrarily-named files.
4. **Directory cannot be empty.** Zero accepted files raises `ErrEmptyDirectory`.
5. **Top-level keys are restricted by filename.** Each file may carry only the keys in its allow-list (see [Top-level keys](#top-level-keys)). A key in the wrong file aborts with `ErrWrongFileForKey`, naming the offender.
6. **Files are processed in alphabetical filename order.** `admin.yaml` → `policy.yaml` → `providers.yaml`. Each top-level key may only appear once across the merged tree — the allow-list ensures a key is never legal in two files at once, so duplicate-key collisions are impossible by construction.
7. **Trusted contents.** File contents are interpolated verbatim. There is no `${VAR}` substitution, no `env:` syntax — see [Why no `${VAR}` substitution](#why-no-var-substitution).

### Loader override

`SLUICE_CONFIG_DIR` selects the directory. The CLI's `--dir` flag overrides this for one-shot validation runs but does not affect the data-plane process.

---

## Top-level keys

The loader recognises six top-level keys, distributed across three files:

| Key | File | Carries |
|---|---|---|
| `providers` | `providers.yaml` | Provider catalogue: base URLs, prefixes, endpoint definitions, required headers, auth-header overrides. |
| `configurations` | `policy.yaml` | Named policy bundles. Each pins upstream credentials, a rule-name list, an optional resilience-policy reference, and tags. |
| `api_keys` | `policy.yaml` | Flat list of gateway-issued bearers; each points at a configuration by name. |
| `rules` | `policy.yaml` | Top-level rule library. Definitions are unique by name; configurations reference them through `rule_names`. |
| `resilience_policies` | `policy.yaml` | Top-level resilience policy library. Definitions are unique by name; configurations reference one through `resilience_name`. |
| `admin` | `admin.yaml` | Management console gate. Optional file; absent means the console never starts. |

Any other top-level key in any file aborts the load.

---

## `providers` block

`providers.yaml` carries `providers:` — a map from provider name to its catalogue entry. See [providers.md](providers.md) for the per-provider deep dive (OpenAI / Anthropic / Gemini specifics, OpenAI-compat surfaces, model-keyed redirect patterns). This section documents the **schema** only.

```yaml
providers:
  <provider name>:
    base_url: "https://api.openai.com"
    prefix: "openai"
    prefix_required: false
    required_headers:
      anthropic-version: "2023-06-01"
    auth_header: "Authorization"
    auth_format: "Bearer {key}"
    endpoints:
      <endpoint name>:
        path: "/v1/chat/completions"
        method: ["POST"]
        accepted_paths: ["/v1/chat/completions"]
        accepts_streaming: true
        request_kind: "openai.chat"
        auth_header: "Authorization"
        auth_format: "Bearer {key}"
        prefix_optional: false
```

### `Provider` fields

| Field | Type | Required | Notes |
|---|---|---|---|
| `base_url` | string | yes | Upstream root URL. The forwarder appends each endpoint's `path` to this when proxying. |
| `prefix` | string | no | URL segment that disambiguates this provider when multiple providers share an `accepted_paths` entry (e.g., `/v1/models`). Empty means bare-only matching (legacy single-provider deploys). |
| `prefix_required` | bool | no | Default `false`. When `false`, the provider matches **both** `/<prefix><accepted_path>` and `<accepted_path>` (the bare form makes this provider the default for that path). When `true`, only the prefixed form matches — and `prefix` cannot be empty (`ErrPrefixRequiredEmpty`). |
| `required_headers` | map[string]string | no | Headers the gateway injects on every forwarded request to this provider (e.g., `anthropic-version: 2023-06-01`). |
| `auth_header` | string | no | Override outgoing HTTP header name into which managed-mode credentials are injected. Empty defers to per-endpoint override, then to the per-provider native default (`Authorization` for OpenAI, `x-api-key` for Anthropic, `x-goog-api-key` for Gemini). |
| `auth_format` | string | no | Template for the credential value, with exactly one `{key}` placeholder. Only consulted when an override `auth_header` is in effect. Empty (with override `auth_header` set) renders the raw credential. |
| `endpoints` | map[string]Endpoint | yes | Provider's endpoint catalogue, keyed by logical name. The key shows up in telemetry as the resolved endpoint name. |

### `Endpoint` fields

| Field | Type | Required | Notes |
|---|---|---|---|
| `path` | string | yes | Upstream path appended to the provider's `base_url`. |
| `method` | []string | yes | HTTP methods the endpoint accepts. |
| `accepted_paths` | []string | yes | Inbound path patterns. Routing builds its index from this list. |
| `accepts_streaming` | bool | no | Default `false`. Affects how the forwarder handles the response body (SSE wiring). |
| `request_kind` | string | yes | Names the typed request shape (e.g. `openai.chat`, `anthropic.messages`) so body-capture middleware can deserialise into the right model type with `DynamicProperties` preservation. |
| `auth_header` | string | no | Per-endpoint override. Empty defers to provider-level then to native default. Load-bearing for OpenAI-compat surfaces on Anthropic and Gemini — both want `Authorization: Bearer` rather than the native header. |
| `auth_format` | string | no | Per-endpoint format template; same `{key}` rules as the provider-level variant. |
| `prefix_optional` | bool | no | Default `false`. When `true`, escapes `Provider.PrefixRequired` for this one endpoint — bare `accepted_paths` emit as routes even though the rest of the provider's endpoints stay prefix-only. Anthropic's `/v1/messages` uses this so vanilla Anthropic SDKs work pointed at the gateway root while the OpenAI-compat `/v1/chat/completions` on the same provider stays prefix-only to avoid colliding with openai. No effect when `prefix_required` is `false` (the endpoint already emits both forms). |

### Auth-header resolution order

The destination builder mints exactly one header per (provider, endpoint, mode) combination. Override resolution (managed mode):

1. Endpoint's `auth_header` / `auth_format` if both set.
2. Otherwise provider's `auth_header` / `auth_format` if both set.
3. Otherwise the per-provider native default baked into `auth.UpstreamCredentialHeader`.

Validation rejects `auth_format` without a matching `auth_header` at the same level (`ErrAuthFormatWithoutHeader`) — the format would otherwise be silently ignored — and rejects an `auth_format` whose `{key}` placeholder count is not exactly one (`ErrInvalidAuthFormat`).

### Route collision

Every (provider, endpoint, accepted_path) tuple emits one or two `RouteIndex` entries:

- `/<prefix><accepted_path>` always emits when `prefix` is non-empty.
- `<accepted_path>` (bare) also emits **unless** the provider has `prefix_required: true` and the endpoint has not set `prefix_optional: true`.

If two providers both try to claim the same fully-resolved path (either bare or prefixed), the loader fails with `ErrPathCollision`. With prefix disambiguation, collisions only happen when two providers both have `prefix_required: false` sharing an `accepted_path`, or when two providers share both the same prefix and an `accepted_path`.

---

## `configurations` block

`policy.yaml` carries `configurations:` — a map from configuration name to a reusable policy bundle. Per-endpoint authorization is implicit: managed mode can only forward to providers that have an entry in `upstream_credentials` (no credential, no forward); passthrough mode is gated by the upstream's own auth on the BYOK token the client carries.

```yaml
configurations:
  production:
    upstream_credentials:
      openai: sk-mock-openai
      anthropic: sk-ant-mock-anthropic
    rule_names:
      - route-claude-models-to-anthropic
      - route-gemini-models-to-gemini
    resilience_name: high-availability
    tags:
      tier: production
```

### `Configuration` fields

| Field | Type | Required | Notes |
|---|---|---|---|
| `upstream_credentials` | map[string]string | no | Provider name → upstream API key the gateway substitutes on the way out. Populated for managed mode; passthrough mode leaves the client's `Authorization` header intact. A provider key with an empty-string value is legal and means "this provider is in the configuration's reach but passthrough-only" — useful for ollama-style upstreams that don't need a key. |
| `rule_names` | []string | no | Names of rules from the top-level `rules:` library this configuration applies. Unknown names abort load with `ErrUnknownRuleName`. Evaluation order = list order (no priority field). |
| `resilience_name` | *string | no | Names the resilience policy from the top-level `resilience_policies:` library this configuration uses. `nil` (unset) means no resilience policy — single-attempt, no breaker. Unknown name aborts load with `ErrUnknownResilienceName`. Every target in the named policy must have a corresponding entry in `upstream_credentials`, else `ErrTargetProviderMissingCredential`. |
| `tags` | map[string]string | no | Static configuration-level labels carried as request context (logged on the per-request structured-log envelope, surfaced on the admin console's configuration detail page). **Not** the same channel as rule-attached request tags: `configurations[].tags` does **not** propagate to `gateway.request.Tags` on the NATS audit event and does **not** bump `gateway.tags.applied.total`. Those signals are driven by [`addTag` rule actions](actions.md#addtag) only. Use this field for static metadata (`tier: production`, `team: ingestion`); use `addTag` for per-request labels that need to surface in audit and dashboards. |

There must be **at least one** entry in `configurations:`. An empty map aborts with `ErrNoConfigurations`.

---

## `api_keys` block

`policy.yaml` carries `api_keys:` — a flat list (slice) of gateway-issued keys. Each entry references a configuration by name. Many keys may share one configuration.

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

### `APIKey` fields

| Field | Type | Required | Notes |
|---|---|---|---|
| `secret` | string | yes | The bearer token clients present. Conventionally prefixed `sk_live_…` for production keys or `sk_dev_…` for development keys, but the loader does not enforce a prefix. Authentication compares this in constant time. |
| `name` | string | yes | Human-readable label surfaced in logs and reporting events. Carries no auth meaning. |
| `configuration` | string | yes | Name of the configuration this key resolves to. Unknown name aborts load with `ErrUnknownConfiguration`. |
| `enabled` | bool | yes | Toggles the key without removing it. A disabled key authenticates structurally but is rejected before forwarding. |

Lookups use `SecretIndex` (built post-validate); the slice exists for enumeration in the admin console and audit reporting.

---

## `rules` block

`policy.yaml` carries `rules:` — the top-level rule library, a flat list of `RuleContract` entries. Definitions are unique by `name`; configurations reference them through `Configuration.RuleNames`. See [rules.md](rules.md) for the condition/action grammar, evaluator algorithm, and worked examples.

Each rule must:

- Have a unique `name` across the library (`ErrDuplicateRuleName`).
- Have a unique `id` across the library if `id` is set (`ErrDuplicateRuleID`). A `nil` ID is the default in operator-authored static config.
- Pass `RuleContract.Validate()` — the per-rule semantic checks.

Cross-validation also enforces that every `useResiliencePolicy` action references a name that exists in the `resilience_policies` library (`ErrUnknownResilienceName`). Empty `policyName` is legal — it explicitly clears `state.PolicyRef` at runtime.

---

## `resilience_policies` block

`policy.yaml` carries `resilience_policies:` — the top-level resilience policy library, a flat list of `ResilienceConfig` entries. See [resilience.md](resilience.md) for the full schema (modes, targets, circuit breaker, per-target actions, observability).

Each policy must:

- Have a unique `name` across the library (`ErrDuplicateResilienceName`).
- Have a unique `id` across the library if `id` is set (`ErrDuplicateResilienceID`).
- Pass `ResilienceConfig.Validate()` — the per-policy semantic checks (mode/target consistency, weight validity, etc.).

A configuration that references a resilience policy must carry credentials for every provider that policy's targets name; the loader cross-checks this and aborts with `ErrTargetProviderMissingCredential` if not.

---

## `admin` block

`admin.yaml` carries `admin:` — the management-console gate. The file is **optional**; absent means the console never starts. When present and `enabled: true`, the gateway brings up a second `http.Server` bound to `bind_addr` serving the embedded SPA at `/` and the control-plane API under `/api/v1/*`.

```yaml
admin:
  enabled: true
  bind_addr: "0.0.0.0:8081"
  password: "operator-secret"
```

### `admin.Config` fields

| Field | Type | Required | Notes |
|---|---|---|---|
| `enabled` | bool | yes | Gates the admin listener. `false` = no second `http.Server`, no admin routes exist anywhere. |
| `bind_addr` | string | no | Listener address (host:port). Empty resolves to `0.0.0.0:8081`. Must not collide with the data-plane listener. Validated as host:port with numeric port (`ErrInvalidBindAddr`). |
| `password` | string | no | Operator credential used for HTTP Basic auth. Username is hardcoded as `admin` and is not configurable. May be empty in YAML — at runtime the `SLUICE_ADMIN_PASSWORD` env var wins when set; otherwise this field is used. With `enabled: true`, **either** the env var or this field must be set (`ErrPasswordRequired`). Never serialised to JSON. |

The console's runtime behaviour (live-feed capacity, body capture budget, snapshot interval) is configured via `SLUICE_ADMIN_*` env vars, not this block — see [environment-variables.md](environment-variables.md).

---

## The binding triangle

```mermaid
flowchart TB
    subgraph providersYaml [providers.yaml]
        P1[Provider:<br/>openai<br/>anthropic<br/>gemini<br/>...]
    end
    subgraph policyYaml [policy.yaml]
        K[APIKey<br/>secret + configuration]
        C[Configuration<br/>upstream_credentials<br/>rule_names<br/>resilience_name]
        R[rules library]
        RP[resilience_policies library]
        K -- configuration --> C
        C -- rule_names --> R
        C -- resilience_name --> RP
        C -- upstream_credentials keys --> P1
        RP -- targets[*].provider --> P1
    end
    Client[Client request] -- Authorization: sk_live_... --> K
    Client -- X-Sluice-Configuration<br/>passthrough --> C
```

What this enforces at load time:

- Every `api_keys[].configuration` resolves to a known configuration name.
- Every `configurations[].rule_names` entry resolves to a rule in the library.
- Every `configurations[].resilience_name` resolves to a policy in the library.
- Every target the bound policy names has a credential entry in the binding configuration's `upstream_credentials` map.
- Every `useResiliencePolicy` action in the rule library names a policy that exists.

What this gives the data plane post-load:

- `SecretIndex`: secret → `*APIKey` for O(1) auth lookup.
- `ConfigurationIndex`: configuration name → `*Configuration` for passthrough mode and post-auth re-binding.
- `RouteIndex`: fully-resolved URL path → (provider, endpoint) for routing.
- `PerConfigurationRules`: configuration name → ordered slice of `*RuleContract` (pointer-stable references into the library) for the rules engine.
- `ResilienceIndex`: policy name → `*ResilienceConfig` for the orchestrator.

---

## Worked examples

### Minimal `policy.yaml` that boots

The smallest configuration tree that satisfies validation: one provider (in `providers.yaml`), one configuration with one credential, one API key referencing it.

```yaml
# providers.yaml
providers:
  openai:
    base_url: "https://api.openai.com"
    prefix: "openai"
    endpoints:
      chat_completions:
        path: "/v1/chat/completions"
        method: ["POST"]
        accepted_paths: ["/v1/chat/completions"]
        accepts_streaming: true
        request_kind: "openai.chat"
```

```yaml
# policy.yaml
configurations:
  production:
    upstream_credentials:
      openai: sk-production-openai-key

api_keys:
  - secret: sk_live_minimal_demo_key
    name: "demo client"
    configuration: production
    enabled: true
```

No `rules`, no `resilience_policies`, no `admin.yaml`. The gateway will boot, accept managed-mode requests with `Authorization: Bearer sk_live_minimal_demo_key`, swap in `sk-production-openai-key`, and forward to `https://api.openai.com/v1/chat/completions`.

### Same configuration split across the three files

```yaml
# providers.yaml
providers:
  openai:
    base_url: "https://api.openai.com"
    prefix: "openai"
    prefix_required: false
    endpoints:
      chat_completions:
        path: "/v1/chat/completions"
        method: ["POST"]
        accepted_paths: ["/v1/chat/completions"]
        accepts_streaming: true
        request_kind: "openai.chat"
      models:
        path: "/v1/models"
        method: ["GET"]
        accepted_paths: ["/v1/models"]
        request_kind: "openai.models"

  anthropic:
    base_url: "https://api.anthropic.com"
    prefix: "anthropic"
    prefix_required: true
    required_headers:
      anthropic-version: "2023-06-01"
    endpoints:
      messages:
        path: "/v1/messages"
        method: ["POST"]
        accepted_paths: ["/v1/messages"]
        accepts_streaming: true
        request_kind: "anthropic.messages"
        prefix_optional: true
      chat_completions:
        path: "/v1/chat/completions"
        method: ["POST"]
        accepted_paths: ["/v1/chat/completions"]
        accepts_streaming: true
        request_kind: "openai.chat"
        auth_header: "Authorization"
        auth_format: "Bearer {key}"
```

```yaml
# policy.yaml
configurations:
  production:
    upstream_credentials:
      openai: sk-mock-openai
      anthropic: sk-ant-mock-anthropic
    rule_names:
      - route-claude-models-to-anthropic
    resilience_name: high-availability
    tags:
      tier: production

api_keys:
  - secret: sk_live_alpha_internal_pipeline
    name: "internal-dev pipeline"
    configuration: production
    enabled: true

rules:
  - name: route-claude-models-to-anthropic
    condition:
      type: modelName
      operator: StartsWith
      expectedModelName: claude-
    actions:
      - type: changeProvider
        newProvider: anthropic
    behavior: continue

resilience_policies:
  - name: high-availability
    mode: failover
    failure_status_codes: [502, 503, 504]
    targets:
      - name: openai-primary
        provider: openai
        order: 1
      - name: anthropic-fallback
        provider: anthropic
        order: 2
        actions:
          - type: changeModelName
            newModelName: claude-3-5-sonnet
```

```yaml
# admin.yaml (optional)
admin:
  enabled: true
  bind_addr: "0.0.0.0:8081"
  password: "operator-secret"
```

What this gives you: openai is the bare-routable default (so `POST /v1/chat/completions` lands on openai); anthropic is prefix-required (so `/anthropic/v1/messages` is reachable) but its native `/v1/messages` is bare-routable too via `prefix_optional`; the `claude-*` model rewrites to anthropic with the resilience policy carrying both providers as failover targets.

---

## Why no `${VAR}` substitution

The loader does not expand `${VAR}` or any `env:` syntax inside the YAML tree. Decoded values are passed through verbatim. This is deliberate.

- **File contents are trusted by construction.** In production the config directory is mounted from a Kubernetes Secret (or a filesystem-permissioned dir on bare metal); the operator picks the substrate and Sluice trusts what's on disk.
- **One source of truth per secret.** A `${OPENAI_KEY}` literal in YAML would mean the file is meaningless without the env, and the env var becomes the de-facto source of truth — but it's not what the admin console shows, what the bundler exports, or what the validator sees. Keeping credentials as literal strings inside the YAML keeps the YAML the canonical artefact.
- **Only file paths are env-overridable.** The location of the config directory is the one piece of dynamic input the gateway tolerates — `SLUICE_CONFIG_DIR` selects the dir. Everything inside the dir is read as-is.

The two intentional exceptions are `SLUICE_ADMIN_PASSWORD` (kept out of YAML for production-prod hygiene) and the server-level `SLUICE_*` env vars (which are not in YAML at all). See [environment-variables.md](environment-variables.md).

---

## Validation errors

Every cross-block invariant the loader enforces, and the sentinel each violation wraps:

| Sentinel | Triggered by |
|---|---|
| `ErrEmptyDirectory` | Config dir has no accepted files. |
| `ErrUnexpectedConfigFile` | A filename other than `providers.yaml`, `policy.yaml`, or `admin.yaml` exists in the dir. |
| `ErrWrongFileForKey` | A top-level key appears in a file whose allow-list does not include it (e.g., `providers:` inside `policy.yaml`). |
| `ErrParse` | A YAML file is malformed, or a top-level node decode failed. |
| `ErrNoConfigurations` | The merged tree has zero entries under `configurations`. |
| `ErrUnknownConfiguration` | An `api_keys[]` entry names a configuration that does not exist. |
| `ErrUnknownRuleName` | A configuration's `rule_names[]` names a rule not in the library. |
| `ErrDuplicateRuleName` | Two rules in the library share the same `name`. |
| `ErrDuplicateRuleID` | Two rules in the library share the same non-nil `id`. |
| `ErrUnknownResilienceName` | A configuration's `resilience_name` or a `useResiliencePolicy` action names a policy not in the library. |
| `ErrDuplicateResilienceName` | Two policies in the library share the same `name`. |
| `ErrDuplicateResilienceID` | Two policies in the library share the same non-nil `id`. |
| `ErrTargetProviderMissingCredential` | A configuration binds a resilience policy whose target names a provider absent from the configuration's `upstream_credentials`. |
| `ErrPathCollision` | Two (provider, endpoint, accepted_path) emissions resolve to the same final URL path. |
| `ErrPrefixRequiredEmpty` | A provider has `prefix_required: true` but no `prefix`. |
| `ErrAuthFormatWithoutHeader` | `auth_format` is set on a provider or endpoint that does not also set `auth_header`. |
| `ErrInvalidAuthFormat` | `auth_format` does not contain exactly one `{key}` placeholder. |

The admin block's `ErrPasswordRequired` and `ErrInvalidBindAddr` propagate from `admin.Config.Validate` through `ResolvedConfig.Validate`.

---

## Known limitations

These are documented intentionally — don't fix them without checking the milestone first.

1. **No hot reload in v1.0 / v1.1 / v1.2.** Restart the gateway to apply config changes. The `ResolvedConfig` is constructed once at startup and held read-only thereafter. v1.3+ adds an `fsnotify`-based reload path that swaps the indexed view atomically without dropping in-flight requests.

2. **No `${VAR}` substitution.** See [Why no `${VAR}` substitution](#why-no-var-substitution). The one operator escape hatch is `SLUICE_ADMIN_PASSWORD`, which overrides `admin.password` at runtime.

3. **The filename allow-list is closed.** You cannot add a fourth file (e.g., `experiments.yaml`) and have the loader pick it up. The merge model assumes every key has exactly one canonical home; adding files means revising the allow-list in `internal/config/loader.go`.

4. **Configuration evaluation order is YAML list order.** `Configuration.RuleNames` is a list, not a priority-keyed map — the order in which the operator wrote the names is the order the engine evaluates them. There is no separate priority field.

5. **API keys are flat — no per-key rules or resilience.** All policy lives on the named configuration. To give two keys different behaviour, point them at two different configurations.

6. **Pointer stability across reload boundaries is not yet a contract.** `ConfigurationIndex` is built from copies so downstream code may retain pointers within one load lifetime, but until reload exists, the question doesn't come up. When v1.3+ ships, the swap will be atomic with the existing pointers staying valid for in-flight requests.

---

## See also

- [providers.md](providers.md) — per-provider deep dive (OpenAI / Anthropic / Gemini, OpenAI-compat surfaces, model-keyed redirect patterns).
- [rules.md](rules.md) — rule grammar (conditions, actions, evaluator algorithm).
- [resilience.md](resilience.md) — resilience policy schema (modes, targets, circuit breaker, observability).
- [environment-variables.md](environment-variables.md) — every `SLUICE_*` env var (server bind, NATS, OTel, admin runtime knobs).
