// Thin fetch wrapper for the control-plane console. Mirrors the gateway
// admin api.ts, but the CP serves the SPA and its API at the listener
// root (no /admin prefix), so API_BASE is empty. The cached Basic auth
// header is injected on every /api/v1/* call; a 401 drops the cached
// credentials and surfaces a typed error so pages route back to /login.

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
  body?: unknown
  constructor(status: number, message: string, body?: unknown) {
    super(message)
    this.name = "APIError"
    this.status = status
    this.body = body
  }
}

/**
 * Fetch JSON from the control-plane API. Attaches the cached Basic auth
 * header; 401 clears the cache and throws UnauthorizedError; other
 * non-2xx throws APIError (whose `body` carries the parsed JSON envelope
 * when the server sent one).
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
    let body: unknown
    if (text) {
      try {
        body = JSON.parse(text)
      } catch {
        // non-JSON error body — leave undefined
      }
    }
    throw new APIError(res.status, text || res.statusText, body)
  }
  if (res.status === 204) {
    return undefined as T
  }
  return res.json() as Promise<T>
}

/**
 * Human-readable text for a failed apiFetch. The CP error envelope is a flat
 * {"error": "..."} (e.g. publish validation failures, bad entity bodies), so
 * prefer that field; fall back to the error message.
 */
export function apiErrorText(e: unknown): string {
  if (e instanceof APIError && e.body && typeof e.body === "object" && "error" in e.body) {
    return String((e.body as { error: unknown }).error)
  }
  return e instanceof Error ? e.message : String(e)
}

/** Validate cached credentials against the authenticated /api/v1/auth/me. */
export async function validateSession(): Promise<boolean> {
  try {
    await apiFetch<{ username: string }>("/api/v1/auth/me")
    return true
  } catch (e) {
    if (e instanceof UnauthorizedError) return false
    throw e
  }
}

/** Fetch the control plane's build version from the unauthenticated endpoint. */
export async function fetchVersion(): Promise<string> {
  const res = await fetch(API_BASE + "/api/v1/version", { headers: { Accept: "application/json" } })
  if (!res.ok) {
    throw new APIError(res.status, res.statusText)
  }
  const body = (await res.json()) as { version: string }
  return body.version
}
