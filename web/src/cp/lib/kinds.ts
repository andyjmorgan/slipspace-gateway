import type { EntityKind } from "./types"

// Per-type page metadata for the control-plane console: the URL segment, the
// list-page title + blurb. The route segments mirror the gateway console's
// (/backends, /configurations, …) so the two consoles navigate identically.

export interface KindMeta {
  kind: EntityKind
  route: string
  title: string
  sub: string
}

export const KIND_META: Record<EntityKind, KindMeta> = {
  configuration: {
    kind: "configuration",
    route: "configurations",
    title: "Configurations",
    sub: "Policy bundles — the credentials, bindings, rules, and connectors a set of API keys resolves to.",
  },
  rule: {
    kind: "rule",
    route: "rules",
    title: "Rules",
    sub: "The shared transform library — request/response rewrites referenced by configurations.",
  },
  backend: {
    kind: "backend",
    route: "backends",
    title: "Backends",
    sub: "Upstream connections shared across configurations — base URLs, per-protocol paths + auth, passthrough families.",
  },
  group: {
    kind: "group",
    route: "groups",
    title: "Groups",
    sub: "Resilience groups — failover / load-balance sets of backend targets with a circuit breaker.",
  },
  connector: {
    kind: "connector",
    route: "connectors",
    title: "Connectors",
    sub: "Record destinations the spool ships to — s3 / azure_blob / webhook / controlplane.",
  },
  api_key: {
    kind: "api_key",
    route: "api-keys",
    title: "API keys",
    sub: "Issued credentials, each resolving to a configuration the holder serves.",
  },
}

export const ROUTE_TO_KIND: Record<string, EntityKind> = Object.fromEntries(
  Object.values(KIND_META).map((m) => [m.route, m.kind]),
)

export function routeFor(kind: EntityKind): string {
  return KIND_META[kind].route
}
