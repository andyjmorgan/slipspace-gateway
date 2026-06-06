# Authentication

Sluice runs two auth schemes side-by-side on the same listeners. **Managed mode** swaps a gateway-issued bearer token for the upstream provider's real credential before forwarding. **Passthrough mode** lets a client carry their own upstream credential through unchanged while still picking up rules, resilience, and observability. Both schemes resolve to a named **Configuration** — the policy bundle the rest of the pipeline runs against.

This page is the operator's reference. It covers the resolution algorithm, every header the gateway reads or strips, how the outbound upstream credential is minted, and worked examples for both modes.

---

## Table of contents

1. [Mental model](#mental-model)
2. [Resolution flow](#resolution-flow)
3. [Managed mode](#managed-mode)
4. [Passthrough mode](#passthrough-mode)
5. [Inbound headers](#inbound-headers)
6. [Outbound credential headers](#outbound-credential-headers)
7. [Worked example: managed](#worked-example-managed)
8. [Worked example: passthrough](#worked-example-passthrough)
9. [Why passthrough exists](#why-passthrough-exists)
10. [Operational concerns](#operational-concerns)
11. [Cross-references](#cross-references)

---

## Mental model

> **Two modes, one resolution surface. Managed substitutes credentials; passthrough forwards them. Both bind the request to a Configuration.**

A request reaches the gateway with some combination of these signals:

- The `X-Sluice-Identity` header — carries a Sluice-issued api-key secret that *selects* a Configuration (via the matching key's `configuration:`) **without** swapping the upstream credential. This is the preferred passthrough selector.
- The `X-Sluice-Configuration` header — names a Configuration directly. **Deprecated** in favour of `X-Sluice-Identity`: a configuration name is human-readable and guessable, whereas an api-key secret is not.
- An `Authorization: Bearer <token>` header — may carry a Sluice-issued secret (managed) or an upstream provider token (passthrough).
- A provider-native credential header — `x-api-key` (Anthropic SDKs) or `x-goog-api-key` (Gemini SDKs).

The auth middleware ([`internal/middleware/auth/resolver.go`](../internal/middleware/auth/resolver.go)) inspects those signals and resolves to one of two outcomes:

| Mode | Inbound signal | Outbound credential |
|---|---|---|
| `managed` | A Sluice-issued secret discovered on `Authorization`, `x-api-key`, or `x-goog-api-key` | Minted by the destination builder from the resolved selection target's credential (sourced from `Configuration.Credentials[provider]`). |
| `passthrough` | A passthrough selector present — `X-Sluice-Identity: <sluice-secret>` (preferred) or `X-Sluice-Configuration: <name>` (deprecated) — while the client carries their own upstream token on `Authorization` | The inbound `Authorization` value, forwarded verbatim. |

Resolution decides which **policy** runs — the Configuration's rules, resilience policy, tags. Resolution does *not* mint the upstream credential. That happens later in the destination builder ([`cmd/gateway/destination.go::buildDestination`](../cmd/gateway/destination.go)), where the resolved selection target's auth convention is applied at the single credential mint site, [`credentialHeaderFor`](../cmd/gateway/destination.go). Keeping identity-resolution separate from credential-injection is what keeps the credential mint site in one place (invariant 6 in [`CLAUDE.md`](../CLAUDE.md)). In the v2 config model the routing decision — which provider/endpoint a request lands on — is made by the selection module ahead of the destination builder, not by mid-pipeline rule actions.

---

## Resolution flow

```mermaid
flowchart TB
    Start[Client request enters auth middleware] --> IdHdr{X-Sluice-Identity<br/>present?}
    IdHdr -- yes --> IdLookup[lookup secret in SecretIndex]
    IdLookup --> IdFound{secret known<br/>and enabled?}
    IdFound -- no --> Err401id[401 unauthorized]
    IdFound -- yes --> IdCfg[lookup Configuration by<br/>key.Configuration]
    IdCfg --> IdCfgFound{configuration<br/>known?}
    IdCfgFound -- no --> Err403id[403 unknown configuration]
    IdCfgFound -- yes --> PassResultId[AuthResult mode=passthrough<br/>APIKey set<br/>LegacyConfigurationHeader=X-Sluice-Configuration also present?<br/>DropHeaders=both selector headers]
    IdHdr -- no --> CfgHdr{X-Sluice-Configuration<br/>present?}
    CfgHdr -- yes --> CfgLookup[lookup Configuration by name]
    CfgLookup --> CfgFound{configuration<br/>known?}
    CfgFound -- no --> Err403[403 unknown configuration]
    CfgFound -- yes --> PassResult[AuthResult mode=passthrough<br/>APIKey=nil<br/>LegacyConfigurationHeader=true<br/>DropHeaders=both selector headers]
    CfgHdr -- no --> Discover[walk inbound headers:<br/>Authorization Bearer<br/>then x-api-key<br/>then x-goog-api-key]
    Discover --> Found{first non-empty<br/>header parsed?}
    Found -- no --> Err401a[401 unauthorized<br/>missing bearer]
    Found -- yes --> SecretLookup[lookup token in SecretIndex]
    SecretLookup --> SecretFound{secret<br/>known?}
    SecretFound -- no --> Err401b[401 unauthorized<br/>unknown key]
    SecretFound -- yes --> Enabled{key.Enabled?}
    Enabled -- no --> Err401c[401 unauthorized<br/>disabled key]
    Enabled -- yes --> CfgByKey[lookup Configuration by<br/>key.Configuration]
    CfgByKey --> CfgFound2{configuration<br/>known?}
    CfgFound2 -- no --> Err403b[403 unknown configuration]
    CfgFound2 -- yes --> ManagedResult[AuthResult mode=managed<br/>APIKey set<br/>DropHeaders=source header<br/>+ both selector headers]
    PassResultId --> Forward[forward to bodycapture + rules + forwarder]
    PassResult --> Forward
    ManagedResult --> Forward
```

Three rules to internalise:

1. **Either passthrough selector takes precedence over any bearer.** If `X-Sluice-Identity` or `X-Sluice-Configuration` is present, resolution is passthrough — the `Authorization` bearer is forwarded verbatim, never looked up as a managed key. This is by design: callers carrying a selector are explicitly asking to keep their own upstream credential and let the gateway pick the policy. Document it for clients so a managed-mode key doesn't silently turn into a passthrough resolution because they added a selector header by accident.
2. **Between the two selectors, `X-Sluice-Identity` wins.** When both are present, the identity secret resolves the Configuration and `AuthResult.LegacyConfigurationHeader` is set so the HTTP handler can emit a deprecation warning — the signal an operator watches to find callers still on the guessable `X-Sluice-Configuration`. An unknown or disabled identity secret fails **401**, not 403, so an attacker cannot probe configuration names by presenting random identity values (contrast the legacy header, which 403s on an unknown name and so leaks name validity).
3. **Managed-mode key discovery walks one header at a time and stops at the first present.** Authorization Bearer wins, then `x-api-key`, then `x-goog-api-key`. If the first present header carries a value that is *not* a known Sluice secret, resolution short-circuits to 401 rather than falling through — an attacker cannot stuff multiple headers to confuse the resolver.

---

## Managed mode

Managed mode is the production path. The client uses a Sluice-issued bearer (conventionally `sk_live_...`), the gateway swaps it for the real upstream credential before forwarding.

### Wire flow

```mermaid
flowchart LR
    A[Client] -- Authorization: Bearer sk_live_xxx --> B[gateway:8585]
    B --> C[auth middleware]
    C -- lookup secret<br/>resolve Configuration --> D[bodycapture + rules + resilience]
    D --> E[destination builder]
    E -- mints upstream credential header<br/>per provider+endpoint --> F[forwarder]
    F -- Authorization: Bearer real-key<br/>OR x-api-key: real-key<br/>OR x-goog-api-key: real-key --> G[upstream]
```

### Algorithm

1. The auth middleware walks `Authorization` → `x-api-key` → `x-goog-api-key` looking for a Sluice secret. The first present header wins; if its value is unknown, resolution fails 401 without falling through.
2. The matched `APIKey` is looked up in `SecretIndex`. If `Enabled` is false, resolution fails 401.
3. `APIKey.Configuration` names a Configuration. If the name is missing from `ConfigurationIndex`, resolution fails 403 with `unknown configuration`.
4. `AuthResult.DropHeaders` is seeded with **both** selector headers (`X-Sluice-Identity`, `X-Sluice-Configuration` — always, they are policy-routing metadata never forwarded) plus the source header the Sluice secret was discovered on. The Sluice secret never leaves the gateway.
5. Downstream of auth, the destination builder ([`cmd/gateway/destination.go::buildDestination`](../cmd/gateway/destination.go)) reads the credential the selection module already resolved onto `target.Credential` (sourced from `Configuration.Credentials[provider]`) and mints the outbound header via [`credentialHeaderFor`](../cmd/gateway/destination.go) — see [Outbound credential headers](#outbound-credential-headers).

### Failure modes

| Inbound state | Wire response | `result` tag in log | Notes |
|---|---|---|---|
| No Authorization, no x-api-key, no x-goog-api-key | 401 `unauthorized` | `unknown_key` | Identical wire shape to "unknown secret" — the gateway refuses to reveal whether a key exists. |
| Authorization without `Bearer ` prefix (malformed) | 401 `unauthorized` | `unknown_key` | `extractBearer` parses case-insensitively per RFC 7235 §2.1. A header that does not start with `Bearer ` produces a discovery miss, same wire shape as a missing header. |
| Bearer value not in SecretIndex | 401 `unauthorized` | `unknown_key` | |
| Bearer value matches a key with `enabled: false` | 401 `unauthorized` | `disabled_key` | The audit log records `disabled_key` so an operator chasing a rejected client can tell the difference from "unknown secret" — the wire shape stays identical to avoid confirming key existence. |
| Bearer resolves to an `APIKey` whose `configuration:` field references a name absent from the index | 403 `unknown configuration` | `unknown_configuration` | Only fires under load-time validation skew — the loader rejects this at startup. A live 403 here implies a config swap that the loader didn't validate. |
| Bearer resolves cleanly but the resolved target's credential is empty (`Configuration.Credentials[provider]` absent or `""`) | request forwarded with **no credential header** (no-credential branch, [`destination.go:137`](../cmd/gateway/destination.go)) | `success` | Auth succeeds; the destination builder strips every credential header and forwards without one. Use for private endpoints that gate themselves (e.g. in-cluster ollama on an unauthenticated NodePort). |

The disabled-key vs unknown-key distinction lives only in the structured log — the wire collapses both to 401 to deny enumeration. See `classifyResult` in [`internal/middleware/auth/auth.go`](../internal/middleware/auth/auth.go).

---

## Passthrough mode

Passthrough mode is the BYOK (bring-your-own-key) path. The client carries their own upstream token; the gateway picks the policy via a passthrough **selector** and forwards the `Authorization` header verbatim. The upstream rejects bad tokens — the gateway does no upstream-token validation for passthrough.

There are two selectors:

- **`X-Sluice-Identity` (preferred)** — carries a Sluice-issued api-key secret. The matching key's `configuration:` field picks the Configuration. The secret is looked up in `SecretIndex` and must resolve to an *enabled* key, so an unknown or disabled value fails **401** — a configuration name is never echoed back, and cannot be probed. `AuthResult.APIKey` is set on this path, so the request carries a per-client identity for audit even though credentials are still forwarded verbatim.
- **`X-Sluice-Configuration` (deprecated)** — names a Configuration directly. No key lookup; an unknown name fails **403 `unknown configuration`**, which leaks name validity. Retained for backward compatibility; new integrations should use `X-Sluice-Identity`.

When both are present, `X-Sluice-Identity` wins and `AuthResult.LegacyConfigurationHeader` is set so the handler logs a deprecation warning.

### Wire flow

```mermaid
flowchart LR
    A[Client] -- X-Sluice-Identity: sk_live_xxx<br/>Authorization: Bearer their-own-token --> B[gateway:8585]
    B --> C[auth middleware]
    C -- resolve key → Configuration<br/>no credential swap --> D[bodycapture + rules + resilience]
    D --> E[destination builder]
    E -- passthrough branch:<br/>re-inject inbound Authorization --> F[forwarder]
    F -- Authorization: Bearer their-own-token<br/>both selector headers STRIPPED --> G[upstream]
```

### Algorithm

1. The auth middleware reads `X-Sluice-Identity` first, trimming whitespace. A non-empty value is looked up in `SecretIndex`; the resolved key must be enabled, and its `configuration:` must name a known Configuration. Failures are 401 (unknown/disabled secret) or 403 (key references a missing configuration — a load-time validation skew). `AuthResult` is built with `Mode=passthrough` and `APIKey` set.
2. Otherwise the middleware reads `X-Sluice-Configuration` and trims whitespace. Any non-empty value forces legacy passthrough regardless of any bearer also on the request. The name is looked up in `ConfigurationIndex`; if absent, resolution fails 403 `unknown configuration`. `AuthResult` is built with `Mode=passthrough`, `APIKey=nil`, `LegacyConfigurationHeader=true`.
3. Either path leaves no managed-mode api_key swap — the upstream owns credential validation. `DropHeaders` always includes **both** selector headers (`X-Sluice-Identity`, `X-Sluice-Configuration`) regardless of which one drove resolution.
4. Downstream, the destination builder takes the passthrough branch of `resolveCredentialHeaders` ([`destination.go:194`](../cmd/gateway/destination.go)): the forwarder's `alwaysDropHeaders` strips inbound `Authorization` unconditionally, and the destination builder re-injects the inbound value via `OutgoingHeaders` so the upstream still sees it. The branch is reached when no `changeApiKey` override is set; a `changeApiKey` override is checked first and wins (see the [`changeApiKey` note](#outbound-credential-headers) below — the action is wired in the v2 destination builder).

### Failure modes

| Inbound state | Wire response | `result` tag in log |
|---|---|---|
| `X-Sluice-Identity` value not a known/enabled secret | 401 `unauthorized` | `unknown_key` / `disabled_key` (collapsed to 401 on the wire) |
| `X-Sluice-Identity` valid but the key's `configuration:` is absent from `ConfigurationIndex` | 403 `unknown configuration` | `unknown_configuration` — load-time validation skew only |
| Both selector headers present | `X-Sluice-Identity` resolves; `LegacyConfigurationHeader` set → deprecation warning logged | (identity-path result tags) |
| `X-Sluice-Configuration: ""` (empty / whitespace-only) and no identity header | falls through to managed-mode discovery; whitespace is trimmed in resolution | (managed-mode result tags) |
| `X-Sluice-Configuration` names a Configuration absent from `ConfigurationIndex` | 403 `unknown configuration` | `unknown_configuration` |
| Valid selector, no `Authorization` header | request forwarded with no `Authorization`; upstream returns its own auth error | `success` |
| Valid selector + bogus `Authorization` value | forwarded verbatim; upstream returns 401/403 | `success` at the gateway layer — auth resolution succeeded, the upstream's rejection is its own concern. |

Passthrough resolution does no rate-limiting and no upstream-token validation. With `X-Sluice-Identity` it *does* carry a per-client `APIKey` identity for audit; the legacy header carries none. The Configuration is the policy knob either way; auditing a passthrough request through the rule chain is the audit story.

---

## Inbound headers

Every header the data plane reads from the client.

| Header | Read by | Effect | Forwarded upstream? |
|---|---|---|---|
| `Authorization` | auth middleware (managed discovery, passthrough verbatim forward) | Managed: parsed as `Bearer <token>`, looked up in `SecretIndex`. Passthrough: forwarded verbatim to upstream. | **Never verbatim in managed mode** — stripped via `DropHeaders` and the destination builder mints a fresh credential. **Forwarded verbatim in passthrough mode** — re-injected through `OutgoingHeaders` because the forwarder's `alwaysDropHeaders` strips inbound `Authorization` unconditionally. |
| `x-api-key` | auth middleware (managed discovery, second-fallback) | Managed: discovered as the Sluice secret if `Authorization` was absent. Used by vanilla Anthropic SDKs that don't speak Bearer. | Never in managed mode — stripped via `DropHeaders` and the destination builder mints the per-provider credential header. The destination builder also strips it on the cross-provider path so an inbound `x-api-key` cannot leak to OpenAI. |
| `x-goog-api-key` | auth middleware (managed discovery, third-fallback) | Managed: discovered as the Sluice secret if `Authorization` and `x-api-key` were absent. Used by vanilla Gemini SDKs. | Never in managed mode — same stripping as `x-api-key`. |
| `X-Sluice-Identity` | auth middleware | **Preferred passthrough selector.** Carries a Sluice-issued api-key secret; the matching enabled key's `configuration:` picks the Configuration, with credentials still forwarded verbatim. Unknown/disabled secret → 401 (no name leak). Trimmed for whitespace. Its value is a credential, so the built-in log/record redactor masks it by default (matches the `sluice-identity` substring — see [environment-variables.md](environment-variables.md)). | **Never.** Always stripped — `passthroughDropHeaders` blacklists it (and `X-Sluice-Configuration`) in every passthrough resolution, and the managed path drops it too. |
| `X-Sluice-Configuration` | auth middleware | **Deprecated passthrough selector** (use `X-Sluice-Identity`). When present and non-empty and no identity header is set, forces legacy passthrough and names the Configuration directly. Unknown name → 403, which leaks name validity. Trimmed for whitespace. When it co-exists with `X-Sluice-Identity`, the identity header wins and a deprecation warning is logged. | **Never.** Always stripped from the outbound request — both modes append it to `DropHeaders` unconditionally. It is policy-routing metadata, not credential, and leaking it upstream confuses providers that reject unknown `X-*` headers. |
| `X-Sluice-Correlation-Id` | [`cmd/gateway/correlation.go`](../cmd/gateway/correlation.go) | When present, becomes the request's correlation ID. When absent, the gateway generates one. Echoed on the **response** so the client can stitch logs end-to-end. | Forwarded upstream as part of the inbound request unless a rule strips it via `setHeader`. The upstream typically ignores it. |
| `X-Sluice-Session-Id` | correlation middleware (echoes only) + mock LLM (scenario keying) | Optional client-supplied session identifier. Echoed on the response when present so the client can correlate multi-turn flows. The mock LLM uses it to key session-scoped scenarios during E2E tests. | Forwarded upstream as part of the inbound request. |
| `X-Sluice-Agent-Id`, `X-Claude-Code-Agent-Id` | correlation middleware (echoes only) | Optional client-supplied agent identifier (the agent/sub-agent that issued the request). `X-Sluice-Agent-Id` is authoritative; `X-Claude-Code-Agent-Id` is the shipped fallback, plus any `SLUICE_AGENT_ID_HEADERS` extras. The resolved id is echoed on the response under `X-Sluice-Agent-Id`. See [observability.md → Agent id](observability.md#agent-id). | Forwarded upstream as part of the inbound request. |
| `Origin`, `Referer`, `Cookie` | forwarder `alwaysDropHeaders` | Browser-session state the gateway has no use for. Stripped in both modes — Anthropic in particular rejects requests carrying a browser `Origin` for organisations with custom retention policy. | **Never.** |
| `Accept-Encoding` | forwarder `alwaysDropHeaders` | Stripped at the inbound edge so upstreams return uncompressed bodies. The admin live-messages capture tees response bytes that flow to the client; compressed bytes render as binary garbage in the viewer. | **Never.** |

Anything else the client sends is forwarded verbatim unless a rule strips it via `setHeader: action=remove`. Rules win the last word on the wire — see [`docs/actions.md`](actions.md).

---

## Outbound credential headers

In managed mode, the destination builder mints exactly one credential header per request via [`credentialHeaderFor`](../cmd/gateway/destination.go) (`cmd/gateway/destination.go:163`). The header name and value format are read off the resolved selection target's `Auth` convention, with a per-provider default fallback:

1. `target.Auth.Header` + `target.Auth.Format` (if `target.Auth != nil` and `Header` is set) — the format string's `{key}` placeholder is replaced with the credential.
2. `target.Auth.Header` + the raw credential (if `Format` is empty).
3. Per-provider default from [`auth.UpstreamCredentialHeader`](../internal/middleware/auth/resolver.go) when `target.Auth` is nil or carries no header.

`target.Auth` is the protocol/provider auth convention the **selection module** resolves at request top, ahead of the destination builder — so the endpoint vs provider override precedence is settled before `credentialHeaderFor` ever runs. The full override stack and worked examples live in [`docs/providers.md#per-protocol-auth-x-api-key-vs-bearer`](providers.md#per-protocol-auth-x-api-key-vs-bearer). The per-provider defaults applied when no override is in effect:

| Provider name (literal match) | Default header | Default value | Source |
|---|---|---|---|
| `openai` | `Authorization` | `Bearer <credential>` | [`auth.UpstreamCredentialHeader`](../internal/middleware/auth/resolver.go) |
| `anthropic` | `x-api-key` | `<credential>` (raw) | [`auth.UpstreamCredentialHeader`](../internal/middleware/auth/resolver.go) |
| `gemini` | `x-goog-api-key` | `<credential>` (raw) | [`auth.UpstreamCredentialHeader`](../internal/middleware/auth/resolver.go) |
| anything else | `Authorization` | `Bearer <credential>` | fallback branch in `UpstreamCredentialHeader` so retargeting a rule to an as-yet-unmodelled provider still produces a reasonable outgoing shape. |

`UpstreamCredentialHeader` is exported so the rules engine's `changeApiKey` action can re-mint a credential header without duplicating the format table. **Note:** in the v2 destination builder `changeApiKey` is wired — the action writes `state.UpstreamCredentialOverride` ([`internal/middleware/rules/actions.go:154`](../internal/middleware/rules/actions.go)), and `resolveCredentialHeaders` ([`cmd/gateway/destination.go:169`](../cmd/gateway/destination.go)) reads it at the single mint site, ahead of the auth mode: a non-empty literal `apiKey` is minted with the post-rule provider's header format (formatting through `credentialHeaderFor` → `UpstreamCredentialHeader`), while the `useSluiceKey` sentinel forwards the inbound `Authorization` verbatim. See [Cross-references](#cross-references).

The destination builder defends the credential surface beyond just minting the right header. There is no named "strategy" type in the code; the logic is a precedence `switch` in [`resolveCredentialHeaders`](../cmd/gateway/destination.go) (`cmd/gateway/destination.go:169`), into which `buildDestination` threads the post-rule `state.UpstreamCredentialOverride`. It is evaluated top to bottom:

| Branch (`destination.go`) | When | Effect on outbound headers |
|---|---|---|
| override literal — line 178 (`override != nil && *override != ""`) | A `changeApiKey` rule set a literal `apiKey` (wins over the auth mode) | Mints the override key via `credentialHeaderFor` using the **post-rule** provider's header format; adds every *other* credential header name in the closed set to `DropHeaders`. |
| override sentinel — line 188 (`override != nil`, empty string) | A `changeApiKey` rule set `useSluiceKey: true` | If the inbound request carried an `Authorization` header, re-injects that value verbatim via `OutgoingHeaders`; strips nothing else. |
| passthrough — line 194 (`mode == auth.ModePassthrough`) | Passthrough mode, no `changeApiKey` override | If the inbound request carried an `Authorization` header, re-injects that value verbatim via `OutgoingHeaders`. (The forwarder's `alwaysDropHeaders` had stripped it on the inbound edge.) No credential is minted; the upstream owns validation. |
| no-credential — line 198 (`target.Credential == ""`) | Managed mode, no override, where the resolved target has no credential (`Configuration.Credentials[provider]` absent or `""`) | Adds every credential header name in the closed set (`Authorization`, `X-Api-Key`, `X-Goog-Api-Key`) to `DropHeaders`; sets none. The upstream sees no credential — appropriate for endpoints the gateway is not authenticated against. |
| credential-set — line 203 (`default`) | Managed mode, no override, with a non-empty `target.Credential` | Mints the header via `credentialHeaderFor(target, target.Credential)`; adds every *other* credential header name in the closed set to `DropHeaders` so an inbound openai-style Bearer cannot leak to anthropic. |

The closed set `credentialHeaderNames` (`Authorization`, `X-Api-Key`, `X-Goog-Api-Key`) is defined in [`cmd/gateway/handler.go:148`](../cmd/gateway/handler.go).

---

## Worked example: managed

Client hits `/openai/v1/chat/completions` with a Sluice-issued bearer.

### Inbound

```http
POST /openai/v1/chat/completions HTTP/1.1
Host: sluice.example.com
Authorization: Bearer sk_live_acme_prod_42
Content-Type: application/json

{"model": "gpt-4o-mini", "messages": [...]}
```

### Resolution

1. Routing maps `/openai/v1/chat/completions` to `(provider=openai, endpoint=chat_completions)`.
2. Auth middleware: `X-Sluice-Configuration` absent → managed-mode discovery.
3. `Authorization` parses as `Bearer sk_live_acme_prod_42`.
4. `SecretIndex` lookup returns:
   ```yaml
   - secret: sk_live_acme_prod_42
     name: acme-prod
     configuration: production
     enabled: true
   ```
5. `ConfigurationIndex["production"]` returns:
   ```yaml
   credentials:
     openai: sk-real-openai-pk-...
   ```
6. `AuthResult`:
   - `Mode = managed`
   - `APIKey.Name = "acme-prod"`
   - `Configuration` = the `production` bundle
   - `DropHeaders = ["X-Sluice-Identity", "X-Sluice-Configuration", "Authorization"]`

### Destination build

1. `buildDestination` falls to the credential-set branch (`destination.go:142` default — managed mode, `target.Credential` non-empty).
2. `credentialHeaderFor` sees `target.Auth == nil` (no openai protocol auth override) → falls back to `auth.UpstreamCredentialHeader("openai", "sk-real-openai-pk-...")` → returns `("Authorization", "Bearer sk-real-openai-pk-...")`.
3. The closed credential-header set adds `X-Api-Key` and `X-Goog-Api-Key` to `DropHeaders` (no-ops here since the inbound carried neither).

### Outbound

```http
POST /v1/chat/completions HTTP/1.1
Host: api.openai.com
Authorization: Bearer sk-real-openai-pk-...
Content-Type: application/json

{"model": "gpt-4o-mini", "messages": [...]}
```

The Sluice secret `sk_live_acme_prod_42` is dropped before forwarding. The OpenAI credential is fresh on the outbound. `X-Sluice-Configuration` was never present, and would have been stripped anyway.

---

## Worked example: passthrough

Claude Code is configured to point its OAuth-issued upstream Anthropic token at the gateway, with `X-Sluice-Configuration: code-assistants` so the gateway can still apply rules and observability.

> This example uses the **legacy** `X-Sluice-Configuration` selector to show the credential-forwarding mechanics. The modern equivalent sends `X-Sluice-Identity: <sluice-secret>` instead — the resolution is identical except the Configuration is reached via the key's `configuration:` (so `APIKey` is set, an unknown value 401s rather than 403s, and `DropHeaders` carries both selector headers).

### Inbound

```http
POST /anthropic/v1/messages HTTP/1.1
Host: sluice.example.com
X-Sluice-Configuration: code-assistants
Authorization: Bearer sk-ant-oauth-USER-OWNED-TOKEN-...
anthropic-version: 2023-06-01
Content-Type: application/json

{"model": "claude-3-5-sonnet", "messages": [...]}
```

### Resolution

1. Routing maps `/anthropic/v1/messages` to `(provider=anthropic, endpoint=messages)`.
2. Auth middleware: `X-Sluice-Configuration: code-assistants` is non-empty → **passthrough wins** even if the bearer also happened to be a known Sluice secret.
3. `ConfigurationIndex["code-assistants"]` returns the `code-assistants` bundle (rules, resilience, tags).
4. `AuthResult`:
   - `Mode = passthrough`
   - `APIKey = nil`
   - `Configuration` = the `code-assistants` bundle
   - `DropHeaders = ["X-Sluice-Identity", "X-Sluice-Configuration"]` (both selector headers, always)

### Destination build

1. `buildDestination` takes the passthrough branch (`destination.go:133`, `mode == auth.ModePassthrough`).
2. The forwarder's `alwaysDropHeaders` strips inbound `Authorization`.
3. The destination builder re-injects the inbound `Authorization` value into `OutgoingHeaders`, so the forwarder sets it back on the outbound request.
4. `X-Sluice-Configuration` is stripped via `DropHeaders` — the upstream never sees it.

### Outbound

```http
POST /v1/messages HTTP/1.1
Host: api.anthropic.com
Authorization: Bearer sk-ant-oauth-USER-OWNED-TOKEN-...
anthropic-version: 2023-06-01
Content-Type: application/json

{"model": "claude-3-5-sonnet", "messages": [...]}
```

The upstream sees the user's own Anthropic OAuth token. The gateway still applied every rule in the `code-assistants` Configuration, published a `gateway.request` event with `mode=passthrough`, and surfaced the request on the live-messages pane. The credential lifecycle stays with Claude Code — Sluice never sees its refresh, never minted it, never could substitute for it.

---

## Why passthrough exists

Some clients carry their own upstream credential lifecycle the gateway cannot substitute for:

- **Claude Code (Anthropic OAuth).** The CLI refreshes a short-lived OAuth token against Anthropic's authorization server; Sluice has no participation in that flow and no way to mint an equivalent token.
- **Vendor SDKs configured against a customer's own provider account.** When the credential belongs to the customer's account, Sluice cannot meaningfully proxy "their" key for them.
- **BYOK customer onboarding.** A customer evaluating Sluice without provisioning a Sluice-issued key first can still get rules / resilience / observability by setting `X-Sluice-Configuration` and forwarding their own bearer.

Passthrough is the answer to "I want sluice on the request path without surrendering my credential to it." The gateway gets to apply policy and emit telemetry; the credential never enters `SecretIndex`, never appears in the admin console's reveal endpoint, never lives anywhere on the gateway side.

It is also the **fallback** when a request needs the gateway's pipeline applied to a request whose credential the operator does not control. If you find yourself wanting to "let this one slip through with my own key", you want passthrough.

---

## Operational concerns

### Credential redaction in logs

The gateway never logs the literal `Authorization` value, nor any `x-api-key` or `x-goog-api-key`. The auth middleware logs `mode`, `api_key_id` (the key's `name` field, not its secret), `configuration`, `provider`, `endpoint`, `result`. The forwarder logs upstream URL and status but never request or response bodies — bodies flow to configured `connector_bindings` under each destination's own retention policy, and admin console body-capture is gated by `gateway.admin.enabled` and Basic auth.

Upstream credentials minted by the destination builder also never reach logs — they live on `Configuration.Credentials` in memory, are read once per request (resolved onto the selection target's `Credential`), and are injected into the outbound header without ever crossing the slog channel.

If you see a Sluice secret in the log stream, you have a bug. Open an issue.

### API key reveal endpoint

The admin console exposes a per-key reveal endpoint at `GET /admin/api/v1/config/api-keys/reveal?configuration=<name>&name=<key-name>` ([`docs/admin-console.md`](admin-console.md#api-routes)). Both query params are required; 400 on missing, 404 on no match. List endpoints stay redacted by default — reveal is opt-in, per-row, behind HTTP Basic auth.

Upstream credentials (`Configuration.Credentials`) are intended to **never** be revealed by the admin console — the configuration inspector and the export bundle replace them with `***` via the redactor in [`internal/admin/configexport/redact.go`](../internal/admin/configexport/redact.go). Note: that redactor (`redactConfigurations`) currently matches the v1 YAML key `upstream_credentials`; the v2 `Configuration.Credentials` map serializes under the key `credentials`, so verify export redaction against your config shape (tracked as a code follow-up). Only the gateway-issued Sluice secrets can be revealed, and only by name.

### Key rotation

Sluice does not support hot reload in v1.0 / v1.1. Rotating a key is a three-step process:

1. Edit `policy.yaml` to add the replacement key (and optionally mark the old key `enabled: false` to retire it gracefully).
2. Restart the gateway pods — rolling restart honours the data plane's graceful shutdown drain, so in-flight requests complete before the old config is swapped out.
3. Update the client to use the replacement secret.

Disabled keys remain in `SecretIndex` and authenticate structurally — the resolver detects `Enabled=false` and returns 401 with `result=disabled_key` so the audit log clearly distinguishes "client is still using the rotated-out secret" from "client is using an unknown secret".

For passthrough mode there is no key rotation on the gateway side — the upstream owns the credential lifecycle, and Sluice never sees it.

---

## Cross-references

- **[`docs/configuration-model.md#api_keys-block`](configuration-model.md#api_keys-block)** — the `api_keys:` YAML block, `APIKey` field reference, and load-time validation (`ErrUnknownConfiguration`).
- **[`docs/providers.md#per-protocol-auth-x-api-key-vs-bearer`](providers.md#per-protocol-auth-x-api-key-vs-bearer)** — the endpoint → provider → default override stack for the outbound credential header, with worked examples for the OpenAI-compat surfaces on Anthropic and Gemini.
- **[`docs/admin-console.md`](admin-console.md)** — the API-key reveal endpoint, Basic-auth password resolution, and redaction in the export bundle.
- **[`docs/actions.md`](actions.md)** — the `setHeader` rule action (which can rewrite outbound headers, including a credential header, via `applyStateOverlays`) and the `changeApiKey` action (which writes `state.UpstreamCredentialOverride`, now read at the single credential mint site to override the upstream credential — literal-key substitution, or `useSluiceKey` to forward the inbound `Authorization`).
- **[`internal/middleware/auth/resolver.go`](../internal/middleware/auth/resolver.go)** — `Resolver`, `AuthResult`, `Mode`, the discovery walk, and `UpstreamCredentialHeader` per-provider defaults.
- **[`internal/middleware/auth/auth.go`](../internal/middleware/auth/auth.go)** — `HTTPHandler`, the typed error → wire status mapping in `writeAuthError`, and the `Result` audit tags.
- **[`cmd/gateway/destination.go`](../cmd/gateway/destination.go)** — `buildDestination`, `resolveCredentialHeaders` (the credential precedence `switch`, including the `changeApiKey` override), and `credentialHeaderFor` (the single mint site).
- **[`cmd/gateway/handler.go`](../cmd/gateway/handler.go)** — the closed `credentialHeaderNames` set (`handler.go:148`), the `authFormatPlaceholder` (`{key}`) constant, and the data-plane handler composition.
- **[`internal/selection/selection.go`](../internal/selection/selection.go)** — `Target` (carries the pre-resolved `Auth` convention and `Credential` the destination builder consumes).
- **[`CLAUDE.md`](../CLAUDE.md)** — load-bearing invariant 6 (credential header lives in one place per `(provider, endpoint)`) and the *Authentication & Auth Modes* design summary.
