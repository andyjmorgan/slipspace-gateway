// Wire types for the control-plane read API, mirroring the Go handler
// view structs (internal/controlplane/http.go GatewayView and
// drift.go driftRow). configdb types carry no JSON tags, so the API
// shape is pinned in those handlers.

/** Liveness derived by the fleet handler from last_seen age. */
export type FleetStatus = "online" | "stale" | "offline"

export interface FleetGateway {
  id: string
  version: string
  labels?: Record<string, string>
  cached_config_hashes?: string[]
  registered_at: string
  last_seen: string
  status: FleetStatus
}

/** Per-gateway config-drift verdict vs the CP's current served closures. */
export type DriftStatus = "current" | "drifted" | "unknown"

export interface DriftRow {
  gateway_id: string
  version: string
  status: DriftStatus
  cached_hashes: string[] | null
}

/** Config entity kinds the CP stores (compose.go KindX constants). */
export const ENTITY_KINDS = [
  "backend",
  "group",
  "configuration",
  "api_key",
  "rule",
  "connector",
] as const
export type EntityKind = (typeof ENTITY_KINDS)[number]

/** One editable config entity. Body is the contract type as JSON. */
export interface ConfigEntity {
  kind: string
  name: string
  body: unknown
  updated_at: string
  updated_by: string
}

/** A published, immutable config version. */
export interface ConfigVersion {
  id: string
  hash: string
  published_at: string
  published_by: string
  active: boolean
}

/** One entry in the change audit log. */
export interface ConfigChange {
  kind: string
  name: string
  op: string
  old_body?: unknown
  new_body?: unknown
  changed_by: string
  changed_at: string
}

/** Result of a successful publish. */
export interface PublishResult {
  version: string
  hash: string
  status: string
}
