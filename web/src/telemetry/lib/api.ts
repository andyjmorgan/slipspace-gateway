// Telemetry console fetch wrapper. Identical Basic-auth flow to the gateway
// admin SPA (src/lib/api.ts), but the telemetry service serves its API at the
// listener root (no /admin prefix), so API_BASE is empty. This is the
// telemetry app's injected client — the shared types come from src/lib, only
// the base + auth wiring differ.

import { auth } from "@/lib/auth"

const API_BASE = ""

export class UnauthorizedError extends Error {
  constructor() {
    super("unauthorized")
    this.name = "UnauthorizedError"
  }
}

export class APIError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = "APIError"
    this.status = status
  }
}

/** Fetch JSON from the telemetry API, attaching the cached Basic header. */
export async function apiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  const authHeader = auth.header()
  if (authHeader) {
    headers.set("Authorization", authHeader)
  }
  headers.set("Accept", "application/json")
  const res = await fetch(API_BASE + path, { ...init, headers })
  if (res.status === 401) {
    auth.clear()
    throw new UnauthorizedError()
  }
  if (!res.ok) {
    const text = await res.text().catch(() => "")
    throw new APIError(res.status, text || res.statusText)
  }
  if (res.status === 204) {
    return undefined as T
  }
  return res.json() as Promise<T>
}
