// Thin fetch wrapper that injects the cached Basic auth header on
// every /api/v1/* call. A 401 response means the cached credentials
// were rejected — we drop them locally and surface a typed error so
// pages can route back to /login.

import { auth } from "@/lib/auth"

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
  const res = await fetch(path, { ...init, headers })
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
