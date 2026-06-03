// Group (resilience) editor model — contract shape, form state, and mappers,
// shared by both consoles. The CP entity body is the contract Group; the
// gateway's GroupView is the same plus name, so one fromContract covers both.

import { stringsFromList } from "@/components/forms/field-helpers"
import type { SelectOption } from "@/components/forms/field-atoms"

export interface CircuitBreakerConfig {
  enabled?: boolean
  failure_threshold?: number
  sampling_duration_seconds?: number
  cooldown_seconds?: number
  half_open_success_threshold?: number
  minimum_throughput?: number
}

export interface GroupTarget {
  backend: string
  alias?: string
  path?: string
  weight?: number
  query?: Record<string, string>
}

export interface GroupContract {
  name: string
  mode: string
  failure_status_codes?: number[]
  strict_weights?: boolean
  circuit_breaker?: CircuitBreakerConfig
  response_header_timeout_seconds?: number
  targets: GroupTarget[]
}

export const GROUP_MODE_OPTIONS: SelectOption[] = [
  { value: "failover", label: "failover (ordered — try targets in sequence)" },
  { value: "load_balance", label: "load_balance (weighted random)" },
  { value: "load_balance_with_failover", label: "load_balance_with_failover" },
]

export interface TargetDraft {
  backend: string
  alias: string
  path: string
  weight: number | null
}

export interface GroupFormState {
  name: string
  mode: string
  failureStatusCodes: string[]
  strictWeights: boolean
  cbEnabled: boolean
  cbConfig: CircuitBreakerConfig | null
  responseHeaderTimeoutSeconds: number | null
  targets: TargetDraft[]
}

export function emptyGroupForm(): GroupFormState {
  return {
    name: "",
    mode: "failover",
    failureStatusCodes: [],
    strictWeights: false,
    cbEnabled: false,
    cbConfig: null,
    responseHeaderTimeoutSeconds: null,
    targets: [],
  }
}

export function groupFormFromContract(name: string, body: GroupContract | Record<string, unknown>): GroupFormState {
  const v = body as GroupContract
  return {
    name,
    mode: v.mode || "failover",
    failureStatusCodes: (v.failure_status_codes ?? []).map((n) => String(n)),
    strictWeights: v.strict_weights ?? false,
    cbEnabled: v.circuit_breaker?.enabled ?? false,
    cbConfig: v.circuit_breaker ?? null,
    responseHeaderTimeoutSeconds: v.response_header_timeout_seconds ?? null,
    targets: (v.targets ?? []).map((t) => ({
      backend: t.backend,
      alias: t.alias ?? "",
      path: t.path ?? "",
      weight: t.weight ?? null,
    })),
  }
}

export function groupFormToContract(form: GroupFormState): GroupContract {
  const targets: GroupTarget[] = form.targets
    .filter((t) => t.backend.trim() !== "")
    .map((t) => {
      const out: GroupTarget = { backend: t.backend.trim() }
      if (t.alias.trim()) out.alias = t.alias.trim()
      if (t.path.trim()) out.path = t.path.trim()
      if (t.weight != null && t.weight > 0) out.weight = t.weight
      return out
    })

  const body: GroupContract = { name: form.name.trim(), mode: form.mode, targets }
  const codes = stringsFromList(form.failureStatusCodes)
    .map((s) => Number(s))
    .filter((n) => Number.isFinite(n) && n > 0)
  if (codes.length > 0) body.failure_status_codes = codes
  if (form.strictWeights) body.strict_weights = true
  if (form.cbEnabled) body.circuit_breaker = { ...(form.cbConfig ?? {}), enabled: true }
  if (form.responseHeaderTimeoutSeconds != null && form.responseHeaderTimeoutSeconds > 0) {
    body.response_header_timeout_seconds = form.responseHeaderTimeoutSeconds
  }
  return body
}
