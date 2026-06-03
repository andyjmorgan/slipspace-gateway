// Control-plane config write API client. Editing entities is permissive
// (the working set may be temporarily inconsistent); publish is the gate
// that composes + validates the whole config before activating it.

import { apiFetch } from "./api"
import type { ConfigChange, ConfigEntity, ConfigVersion, PublishResult } from "./types"

export function listEntities(): Promise<ConfigEntity[]> {
  return apiFetch<ConfigEntity[]>("/api/v1/config/entities")
}

export function getEntity(kind: string, name: string): Promise<ConfigEntity> {
  return apiFetch<ConfigEntity>(`/api/v1/config/entities/${encodeURIComponent(kind)}/${encodeURIComponent(name)}`)
}

export function putEntity(kind: string, name: string, body: unknown): Promise<unknown> {
  return apiFetch(`/api/v1/config/entities/${encodeURIComponent(kind)}/${encodeURIComponent(name)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

export function deleteEntity(kind: string, name: string): Promise<void> {
  return apiFetch<void>(`/api/v1/config/entities/${encodeURIComponent(kind)}/${encodeURIComponent(name)}`, {
    method: "DELETE",
  })
}

export function listVersions(): Promise<ConfigVersion[]> {
  return apiFetch<ConfigVersion[]>("/api/v1/config/versions")
}

export function listChanges(limit = 50): Promise<ConfigChange[]> {
  return apiFetch<ConfigChange[]>(`/api/v1/config/changes?limit=${limit}`)
}

export function publish(): Promise<PublishResult> {
  return apiFetch<PublishResult>("/api/v1/config/publish", { method: "POST" })
}

export function activateVersion(id: string): Promise<unknown> {
  return apiFetch(`/api/v1/config/versions/${encodeURIComponent(id)}/activate`, { method: "POST" })
}
