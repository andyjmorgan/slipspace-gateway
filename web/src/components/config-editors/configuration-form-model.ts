// Configuration editor model — the form state + the shared types, plus the
// control-plane mappers. The gateway and CP share the form component and types
// but NOT the credential mapping: the gateway redacts secrets on read
// (mask + submit-null-to-keep), while the CP carries credentials inline as
// plaintext. So the gateway keeps its own formFromDetail/toWriteBody; the CP
// uses configFormFromContract/configFormToContract here.

import {
  recordFromPairs,
  pairsFromRecord,
  stringsFromList,
  type KVPair,
} from "@/components/forms/field-helpers"
import type { SelectOption } from "@/components/forms/field-atoms"

// PROTOCOLS is the closed set of generative protocol names — the entire allowed
// value space for a binding's protocol and a backend's protocols-map key. Source
// of truth: the ProtocolX consts in contracts/config/model_v2.go.
export const PROTOCOLS = ["chat", "responses", "messages", "generate_content", "embeddings"] as const

export type Protocol = (typeof PROTOCOLS)[number]

// PROTOCOL_OPTIONS adapts PROTOCOLS to the SelectField option shape.
export const PROTOCOL_OPTIONS: SelectOption[] = PROTOCOLS.map((p) => ({ value: p, label: p }))

// existing carries the gateway's redacted projection (last4) when editing a
// pre-existing credential; null when there's nothing stored (or on the CP,
// which renders the plaintext value directly).
export interface CredentialDraft {
  backend: string
  existing: { last4: string } | null
  value: string
  dirty: boolean
}

export interface BindingDraft {
  protocol: string
  models: string[]
  destinationKind: "backend" | "group"
  destination: string
  alias: string
}

export interface PassthroughBindingDraft {
  family: string
  backend: string
}

export interface ConfigFormState {
  name: string
  credentials: CredentialDraft[]
  bindings: BindingDraft[]
  passthroughBindings: PassthroughBindingDraft[]
  ruleNames: string[]
  tags: KVPair[]
  connectorBindings: unknown[]
}

export function emptyConfigForm(): ConfigFormState {
  return { name: "", credentials: [], bindings: [], passthroughBindings: [], ruleNames: [], tags: [], connectorBindings: [] }
}

// --- control-plane (plaintext) mappers ---

interface ConfigBindingRow {
  protocol: string
  models?: string[]
  backend?: string
  group?: string
  alias?: string
}

interface ConfigBody {
  credentials?: Record<string, string>
  bindings?: ConfigBindingRow[]
  passthrough_bindings?: Array<{ family: string; backend: string }>
  rule_names?: string[]
  tags?: Record<string, string>
  connector_bindings?: unknown[]
}

export function configFormFromContract(name: string, body: Record<string, unknown>): ConfigFormState {
  const c = body as ConfigBody
  return {
    name,
    credentials: Object.entries(c.credentials ?? {}).map(([backend, value]) => ({
      backend,
      existing: null,
      value: String(value ?? ""),
      dirty: true,
    })),
    bindings: (c.bindings ?? []).map((b) => ({
      protocol: b.protocol,
      models: (b.models ?? []).slice(),
      destinationKind: b.group ? "group" : "backend",
      destination: b.group ?? b.backend ?? "",
      alias: b.alias ?? "",
    })),
    passthroughBindings: (c.passthrough_bindings ?? []).map((p) => ({ family: p.family, backend: p.backend })),
    ruleNames: (c.rule_names ?? []).slice(),
    tags: pairsFromRecord(c.tags),
    connectorBindings: (c.connector_bindings ?? []).slice(),
  }
}

export function configFormToContract(form: ConfigFormState): { name: string } & ConfigBody {
  const credentials: Record<string, string> = {}
  for (const c of form.credentials) {
    const backend = c.backend.trim()
    if (backend === "") continue
    credentials[backend] = c.value
  }
  const bindings = form.bindings
    .filter((b) => b.destination.trim() !== "")
    .map((b) => {
      const row: ConfigBindingRow = { protocol: b.protocol, models: stringsFromList(b.models) }
      if (b.destinationKind === "group") row.group = b.destination.trim()
      else {
        row.backend = b.destination.trim()
        if (b.alias.trim()) row.alias = b.alias.trim()
      }
      return row
    })
  const passthrough = form.passthroughBindings
    .filter((p) => p.family.trim() !== "" && p.backend.trim() !== "")
    .map((p) => ({ family: p.family.trim(), backend: p.backend.trim() }))

  const body: { name: string } & ConfigBody = { name: form.name.trim() }
  if (Object.keys(credentials).length > 0) body.credentials = credentials
  if (bindings.length > 0) body.bindings = bindings
  if (passthrough.length > 0) body.passthrough_bindings = passthrough
  const rules = stringsFromList(form.ruleNames)
  if (rules.length > 0) body.rule_names = rules
  const tags = recordFromPairs(form.tags)
  if (Object.keys(tags).length > 0) body.tags = tags
  if (form.connectorBindings.length > 0) body.connector_bindings = form.connectorBindings
  return body
}
