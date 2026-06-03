// View shape + body→view mapper for the shared api-key list view. Kept out of
// api-key-views.tsx so that module only exports components
// (react-refresh/only-export-components) — same split as backend-views-model.ts
// and the *-form-model.ts files. The gateway feeds the list table from its live
// read API (APIKeyListItem, which carries name/configuration/enabled plus the
// redacted secret it renders in a gateway-only column); the control-plane
// console maps a staged entity's stored body into a row via the function here.
//
// SECURITY: a list row never carries a secret value. The CP entity body holds
// the plaintext secret, but the row shape deliberately drops it — the CP list
// shows name / configuration / enabled only, never a key.

// ApiKeyListItem is one row of the api-keys table — the non-secret columns
// shared by both consoles.
export interface ApiKeyListItem {
  name: string
  configuration: string
  enabled: boolean
}

// apiKeyListItemFromContract derives a list row from a stored api-key body,
// dropping the secret. `enabled` defaults to true when absent, matching the
// editor's contract mapping.
export function apiKeyListItemFromContract(name: string, body: Record<string, unknown>): ApiKeyListItem {
  return {
    name,
    configuration: typeof body.configuration === "string" ? body.configuration : "",
    enabled: body.enabled !== false,
  }
}
