// API-key editor model. The entity name (storage key) doubles as the key's
// label. Kept out of api-key-form.tsx so that module only exports components.

export interface ApiKeyFormState {
  name: string
  secret: string
  configuration: string
  enabled: boolean
  id?: string
}

export function emptyApiKeyForm(): ApiKeyFormState {
  return { name: "", secret: "", configuration: "", enabled: true }
}

export function apiKeyFormFromContract(name: string, body: Record<string, unknown>): ApiKeyFormState {
  return {
    name,
    secret: typeof body.secret === "string" ? body.secret : "",
    configuration: typeof body.configuration === "string" ? body.configuration : "",
    enabled: body.enabled !== false,
    id: typeof body.id === "string" ? body.id : undefined,
  }
}

export function apiKeyFormToContract(form: ApiKeyFormState): { name: string; secret: string; configuration: string; enabled: boolean; id?: string } {
  const out = {
    name: form.name.trim(),
    secret: form.secret.trim(),
    configuration: form.configuration.trim(),
    enabled: form.enabled,
  }
  return form.id ? { ...out, id: form.id } : out
}
