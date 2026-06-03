// View shape + body→view mapper for the shared connector list view. Kept out of
// connector-views.tsx so that module only exports components
// (react-refresh/only-export-components) — same split as the *-form-model.ts
// files. The gateway feeds this view from its live read API (Connector, which is
// structurally identical); the control-plane console maps a staged entity's
// ConnectorContract body into it via the function here.

import type { ConnectorContract } from "@/components/config-editors/connector-form-model"

// ConnectorListItem is one row of the connectors table — the type, the resolved
// destination string, and the auth mode (if any).
export interface ConnectorListItem {
  name: string
  type: string
  destination: string
  auth_mode?: string
}

// connectorDestination renders the per-type destination summary the list shows.
export function connectorDestination(c: ConnectorContract | Record<string, unknown>): string {
  const b = c as ConnectorContract
  if (b.type === "s3") return `${b.bucket ?? "?"}${b.prefix ? "/" + b.prefix : ""} · ${b.region ?? ""}`
  if (b.type === "azure_blob") return `${b.account ?? "?"} / ${b.container ?? "?"}`
  if (b.type === "webhook" || b.type === "controlplane") return b.url ?? "?"
  return ""
}

// connectorListItemFromContract derives a list row from a stored connector body.
export function connectorListItemFromContract(name: string, body: ConnectorContract | Record<string, unknown>): ConnectorListItem {
  const b = body as ConnectorContract
  return {
    name,
    type: b.type ?? "",
    destination: connectorDestination(b),
    auth_mode: b.auth?.mode,
  }
}
