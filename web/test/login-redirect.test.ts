// Unit tests for the login deep-link helper (src/lib/login-redirect.ts).
// Run with `npm test` (node:test + Node's built-in type stripping).

import { test } from "node:test"
import assert from "node:assert/strict"
import {
  LOGIN_PATH,
  locationPath,
  loginRedirectState,
  redirectTarget,
  safeRedirectPath,
} from "../src/lib/login-redirect.ts"

const FALLBACK = "/"

test("safeRedirectPath keeps same-origin paths with search and hash", () => {
  const cases = [
    "/",
    "/sessions",
    "/sessions/6f0a97a0-4f67-488d-bea7-f00157452e7f#span=82163fb6-5e76-4289-a90a-6cb58b1b9865",
    "/messages?provider=anthropic&model=claude#x",
    "/sessions/abc/lifecycle#span=fx-m3",
    "/security/findings?from=2026-09-29T00%3A00%3A00Z",
  ]
  for (const c of cases) {
    assert.equal(safeRedirectPath(c, FALLBACK), c, c)
  }
})

test("safeRedirectPath rejects open-redirect and junk inputs", () => {
  const cases: unknown[] = [
    undefined,
    null,
    42,
    {},
    "",
    "sessions",
    "https://evil.example/x",
    "http:/evil.example",
    "javascript:alert(1)",
    "data:text/html,hi",
    "//evil.example",
    "//evil.example/sessions",
    "/\\evil.example",
    "\\\\evil.example",
    "/\t/evil.example",
    "/\n/evil.example",
    "/\r/evil.example",
    "/x\u0000y",
    " /sessions",
    "/" + "a".repeat(4096),
  ]
  for (const c of cases) {
    assert.equal(safeRedirectPath(c, FALLBACK), FALLBACK, JSON.stringify(c))
  }
})

test("safeRedirectPath never returns to the login page itself", () => {
  for (const c of [LOGIN_PATH, `${LOGIN_PATH}?x=1`, `${LOGIN_PATH}#a`, `${LOGIN_PATH}/`, "/a/../login"]) {
    assert.equal(safeRedirectPath(c, "/dashboard"), "/dashboard", c)
  }
  // A path that merely starts with the same letters is not the login page.
  assert.equal(safeRedirectPath("/loginhistory", FALLBACK), "/loginhistory")
})

test("safeRedirectPath normalises dot segments without leaving the origin", () => {
  assert.equal(safeRedirectPath("/a/../../sessions", FALLBACK), "/sessions")
  assert.equal(safeRedirectPath("/./%2F%2Fevil.example", FALLBACK), "/%2F%2Fevil.example")
})

test("redirectTarget reads router state defensively", () => {
  assert.equal(redirectTarget(undefined, "/dashboard"), "/dashboard")
  assert.equal(redirectTarget(null, "/dashboard"), "/dashboard")
  assert.equal(redirectTarget("from", "/dashboard"), "/dashboard")
  assert.equal(redirectTarget({}, "/dashboard"), "/dashboard")
  assert.equal(redirectTarget({ from: 7 }, "/dashboard"), "/dashboard")
  assert.equal(redirectTarget({ from: "//evil.example" }, "/dashboard"), "/dashboard")
  assert.equal(redirectTarget({ from: "/rules/r1?x=1" }, "/dashboard"), "/rules/r1?x=1")
})

// The guard → login → return round-trip at the helper level: whatever
// location the gate records is exactly where the login page sends the operator.
// The browser-level flow (real router, real inspector modal) is covered by
// test/e2e/login-deep-link.test.mjs.
test("guard state round-trips the full location through login", () => {
  const loc = {
    pathname: "/sessions/6f0a97a0-4f67-488d-bea7-f00157452e7f",
    search: "?tab=timeline",
    hash: "#span=82163fb6-5e76-4289-a90a-6cb58b1b9865",
  }
  const state = loginRedirectState(loc)
  assert.deepEqual(state, { from: locationPath(loc) })
  assert.equal(
    redirectTarget(state, FALLBACK),
    "/sessions/6f0a97a0-4f67-488d-bea7-f00157452e7f?tab=timeline#span=82163fb6-5e76-4289-a90a-6cb58b1b9865",
  )
  assert.equal(redirectTarget(loginRedirectState({ pathname: "/" }), "/dashboard"), "/")
})
