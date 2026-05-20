// Thin fetch wrapper that injects the cached Basic auth header on
// every /admin/api/v1/* call. A 401 response means the cached
// credentials were rejected — we drop them locally and surface a
// typed error so pages can route back to /login.
//
// Callers pass paths starting with "/api/v1/..."; apiFetch prepends
// the API_BASE prefix that matches internal/admin.Prefix on the Go
// side. Keeping the prefix in one place means hook callers don't
// have to thread it through.

import { auth } from "@/lib/auth"

const API_BASE = "/admin"

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

/**
 * Fetch JSON from the gateway's admin API. The Basic auth header is
 * attached automatically when credentials are cached. 401 clears the
 * cache and throws UnauthorizedError; other non-2xx throws APIError.
 */
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
  return res.json() as Promise<T>
}

/**
 * Validate the cached credentials by hitting /api/v1/auth/me. Returns
 * true on success, false on 401 (cache is cleared by apiFetch).
 */
export async function validateSession(): Promise<boolean> {
  try {
    await apiFetch<{ username: string }>("/api/v1/auth/me")
    return true
  } catch (e) {
    if (e instanceof UnauthorizedError) return false
    throw e
  }
}

/**
 * Fetch the gateway's build-time version from the unauthenticated
 * /api/v1/version endpoint. Used by the sidebar and login page so the
 * displayed version always matches the binary actually serving the
 * console.
 */
export async function fetchVersion(): Promise<string> {
  const res = await fetch(API_BASE + "/api/v1/version", { headers: { Accept: "application/json" } })
  if (!res.ok) {
    throw new APIError(res.status, res.statusText)
  }
  const body = (await res.json()) as { version: string }
  return body.version
}
