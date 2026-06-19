// Auth state — the SPA caches the operator's Basic credentials in
// sessionStorage so subsequent fetches don't re-prompt. sessionStorage
// (not localStorage) clears on tab close, which matches the lifetime
// operators expect from a console.
//
// The stored value is the base64-encoded "admin:password" string ready
// to drop into an `Authorization: Basic ...` header. Decoding it is not
// supported — the SPA never needs the plaintext password back.

const KEY = "slipspace.basic"

function b64(s: string): string {
  // btoa expects a binary string. Passwords are ASCII in practice for
  // dev/internal deployments; if a future ops policy adds unicode we'll
  // need a TextEncoder + Uint8Array path.
  return btoa(s)
}

export const auth = {
  /** Returns the cached `Basic <token>` header value, or null. */
  header(): string | null {
    const v = sessionStorage.getItem(KEY)
    return v ? `Basic ${v}` : null
  },

  /** Cache credentials. Stored as base64(username:password). */
  store(username: string, password: string) {
    sessionStorage.setItem(KEY, b64(`${username}:${password}`))
  },

  /** Drop the cached credentials. */
  clear() {
    sessionStorage.removeItem(KEY)
  },

  /** True iff a credential is cached. Does not verify it server-side. */
  isLoggedIn(): boolean {
    return sessionStorage.getItem(KEY) != null
  },
}
