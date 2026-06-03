// View shapes + mappers for the shared configuration list/detail views.
// Kept out of configuration-views.tsx so that module only exports components
// (react-refresh/only-export-components) — same split as the *-form-model.ts
// and backend-views-model.ts files.
//
// Two console contexts feed these views, and they differ on ONE load-bearing
// point: credential representation. The gateway read API redacts every
// credential (it only ever ships a {length, last4} RedactedSecret — never a
// plaintext value), whereas the control-plane entity body carries credentials
// inline as refs/plaintext. So there are TWO mappers:
//
//   - configurationDetailFromGateway — from the gateway's redacted detail DTO,
//     keeping its real last4 mask.
//   - configurationDetailFromContract — from a staged CP entity body, where the
//     credential value is a ref/plaintext we MUST NOT render. We map it to a
//     masked CredentialView that exposes only the backend key + whether a value
//     is set — never the value itself.
//
// The shared ConfigurationDetailView renders CredentialView only; neither path
// can leak a raw secret because the view shape has no field for one.

// CredentialView is one row of the credentials table. It carries only the
// backend key and a non-reversible presence/mask indicator — never the secret.
export interface CredentialView {
  backend: string
  // mask is a display-only hint: "set" when a value exists (CP plaintext path,
  // which deliberately discards the value), or the gateway's "••••<last4>"
  // redaction. Never the plaintext credential.
  mask: string
  // length is the gateway's reported credential length, or undefined on the CP
  // path where the body carries the value (which we refuse to measure/expose).
  length?: number
}

export interface ConfigBindingView {
  protocol: string
  models: string[]
  backend?: string
  group?: string
  alias?: string
  tags: string[]
}

export interface PassthroughBindingView {
  family: string
  backend: string
  tags: string[]
}

export interface RuleAttachmentView {
  name: string
  // condition_summary / action_types / behavior are only available on the
  // gateway path (the read API enriches rule_names into attachments). On the CP
  // path the configuration entity only stores rule_names, so the view falls
  // back to the bare name.
  conditionSummary?: string
  actionTypes?: string[]
  behavior?: string
}

export interface ConnectorBindingView {
  connector: string
  sampling?: number
  samplingKey?: string
  maxBodyBytes?: number
  oversizeBehaviour?: string
  hasFilter: boolean
}

export interface APIKeyView {
  name: string
  mask: string
  enabled: boolean
}

// ConfigurationListItem is one row of the configurations table.
export interface ConfigurationListItem {
  name: string
  tags: Record<string, string>
  keyCount?: number
  ruleCount: number
}

// ConfigurationDetailData is the full configuration as the detail view renders
// it. api_keys is optional: only the gateway read API resolves the keys that
// point at a configuration; the CP entity body has no such back-reference.
export interface ConfigurationDetailData {
  name: string
  tags: Record<string, string>
  credentials: CredentialView[]
  bindings: ConfigBindingView[]
  passthroughBindings: PassthroughBindingView[]
  rules: RuleAttachmentView[]
  connectorBindings: ConnectorBindingView[]
  apiKeys?: APIKeyView[]
}

// --- gateway (redacted read API) mappers ---

interface GatewayRedactedSecret {
  length: number
  last4: string
}

interface GatewayBindingRow {
  protocol: string
  models?: string[]
  backend?: string
  group?: string
  alias?: string
  tags?: string[]
}

interface GatewayPassthroughBindingRow {
  family: string
  backend: string
  tags?: string[]
}

interface GatewayRuleAttachment {
  name: string
  condition_summary?: string
  action_types?: string[]
  behavior?: string
}

interface GatewayConnectorBinding {
  connector: string
  sampling?: number
  sampling_key?: string
  max_body_bytes?: number
  oversize_behaviour?: string
  filter?: unknown
}

interface GatewayAPIKeySummary {
  name: string
  secret: GatewayRedactedSecret
  enabled: boolean
}

interface GatewayConfigurationDetail {
  name: string
  credentials: Record<string, GatewayRedactedSecret>
  bindings: GatewayBindingRow[]
  passthrough_bindings: GatewayPassthroughBindingRow[]
  rules: GatewayRuleAttachment[]
  tags?: Record<string, string>
  connector_bindings?: GatewayConnectorBinding[]
  api_keys: GatewayAPIKeySummary[]
}

function maskRedacted(r: GatewayRedactedSecret): string {
  if (r.length === 0) return ""
  return `••••${r.last4}`
}

// configurationListItemFromSummary derives a list row from the gateway's
// configuration summary DTO.
export function configurationListItemFromSummary(s: {
  name: string
  key_count: number
  rule_count: number
  tags?: Record<string, string>
}): ConfigurationListItem {
  return {
    name: s.name,
    tags: s.tags ?? {},
    keyCount: s.key_count,
    ruleCount: s.rule_count,
  }
}

// configurationDetailFromGateway maps the gateway's redacted detail DTO into
// the view shape, preserving its real last4 credential mask.
export function configurationDetailFromGateway(d: GatewayConfigurationDetail): ConfigurationDetailData {
  return {
    name: d.name,
    tags: d.tags ?? {},
    credentials: Object.entries(d.credentials ?? {}).map(([backend, r]) => ({
      backend,
      mask: maskRedacted(r),
      length: r.length,
    })),
    bindings: (d.bindings ?? []).map((b) => ({
      protocol: b.protocol,
      models: (b.models ?? []).slice(),
      backend: b.backend,
      group: b.group,
      alias: b.alias,
      tags: (b.tags ?? []).slice(),
    })),
    passthroughBindings: (d.passthrough_bindings ?? []).map((p) => ({
      family: p.family,
      backend: p.backend,
      tags: (p.tags ?? []).slice(),
    })),
    rules: (d.rules ?? []).map((r) => ({
      name: r.name,
      conditionSummary: r.condition_summary,
      actionTypes: r.action_types,
      behavior: r.behavior,
    })),
    connectorBindings: (d.connector_bindings ?? []).map(connectorBindingFromGateway),
    apiKeys: (d.api_keys ?? []).map((k) => ({
      name: k.name,
      mask: maskRedacted(k.secret),
      enabled: k.enabled,
    })),
  }
}

function connectorBindingFromGateway(b: GatewayConnectorBinding): ConnectorBindingView {
  return {
    connector: b.connector,
    sampling: b.sampling,
    samplingKey: b.sampling_key,
    maxBodyBytes: b.max_body_bytes,
    oversizeBehaviour: b.oversize_behaviour,
    hasFilter: b.filter != null,
  }
}

// --- control-plane (entity body) mappers ---

interface ContractBindingRow {
  protocol: string
  models?: string[]
  backend?: string
  group?: string
  alias?: string
  tags?: string[]
}

interface ContractConnectorBinding {
  connector: string
  sampling?: number
  sampling_key?: string
  max_body_bytes?: number
  oversize_behaviour?: string
  filter?: unknown
}

interface ContractConfigBody {
  credentials?: Record<string, string>
  bindings?: ContractBindingRow[]
  passthrough_bindings?: Array<{ family: string; backend: string; tags?: string[] }>
  rule_names?: string[]
  tags?: Record<string, string>
  connector_bindings?: ContractConnectorBinding[]
}

// configurationListItemFromContract derives a list row from a stored
// configuration entity body. key_count is unavailable on the CP (no
// back-reference from configuration to API key), so it is left undefined.
export function configurationListItemFromContract(
  name: string,
  body: ContractConfigBody | Record<string, unknown>,
): ConfigurationListItem {
  const c = body as ContractConfigBody
  return {
    name,
    tags: c.tags ?? {},
    ruleCount: (c.rule_names ?? []).length,
  }
}

// configurationDetailFromContract maps a staged CP entity body into the view
// shape. The body carries credentials inline (ref/plaintext); this mapper
// DELIBERATELY discards every credential value, projecting only the backend key
// plus a "set"/empty presence marker. The raw value never reaches the view.
export function configurationDetailFromContract(
  name: string,
  body: ContractConfigBody | Record<string, unknown>,
): ConfigurationDetailData {
  const c = body as ContractConfigBody
  return {
    name,
    tags: c.tags ?? {},
    credentials: Object.entries(c.credentials ?? {}).map(([backend, value]) => ({
      backend,
      mask: String(value ?? "").length > 0 ? "set" : "",
    })),
    bindings: (c.bindings ?? []).map((b) => ({
      protocol: b.protocol,
      models: (b.models ?? []).slice(),
      backend: b.backend,
      group: b.group,
      alias: b.alias,
      tags: (b.tags ?? []).slice(),
    })),
    passthroughBindings: (c.passthrough_bindings ?? []).map((p) => ({
      family: p.family,
      backend: p.backend,
      tags: (p.tags ?? []).slice(),
    })),
    rules: (c.rule_names ?? []).map((rname) => ({ name: rname })),
    connectorBindings: (c.connector_bindings ?? []).map((b) => ({
      connector: b.connector,
      sampling: b.sampling,
      samplingKey: b.sampling_key,
      maxBodyBytes: b.max_body_bytes,
      oversizeBehaviour: b.oversize_behaviour,
      hasFilter: b.filter != null,
    })),
  }
}
