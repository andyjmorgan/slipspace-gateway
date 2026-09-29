// Login deep-link preservation, shared by the admin and telemetry consoles.
//
// When a gate bounces an operator to /login it records where they were
// headed (pathname + search + hash, router-relative) in router state; after
// sign-in the login page sends them back there. The hash is load-bearing:
// the telemetry session view opens its span inspector from `#span=<cid>`.
//
// The return target is sanitised before use so the round-trip can never be
// turned into an open redirect: only same-origin, router-relative paths
// survive. This module is dependency-free so node:test can exercise it
// directly (web/test/login-redirect.test.ts).

/** LOGIN_PATH is the router-relative path of both consoles' sign-in page. */
export const LOGIN_PATH = "/login"

/** LoginRedirectState is the router state a gate hands to the login page. */
export type LoginRedirectState = { from: string }

/** RouterLocation is the subset of a react-router Location we read. */
export type RouterLocation = { pathname: string; search?: string; hash?: string }

// sentinelOrigin resolves candidate paths against a throwaway origin so the
// URL parser can tell us whether the input escapes to another host.
const sentinelOrigin = "http://login-redirect.invalid"

// maxRedirectLength bounds the stored target; real console paths are far
// shorter, and anything larger is not worth parsing.
const maxRedirectLength = 2048

/** locationPath flattens a router location to `pathname + search + hash`. */
export function locationPath(loc: RouterLocation): string {
  return `${loc.pathname}${loc.search ?? ""}${loc.hash ?? ""}`
}

/** loginRedirectState builds the state a gate attaches to its /login redirect. */
export function loginRedirectState(loc: RouterLocation): LoginRedirectState {
  return { from: locationPath(loc) }
}

// hasUnsafeChar rejects backslashes (browsers treat `\` as `/`, so `/\evil`
// becomes `//evil`) and ASCII control characters (the URL parser strips tab
// and newline, so `/\t/evil` would collapse to `//evil`).
function hasUnsafeChar(s: string): boolean {
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i)
    if (c === 0x5c || c < 0x20 || c === 0x7f) return true
  }
  return false
}

/**
 * safeRedirectPath returns `raw` normalised as a router-relative path when it
 * is a same-origin path, else `fallback`. It rejects non-strings, absolute
 * URLs (`https://…`, `javascript:…`), protocol-relative `//host`, backslash
 * and control-character tricks, and the login page itself (no loops).
 */
export function safeRedirectPath(raw: unknown, fallback: string): string {
  if (typeof raw !== "string" || raw.length === 0 || raw.length > maxRedirectLength) return fallback
  if (!raw.startsWith("/") || raw.startsWith("//") || hasUnsafeChar(raw)) return fallback
  let u: URL
  try {
    u = new URL(raw, sentinelOrigin)
  } catch {
    return fallback
  }
  if (u.origin !== sentinelOrigin) return fallback
  if (u.pathname === LOGIN_PATH || u.pathname.startsWith(`${LOGIN_PATH}/`)) return fallback
  return `${u.pathname}${u.search}${u.hash}`
}

/**
 * redirectTarget reads the `from` a gate left in router state and returns the
 * sanitised post-login destination, or `fallback` when absent or unsafe.
 */
export function redirectTarget(state: unknown, fallback: string): string {
  if (typeof state !== "object" || state === null || !("from" in state)) return fallback
  return safeRedirectPath((state as { from: unknown }).from, fallback)
}
