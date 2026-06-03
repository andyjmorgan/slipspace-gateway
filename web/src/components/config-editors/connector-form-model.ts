// Connector editor model — the contract shape, form state, and form↔contract
// mappers, shared by the gateway and control-plane consoles. Connectors carry
// no plaintext secrets (credentials are env:/file: secret_ref indirections), so
// the form round-trips the contract directly. The control-plane "controlplane"
// connector type reuses the webhook transport fields.

import type { SelectOption } from "@/components/forms/field-atoms"

export interface ConnectorAuth {
  mode: string
  access_key_id_ref?: string
  secret_access_key_ref?: string
  role_arn?: string
  external_id_ref?: string
  sas_token_ref?: string
  account_key_ref?: string
}

export interface ConnectorContract {
  name: string
  type: string
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
  auth?: ConnectorAuth
}

export type ConnectorFormState = ConnectorContract & { auth: ConnectorAuth }

export const CONNECTOR_TYPE_OPTIONS: SelectOption[] = [
  { value: "s3", label: "s3" },
  { value: "azure_blob", label: "azure_blob" },
  { value: "webhook", label: "webhook" },
  { value: "controlplane", label: "controlplane" },
]

export const S3_AUTH_MODES: SelectOption[] = [
  { value: "workload_identity", label: "workload_identity" },
  { value: "static", label: "static" },
  { value: "assume_role", label: "assume_role" },
]

export const AZURE_AUTH_MODES: SelectOption[] = [
  { value: "workload_identity", label: "workload_identity" },
  { value: "sas_token", label: "sas_token" },
  { value: "account_key", label: "account_key" },
]

export function emptyConnectorForm(): ConnectorFormState {
  return { name: "", type: "s3", auth: { mode: "workload_identity" } }
}

export function connectorFormFromContract(name: string, body: ConnectorContract | Record<string, unknown>): ConnectorFormState {
  const c = body as ConnectorContract
  return { ...c, name, auth: c.auth ?? { mode: "workload_identity" } }
}

export function connectorFormToContract(form: ConnectorFormState): ConnectorContract {
  const out: ConnectorContract = { name: form.name.trim(), type: form.type }
  if (form.type === "s3") {
    out.bucket = form.bucket?.trim() || undefined
    out.prefix = form.prefix?.trim() || undefined
    out.region = form.region?.trim() || undefined
    out.endpoint_url = form.endpoint_url?.trim() || undefined
    if (form.use_path_style) out.use_path_style = true
    out.auth = cleanAuth(form.auth, form.type)
  } else if (form.type === "azure_blob") {
    out.account = form.account?.trim() || undefined
    out.container = form.container?.trim() || undefined
    out.auth = cleanAuth(form.auth, form.type)
  } else if (form.type === "webhook" || form.type === "controlplane") {
    out.url = form.url?.trim() || undefined
    out.secret_ref = form.secret_ref?.trim() || undefined
    if (form.timeout_ms != null && form.timeout_ms > 0) out.timeout_ms = form.timeout_ms
  }
  return out
}

// cleanAuth drops auth fields irrelevant to the selected mode so the write body
// matches what the per-mode validator expects.
export function cleanAuth(auth: ConnectorAuth, type: string): ConnectorAuth {
  const out: ConnectorAuth = { mode: auth.mode }
  if (type === "s3") {
    if (auth.mode === "static") {
      out.access_key_id_ref = auth.access_key_id_ref?.trim() || undefined
      out.secret_access_key_ref = auth.secret_access_key_ref?.trim() || undefined
    } else if (auth.mode === "assume_role") {
      out.role_arn = auth.role_arn?.trim() || undefined
      out.external_id_ref = auth.external_id_ref?.trim() || undefined
    }
  } else if (type === "azure_blob") {
    if (auth.mode === "sas_token") out.sas_token_ref = auth.sas_token_ref?.trim() || undefined
    else if (auth.mode === "account_key") out.account_key_ref = auth.account_key_ref?.trim() || undefined
  }
  return out
}
