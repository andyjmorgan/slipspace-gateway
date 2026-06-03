// View shapes + body→view mappers for the shared backend list/detail views.
// Kept out of backend-views.tsx so that module only exports components
// (react-refresh/only-export-components) — same split as the *-form-model.ts
// files. The gateway feeds these views from its live read API (BackendSummary /
// BackendDetail, which are structurally identical); the control-plane console
// maps a staged entity's BackendContract body into them via the functions here.

import type { BackendContract } from "@/components/config-editors/backend-form-model"

// BackendListItem is one row of the backends table.
export interface BackendListItem {
  name: string
  base_url: string
  protocols: string[]
  has_passthrough: boolean
}

export interface ProtocolRow {
  name: string
  path: string
  auth_header?: string
  auth_format?: string
}

export interface PassthroughPathRow {
  match: string
  methods: string[]
}

export interface PassthroughFamilyRow {
  name: string
  auth_header?: string
  paths: PassthroughPathRow[]
}

// BackendDetailData is the full backend as the detail view renders it.
export interface BackendDetailData {
  name: string
  base_url: string
  required_headers?: Record<string, string>
  query?: Record<string, string>
  protocols: ProtocolRow[]
  passthrough?: PassthroughFamilyRow[]
}

// backendListItemFromContract derives a list row from a stored backend body —
// the protocol/passthrough maps collapse to a name list + a boolean.
export function backendListItemFromContract(name: string, body: BackendContract | Record<string, unknown>): BackendListItem {
  const b = body as BackendContract
  return {
    name,
    base_url: b.base_url ?? "",
    protocols: Object.keys(b.protocols ?? {}),
    has_passthrough: Object.keys(b.passthrough ?? {}).length > 0,
  }
}

// backendDetailFromContract expands a stored backend body into the detail view
// shape — the keyed protocol/passthrough maps become ordered rows.
export function backendDetailFromContract(name: string, body: BackendContract | Record<string, unknown>): BackendDetailData {
  const b = body as BackendContract
  return {
    name,
    base_url: b.base_url ?? "",
    required_headers: b.required_headers,
    query: b.query,
    protocols: Object.entries(b.protocols ?? {}).map(([pname, p]) => ({
      name: pname,
      path: p.path ?? "",
      auth_header: p.auth?.header,
      auth_format: p.auth?.format,
    })),
    passthrough: Object.entries(b.passthrough ?? {}).map(([fname, f]) => ({
      name: fname,
      auth_header: f.auth?.header,
      paths: (f.paths ?? []).map((pp) => ({ match: pp.match, methods: pp.methods.slice() })),
    })),
  }
}
