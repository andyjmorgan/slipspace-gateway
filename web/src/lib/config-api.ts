// Typed client + React hook for the read-only configuration endpoints
// under /admin/api/v1/config/*. Mirrors the Go DTOs in
// internal/admin/config_dto.go — secrets always arrive as RedactedSecret,
// never plaintext.

import { useEffect, useState, useCallback, useRef } from "react"
import { apiFetch, UnauthorizedError } from "@/lib/api"

export type RedactedSecret = {
  length: number
  last4: string
}

export type ConfigurationSummary = {
  name: string
  key_count: number
  rule_count: number
  tags?: Record<string, string>
}

export type RuleAttachment = {
  name: string
  condition_summary: string
  action_types: string[]
  behavior?: string
}

export type APIKeySummary = {
  name: string
  secret: RedactedSecret
  enabled: boolean
}

// ConnectorBinding mirrors the contract type. The editor does not yet offer a
// rich editor for these, but reads and round-trips them verbatim on save so a
// full-replace PUT never drops a configuration's connector bindings.
export type ConnectorBinding = {
  connector: string
  sampling?: number
  sampling_key?: string
  max_body_bytes?: number
  oversize_behaviour?: string
  filter?: unknown
}

export type ConfigurationDetail = {
  name: string
  credentials: Record<string, RedactedSecret>
  bindings: BindingRow[]
  passthrough_bindings: PassthroughBindingRow[]
  rules: RuleAttachment[]
  tags?: Record<string, string>
  connector_bindings?: ConnectorBinding[]
  api_keys: APIKeySummary[]
}

export type RuleSummary = {
  name: string
  condition_summary: string
  action_types: string[]
  behavior?: string
  used_by: string[]
}

export type RuleDetail = {
  name: string
  behavior?: string
  // Polymorphic — interrogated as-is by the SPA. The Go side already
  // marshals the concrete condition/action shapes via the contract types'
  // MarshalJSON, so this carries the discriminator + fields.
  condition?: Record<string, unknown>
  actions?: Record<string, unknown>[]
  used_by: string[]
}

export type ProviderSummary = {
  name: string
  base_url: string
  protocols: string[]
  has_passthrough: boolean
}

export type ProtocolRow = {
  name: string
  path: string
  auth_header?: string
  auth_format?: string
}

export type PassthroughPathRow = {
  match: string
  methods: string[]
}

export type PassthroughFamilyRow = {
  name: string
  auth_header?: string
  paths: PassthroughPathRow[]
}

export type ProviderDetail = {
  name: string
  base_url: string
  required_headers?: Record<string, string>
  query?: Record<string, string>
  protocols: ProtocolRow[]
  passthrough?: PassthroughFamilyRow[]
}

export type BindingRow = {
  configuration?: string
  protocol: string
  models: string[]
  provider?: string
  group?: string
  alias?: string
  tags?: string[]
}

export type PassthroughBindingRow = {
  configuration?: string
  family: string
  provider: string
  tags?: string[]
}

export type BindingsResponse = {
  bindings: BindingRow[]
  passthrough_bindings: PassthroughBindingRow[]
}

export type APIKeyReveal = {
  name: string
  secret: string
  enabled: boolean
  configuration: string
}

// ---------------------------------------------------------------------------
// v2 contract shapes the write editors read + submit. These mirror the Go
// contract types (contracts/config/model_v2.go, connectors.go, api_keys.go) on
// the wire — the GET detail endpoints return the contract type directly for
// providers/groups/connectors (no DTO projection except for credentials, which
// are redacted on the configuration detail and never travel on the provider /
// group / connector shapes).

export type ProviderAuth = {
  header: string
  format?: string
}

export type ProviderProtocol = {
  path?: string
  auth?: ProviderAuth | null
}

export type PassthroughPath = {
  match: string
  methods: string[]
}

export type PassthroughFamily = {
  auth?: ProviderAuth | null
  paths: PassthroughPath[]
}

// Provider is the full editable provider connection. The GET /providers/{name}
// detail endpoint returns the ProviderDetail DTO (protocols/passthrough as
// arrays); the write body accepts the contract Provider (protocols/passthrough
// as maps). The editor seeds from ProviderDetail and submits this map shape.
export type Provider = {
  base_url: string
  required_headers?: Record<string, string>
  query?: Record<string, string>
  protocols?: Record<string, ProviderProtocol>
  passthrough?: Record<string, PassthroughFamily>
}

export type ProviderWriteBody = Provider & {
  name: string
}

// CircuitBreakerConfig mirrors contracts/resilience.CircuitBreakerConfig.
// The editor only toggles `enabled`; the remaining tuning fields round-trip
// untouched (the read seeds and the write passes through whatever was set).
export type CircuitBreakerConfig = {
  enabled?: boolean
  failure_threshold?: number
  failure_rate_threshold?: number
  cooldown_seconds?: number
  half_open_success_threshold?: number
}

export type Target = {
  provider: string
  alias?: string
  query?: Record<string, string>
  path?: string
  weight?: number
}

export type Group = {
  mode: string
  failure_status_codes?: number[]
  circuit_breaker?: CircuitBreakerConfig | null
  strict_weights?: boolean
  response_header_timeout_seconds?: number
  targets: Target[]
}

export type GroupView = Group & {
  name: string
}

export type GroupWriteBody = Group & {
  name: string
}

export type ConnectorAuth = {
  mode: string
  access_key_id_ref?: string
  secret_access_key_ref?: string
  role_arn?: string
  external_id_ref?: string
  sas_token_ref?: string
  account_key_ref?: string
}

export type ConnectorRotation = {
  max_bytes?: number
  max_age_seconds?: number
}

// Connector is the full editable connector definition (s3 / azure_blob /
// webhook). Carries no plaintext secrets — cloud + webhook credentials are
// secret_ref indirections resolved at runtime — so it round-trips directly with
// no mask/write-back.
export type Connector = {
  name: string
  type: string
  auth?: ConnectorAuth | null
  rotation?: ConnectorRotation | null
  bucket?: string
  prefix?: string
  region?: string
  endpoint_url?: string
  use_path_style?: boolean
  account?: string
  container?: string
  url?: string
  secret_ref?: string
  timeout_ms?: number
}

export type APIKeyListItem = {
  id?: string
  name: string
  configuration: string
  enabled: boolean
  secret: RedactedSecret
}

// ConfigurationWriteBody is the POST/PUT shape for a configuration. Credentials
// is map[provider] -> string | null with write-back-if-delivered semantics: null
// keeps the stored secret for that provider (the masked-unchanged signal), a
// real string sets it, "" is a no-credential provider. A provider absent from the
// map is dropped. There is deliberately no api_keys field — keys are managed
// only via the dedicated /api-keys endpoints.
export type ConfigurationWriteBody = {
  name: string
  credentials?: Record<string, string | null>
  bindings?: BindingRow[]
  passthrough_bindings?: PassthroughBindingRow[]
  rule_names?: string[]
  tags?: Record<string, string>
  connector_bindings?: ConnectorBinding[]
}

// ConflictError is the 409 wire shape shared by every config-write resource —
// name-already-exists on POST, rename-not-supported on PUT, or still-referenced
// on DELETE. UsedBy is populated only in the referenced case.
export type ConflictError = {
  error: string
  name?: string
  used_by?: string[]
}

// ValidationFailure is the 422 wire shape returned when the post-mutation
// RevalidateAndIndex fails.
export type ValidationFailure = {
  error: string
  detail: string
}

// PreviewResult is the dry-run wire shape: whether a candidate mutation would
// pass validation, and the underlying error when not.
export type PreviewResult = {
  valid: boolean
  error?: string
}

// ---------------------------------------------------------------------------
// Hook scaffold — config endpoints are immutable for the lifetime of the
// gateway process, so the hooks fetch once on mount with a manual refetch.
// No polling cadence.

export type ConfigFetchState<T> =
  | { status: "loading" }
  | { status: "ok"; data: T }
  | { status: "unauthorized" }
  | { status: "not_found" }
  | { status: "error"; message: string }

export type ConfigFetchHandle<T> = {
  state: ConfigFetchState<T>
  refetch: () => void
}

function useConfigFetch<T>(path: string | null): ConfigFetchHandle<T> {
  const [state, setState] = useState<ConfigFetchState<T>>({ status: "loading" })
  const [nonce, setNonce] = useState(0)
  const cancelled = useRef(false)
  const refetch = useCallback(() => setNonce((n) => n + 1), [])

  useEffect(() => {
    if (!path) {
      setState({ status: "loading" })
      return
    }
    cancelled.current = false
    setState({ status: "loading" })
    apiFetch<T>(path)
      .then((data) => {
        if (!cancelled.current) setState({ status: "ok", data })
      })
      .catch((e) => {
        if (cancelled.current) return
        if (e instanceof UnauthorizedError) {
          setState({ status: "unauthorized" })
          return
        }
        // 404 on a detail page is a soft state — render an empty/missing
        // panel rather than a destructive error toast.
        const status = (e as { status?: number }).status
        if (status === 404) {
          setState({ status: "not_found" })
          return
        }
        const msg = e instanceof Error ? e.message : String(e)
        setState({ status: "error", message: msg })
      })
    return () => {
      cancelled.current = true
    }
  }, [path, nonce])

  return { state, refetch }
}

export function useConfigurations(): ConfigFetchHandle<ConfigurationSummary[]> {
  return useConfigFetch<ConfigurationSummary[]>("/api/v1/config/configurations")
}

export function useConfiguration(name: string | undefined): ConfigFetchHandle<ConfigurationDetail> {
  return useConfigFetch<ConfigurationDetail>(name ? `/api/v1/config/configurations/${encodeURIComponent(name)}` : null)
}

export function useRules(): ConfigFetchHandle<RuleSummary[]> {
  return useConfigFetch<RuleSummary[]>("/api/v1/config/rules")
}

export function useRule(name: string | undefined): ConfigFetchHandle<RuleDetail> {
  return useConfigFetch<RuleDetail>(name ? `/api/v1/config/rules/${encodeURIComponent(name)}` : null)
}

export function useProviders(): ConfigFetchHandle<ProviderSummary[]> {
  return useConfigFetch<ProviderSummary[]>("/api/v1/config/providers")
}

export function useProvider(name: string | undefined): ConfigFetchHandle<ProviderDetail> {
  return useConfigFetch<ProviderDetail>(name ? `/api/v1/config/providers/${encodeURIComponent(name)}` : null)
}

/**
 * revealAPIKey fetches the plaintext secret for a single api_key. The
 * call is intentionally NOT a polling hook — it fires on user demand
 * (a "Reveal" button) and the plaintext never lives in shared state.
 * Throws UnauthorizedError on 401 so the caller can route back to /login;
 * throws APIError otherwise.
 */
export async function revealAPIKey(configuration: string, name: string): Promise<APIKeyReveal> {
  const qs = new URLSearchParams({ configuration, name }).toString()
  return apiFetch<APIKeyReveal>(`/api/v1/config/api-keys/reveal?${qs}`)
}

export function useBindings(): ConfigFetchHandle<BindingsResponse> {
  return useConfigFetch<BindingsResponse>("/api/v1/config/bindings")
}

export function useConnectors(): ConfigFetchHandle<Connector[]> {
  return useConfigFetch<Connector[]>("/api/v1/config/connectors")
}

export function useConnector(name: string | undefined): ConfigFetchHandle<Connector> {
  return useConfigFetch<Connector>(name ? `/api/v1/config/connectors/${encodeURIComponent(name)}` : null)
}

export function useGroups(): ConfigFetchHandle<GroupView[]> {
  return useConfigFetch<GroupView[]>("/api/v1/config/groups")
}

export function useGroup(name: string | undefined): ConfigFetchHandle<GroupView> {
  return useConfigFetch<GroupView>(name ? `/api/v1/config/groups/${encodeURIComponent(name)}` : null)
}

export function useAPIKeys(): ConfigFetchHandle<APIKeyListItem[]> {
  return useConfigFetch<APIKeyListItem[]>("/api/v1/config/api-keys")
}

export function useAPIKey(id: string | undefined): ConfigFetchHandle<APIKeyListItem> {
  return useConfigFetch<APIKeyListItem>(id ? `/api/v1/config/api-keys/${encodeURIComponent(id)}` : null)
}

export type PolicyTarget = {
  name: string
  provider?: string
  order?: number
  weight?: number
  circuit_state: string
}

export type PolicySummary = {
  name: string
  mode: string
  strict_weights?: boolean
  failure_status_codes?: number[]
  circuit_breaker_enabled?: boolean
  targets: PolicyTarget[]
}

export type PoliciesResponse = {
  pod: string
  policies: PolicySummary[]
}

export function usePolicies(): ConfigFetchHandle<PoliciesResponse> {
  return useConfigFetch<PoliciesResponse>("/api/v1/policies")
}

// ---------------------------------------------------------------------------
// Rules write API. Mirrors the Phase 2 server handlers under
// /admin/api/v1/config/rules[/{name}] — see internal/admin/rules_write.go.
// Every write goes through the same Snapshot.Clone → mutate → Validate →
// WritePolicyYAML → Store.Replace flow, so a 200/201/204 here means the
// next GET reflects the new state and the change is persisted to
// SLUICE_CONFIG_DIR/policy.yaml.

/**
 * RuleWriteBody is the JSON wire shape the create/replace handlers
 * accept. Mirrors RuleContract — name plus a polymorphic condition
 * and an ordered action list. Condition and Action are dispatched on
 * the `type` discriminator and use snake_case JSON field names per
 * the schema convention.
 */
export type RuleWriteBody = {
  name: string
  behavior?: string
  condition?: Record<string, unknown>
  actions?: Record<string, unknown>[]
}

/**
 * RuleConflict is the JSON envelope the server returns with 409 on
 * the rules write endpoints. `used_by` is populated only on the
 * DELETE-referenced path; create-duplicate and PUT-rename responses
 * leave it absent.
 */
export type RuleConflict = {
  error: string
  name?: string
  used_by?: string[]
}

/**
 * RuleValidationError is the JSON envelope returned with 422 when the
 * post-mutation Validate fails. `detail` carries the wrapped sentinel
 * chain so the SPA can show the operator the underlying reason.
 */
export type RuleValidationFailure = {
  error: string
  detail: string
}

/**
 * createRule POSTs a new rule. 201 returns the canonical RuleDetail.
 * 409 surfaces as APIError with body={RuleConflict, name:"..."} —
 * callers should pattern-match on `status === 409`.
 */
export async function createRule(rule: RuleWriteBody): Promise<RuleDetail> {
  return apiFetch<RuleDetail>("/api/v1/config/rules", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(rule),
  })
}

/**
 * replaceRule PUTs a rule by URL name. 200 returns the updated
 * RuleDetail. Rename is rejected by the server with 409 — pass the
 * same name on the URL and in the body to avoid that path. 404 fires
 * when the URL name does not match any existing rule.
 */
export async function replaceRule(name: string, rule: RuleWriteBody): Promise<RuleDetail> {
  return apiFetch<RuleDetail>(`/api/v1/config/rules/${encodeURIComponent(name)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(rule),
  })
}

/**
 * deleteRule removes a rule by name. 204 on success. 409 with
 * RuleConflict.used_by populated when the rule is referenced by one
 * or more configurations — the editor renders the list inline and
 * asks the operator to unbind first.
 */
export async function deleteRule(name: string): Promise<void> {
  await apiFetch<void>(`/api/v1/config/rules/${encodeURIComponent(name)}`, {
    method: "DELETE",
  })
}

// ---------------------------------------------------------------------------
// Providers / groups / configurations / connectors / api_keys write API.
// Every write mirrors the same Snapshot.Clone -> mutate -> Validate ->
// WritePolicyYAML -> Store.Replace flow as rules; a 200/201/204 means the next
// GET reflects the new state and the change is persisted. POST/PUT accept
// ?dry_run=true and return PreviewResult without persisting (the diff-preview's
// safety half — see internal/admin/config_write.go::previewMutation).

function dryRunPath(path: string): string {
  return path.includes("?") ? `${path}&dry_run=true` : `${path}?dry_run=true`
}

// Providers ------------------------------------------------------------------

export async function createProvider(body: ProviderWriteBody): Promise<ProviderDetail> {
  return apiFetch<ProviderDetail>("/api/v1/config/providers", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

export async function replaceProvider(name: string, body: ProviderWriteBody): Promise<ProviderDetail> {
  return apiFetch<ProviderDetail>(`/api/v1/config/providers/${encodeURIComponent(name)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

export async function deleteProvider(name: string): Promise<void> {
  await apiFetch<void>(`/api/v1/config/providers/${encodeURIComponent(name)}`, { method: "DELETE" })
}

export async function previewProvider(name: string | null, body: ProviderWriteBody): Promise<PreviewResult> {
  const base = name ? `/api/v1/config/providers/${encodeURIComponent(name)}` : "/api/v1/config/providers"
  return apiFetch<PreviewResult>(dryRunPath(base), {
    method: name ? "PUT" : "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

// Groups --------------------------------------------------------------------

export async function createGroup(body: GroupWriteBody): Promise<GroupView> {
  return apiFetch<GroupView>("/api/v1/config/groups", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

export async function replaceGroup(name: string, body: GroupWriteBody): Promise<GroupView> {
  return apiFetch<GroupView>(`/api/v1/config/groups/${encodeURIComponent(name)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

export async function deleteGroup(name: string): Promise<void> {
  await apiFetch<void>(`/api/v1/config/groups/${encodeURIComponent(name)}`, { method: "DELETE" })
}

export async function previewGroup(name: string | null, body: GroupWriteBody): Promise<PreviewResult> {
  const base = name ? `/api/v1/config/groups/${encodeURIComponent(name)}` : "/api/v1/config/groups"
  return apiFetch<PreviewResult>(dryRunPath(base), {
    method: name ? "PUT" : "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

// Configurations ------------------------------------------------------------

export async function createConfiguration(body: ConfigurationWriteBody): Promise<ConfigurationDetail> {
  return apiFetch<ConfigurationDetail>("/api/v1/config/configurations", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

export async function replaceConfiguration(name: string, body: ConfigurationWriteBody): Promise<ConfigurationDetail> {
  return apiFetch<ConfigurationDetail>(`/api/v1/config/configurations/${encodeURIComponent(name)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

export async function deleteConfiguration(name: string): Promise<void> {
  await apiFetch<void>(`/api/v1/config/configurations/${encodeURIComponent(name)}`, { method: "DELETE" })
}

export async function previewConfiguration(name: string | null, body: ConfigurationWriteBody): Promise<PreviewResult> {
  const base = name ? `/api/v1/config/configurations/${encodeURIComponent(name)}` : "/api/v1/config/configurations"
  return apiFetch<PreviewResult>(dryRunPath(base), {
    method: name ? "PUT" : "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

// Connectors ----------------------------------------------------------------

export async function createConnector(body: Connector): Promise<Connector> {
  return apiFetch<Connector>("/api/v1/config/connectors", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

export async function replaceConnector(name: string, body: Connector): Promise<Connector> {
  return apiFetch<Connector>(`/api/v1/config/connectors/${encodeURIComponent(name)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

export async function deleteConnector(name: string): Promise<void> {
  await apiFetch<void>(`/api/v1/config/connectors/${encodeURIComponent(name)}`, { method: "DELETE" })
}

// API keys ------------------------------------------------------------------

export type APIKeyCreateBody = {
  name: string
  configuration: string
  enabled?: boolean
}

// createAPIKey mints a key. The 201 response carries the PLAINTEXT secret
// exactly once — show it, let the operator copy it, then it is gone forever.
export async function createAPIKey(body: APIKeyCreateBody): Promise<APIKeyReveal> {
  return apiFetch<APIKeyReveal>("/api/v1/config/api-keys", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

// replaceAPIKey reassigns an existing key's configuration + enabled state. The
// secret is immutable; rename is rejected with 409 (use patchAPIKey).
export async function replaceAPIKey(id: string, body: { configuration: string; enabled: boolean }): Promise<APIKeyListItem> {
  return apiFetch<APIKeyListItem>(`/api/v1/config/api-keys/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

// patchAPIKey is the partial update — the everyday enable/disable off-switch,
// plus rename / reassign. Omitted fields are unchanged; the secret is never
// touched.
export async function patchAPIKey(
  id: string,
  body: { name?: string; configuration?: string; enabled?: boolean },
): Promise<APIKeyListItem> {
  return apiFetch<APIKeyListItem>(`/api/v1/config/api-keys/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

export async function deleteAPIKey(id: string): Promise<void> {
  await apiFetch<void>(`/api/v1/config/api-keys/${encodeURIComponent(id)}`, { method: "DELETE" })
}

// apiKeyRef returns the path ref for a key — its minted UUID when present,
// otherwise the name fallback (matches apiKeyIndexByRef on the Go side).
export function apiKeyRef(k: APIKeyListItem): string {
  return k.id ?? k.name
}
