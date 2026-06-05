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

// apiBase is prepended to every "/api/v1/..." path. The gateway admin console
// mounts its API under /admin (the default); the telemetry console serves its
// API at the listener root and calls setApiBase("") at bootstrap. Kept as a
// module-level switch so the shared data hooks (dashboard, messages) work
// against either backend without threading the prefix through every caller.
let apiBase = "/admin"

/** setApiBase overrides the API prefix. Call once at SPA bootstrap, before any
 * fetch. Telemetry sets "" (root); the gateway keeps the "/admin" default. */
export function setApiBase(base: string): void {
  apiBase = base
}

export class UnauthorizedError extends Error {
  constructor() {
    super("unauthorized")
    this.name = "UnauthorizedError"
  }
}

export class APIError extends Error {
  status: number
  /**
   * body is the parsed JSON envelope the server sent with the error,
   * when there was one. Callers (e.g. the rule editor) read this to
   * render structured 409/422 payloads — `RuleConflictError` with the
   * `used_by` list, `RuleValidationError` with the underlying detail
   * string, etc.
   */
  body?: unknown
  constructor(status: number, message: string, body?: unknown) {
    super(message)
    this.name = "APIError"
    this.status = status
    this.body = body
  }
}

/**
 * Fetch JSON from the gateway's admin API. The Basic auth header is
 * attached automatically when credentials are cached. 401 clears the
 * cache and throws UnauthorizedError; other non-2xx throws APIError
 * (whose `body` field carries the parsed JSON body when the server
 * sent one, so callers can render structured error envelopes like the
 * 409 RuleConflictError shape).
 */
export async function apiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  const authHeader = auth.header()
  if (authHeader) {
    headers.set("Authorization", authHeader)
  }
  headers.set("Accept", "application/json")
  const res = await fetch(apiBase + path, { ...init, headers })
  if (res.status === 401) {
    auth.clear()
    throw new UnauthorizedError()
  }
  if (!res.ok) {
    const text = await res.text().catch(() => "")
    let body: unknown
    if (text) {
      try {
        body = JSON.parse(text)
      } catch {
        // non-JSON error body — leave body undefined
      }
    }
    throw new APIError(res.status, text || res.statusText, body)
  }
  // 204 No Content carries no body. Return undefined cast to T so
  // void-typed callers (DELETE) don't need a special branch.
  if (res.status === 204) {
    return undefined as T
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
  const res = await fetch(apiBase + "/api/v1/version", { headers: { Accept: "application/json" } })
  if (!res.ok) {
    throw new APIError(res.status, res.statusText)
  }
  const body = (await res.json()) as { version: string }
  return body.version
}
