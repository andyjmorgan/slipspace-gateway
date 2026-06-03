// Backend editor model: the contract shape, the editor's form state, and the
// form↔contract mappers. Kept out of backend-form.tsx so that module only
// exports components (react-refresh/only-export-components) — same split as
// field-helpers.ts / field-atoms.tsx.

import {
  recordFromPairs,
  pairsFromRecord,
  stringsFromList,
  type KVPair,
} from "@/components/forms/field-helpers"

// BackendContract is the on-the-wire backend shape (the CP entity body and the
// gateway write body both structurally match this).
export interface BackendContract {
  name: string
  base_url: string
  required_headers?: Record<string, string>
  query?: Record<string, string>
  protocols?: Record<string, { path?: string; auth?: { header: string; format?: string } }>
  passthrough?: Record<string, { auth?: { header: string }; paths: { match: string; methods: string[] }[] }>
}

export type ProtocolDraft = { name: string; path: string; authHeader: string; authFormat: string }
export type PassthroughPathDraft = { match: string; methods: string[] }
export type PassthroughFamilyDraft = { name: string; authHeader: string; paths: PassthroughPathDraft[] }

export interface BackendFormState {
  name: string
  baseUrl: string
  requiredHeaders: KVPair[]
  query: KVPair[]
  protocols: ProtocolDraft[]
  passthrough: PassthroughFamilyDraft[]
}

export function emptyBackendForm(): BackendFormState {
  return { name: "", baseUrl: "", requiredHeaders: [], query: [], protocols: [], passthrough: [] }
}

// backendFormFromContract maps a contract Backend (maps, nested auth) into the
// editor's array-shaped form state. The CP loads entities in this shape.
export function backendFormFromContract(name: string, body: BackendContract | Record<string, unknown>): BackendFormState {
  const b = body as BackendContract
  return {
    name,
    baseUrl: b.base_url ?? "",
    requiredHeaders: pairsFromRecord(b.required_headers),
    query: pairsFromRecord(b.query),
    protocols: Object.entries(b.protocols ?? {}).map(([pname, p]) => ({
      name: pname,
      path: p.path ?? "",
      authHeader: p.auth?.header ?? "",
      authFormat: p.auth?.format ?? "",
    })),
    passthrough: Object.entries(b.passthrough ?? {}).map(([fname, f]) => ({
      name: fname,
      authHeader: f.auth?.header ?? "",
      paths: (f.paths ?? []).map((pp) => ({ match: pp.match, methods: pp.methods.slice() })),
    })),
  }
}

// backendFormToContract serialises the form back into the contract Backend
// (maps). Both consoles send this shape.
export function backendFormToContract(form: BackendFormState): BackendContract {
  const body: BackendContract = { name: form.name.trim(), base_url: form.baseUrl.trim() }
  const headers = recordFromPairs(form.requiredHeaders)
  if (Object.keys(headers).length > 0) body.required_headers = headers
  const query = recordFromPairs(form.query)
  if (Object.keys(query).length > 0) body.query = query

  if (form.protocols.length > 0) {
    body.protocols = {}
    for (const p of form.protocols) {
      const name = p.name.trim()
      if (name === "") continue
      body.protocols[name] = {
        path: p.path.trim() || undefined,
        auth: p.authHeader.trim()
          ? { header: p.authHeader.trim(), format: p.authFormat.trim() || undefined }
          : undefined,
      }
    }
  }

  const families = form.passthrough.filter((f) => f.name.trim() !== "")
  if (families.length > 0) {
    body.passthrough = {}
    for (const f of families) {
      body.passthrough[f.name.trim()] = {
        auth: f.authHeader.trim() ? { header: f.authHeader.trim() } : undefined,
        paths: f.paths
          .filter((pp) => pp.match.trim() !== "")
          .map((pp) => ({ match: pp.match.trim(), methods: stringsFromList(pp.methods) })),
      }
    }
  }
  return body
}
