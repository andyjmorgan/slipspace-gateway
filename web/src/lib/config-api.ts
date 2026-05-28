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

export type ConfigurationDetail = {
  name: string
  upstream_credentials: Record<string, RedactedSecret>
  rules: RuleAttachment[]
  tags?: Record<string, string>
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
  prefix?: string
  prefix_required: boolean
  base_url: string
  endpoint_count: number
}

export type EndpointSummary = {
  name: string
  path: string
  methods: string[]
  accepted_paths: string[]
  accepts_streaming: boolean
  request_kind: string
  auth_header?: string
  auth_format?: string
  prefix_optional?: boolean
}

export type ProviderDetail = {
  name: string
  prefix?: string
  prefix_required: boolean
  base_url: string
  auth_header?: string
  auth_format?: string
  required_headers?: Record<string, string>
  endpoints: EndpointSummary[]
}

export type RouteRow = {
  path: string
  provider: string
  endpoint: string
  methods: string[]
}

export type APIKeyReveal = {
  name: string
  secret: string
  enabled: boolean
  configuration: string
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

export function useRoutes(): ConfigFetchHandle<RouteRow[]> {
  return useConfigFetch<RouteRow[]>("/api/v1/config/routes")
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
// Rules write API. Mirrors the Phase 2 backend handlers under
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
 * RuleConflict is the JSON envelope the backend returns with 409 on
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
 * RuleDetail. Rename is rejected by the backend with 409 — pass the
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
