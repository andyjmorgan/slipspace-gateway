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
