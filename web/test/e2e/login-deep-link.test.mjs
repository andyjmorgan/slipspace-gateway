// Browser-level test for login deep-link preservation in the telemetry console.
//
// Spawns the telemetry vite dev server, drives it with headless Chromium, and
// mocks /api/** from the bundled session-spans fixture. The fixture is served
// in two pages with the target span on the second, delayed page, so the test
// also covers the progressive-paint path where the #span= target resolves
// only after the first render.
//
// Run with `npm run test:e2e` (needs a Playwright Chromium:
// `npx playwright install chromium` once).

import { after, before, test } from "node:test"
import assert from "node:assert/strict"
import { spawn } from "node:child_process"
import { createServer } from "node:net"
import { dirname, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import { chromium } from "playwright"

const webDir = resolve(dirname(fileURLToPath(import.meta.url)), "../..")
const { SESSION_SPANS_FIXTURE, FIXTURE_SESSION_ID } = await import(
  resolve(webDir, "src/telemetry/mock/session-spans-fixture.ts")
)

const SID = FIXTURE_SESSION_ID
const TARGET_CID = "fx-c1"
const GOOD = "Basic " + Buffer.from("operator:correct").toString("base64")

// A span on the second page, so the hash resolves after progressive paint.
const split = SESSION_SPANS_FIXTURE.findIndex((s) => s.cid === TARGET_CID)
assert.ok(split > 0, "target cid must not be the first fixture span")

function freePort() {
  return new Promise((res, rej) => {
    const srv = createServer()
    srv.once("error", rej)
    srv.listen(0, "127.0.0.1", () => {
      const { port } = srv.address()
      srv.close(() => res(port))
    })
  })
}

let vite
let browser
let baseURL

before(async () => {
  const port = await freePort()
  baseURL = `http://127.0.0.1:${port}`
  vite = spawn(
    process.execPath,
    [resolve(webDir, "node_modules/vite/bin/vite.js"), "--config", "vite.telemetry.config.ts", "--host", "127.0.0.1", "--port", String(port), "--strictPort"],
    { cwd: webDir, stdio: ["ignore", "pipe", "pipe"] },
  )
  let log = ""
  vite.stdout.on("data", (d) => (log += d))
  vite.stderr.on("data", (d) => (log += d))
  const deadline = Date.now() + 60_000
  for (;;) {
    try {
      const r = await fetch(baseURL + "/", { headers: { accept: "text/html" } })
      if (r.ok) break
    } catch {
      /* not up yet */
    }
    if (Date.now() > deadline) throw new Error("vite dev server did not start:\n" + log)
    await new Promise((r) => setTimeout(r, 250))
  }
  browser = await chromium.launch({ headless: true })
})

after(async () => {
  await browser?.close()
  vite?.kill()
})

// newPage returns a page whose /api/** is served from the fixture. Requests
// without the GOOD credentials get a 401, so a wrong password exercises the
// live-401 bounce (useLoginRedirect) as well as the cold Guard redirect.
async function newPage() {
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 900 } })
  const page = await ctx.newPage()
  await page.route("**/api/**", async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    if (req.headers()["authorization"] !== GOOD) {
      return route.fulfill({ status: 401, contentType: "application/json", body: "{}" })
    }
    const spansPrefix = `/api/v1/sessions/${encodeURIComponent(SID)}/spans`
    if (url.pathname === spansPrefix) {
      if (!url.searchParams.get("cursor")) {
        return route.fulfill({ json: { spans: SESSION_SPANS_FIXTURE.slice(0, split), next_cursor: "page-2" } })
      }
      await new Promise((r) => setTimeout(r, 750))
      return route.fulfill({ json: { spans: SESSION_SPANS_FIXTURE.slice(split), next_cursor: "" } })
    }
    if (url.pathname.startsWith(spansPrefix + "/")) {
      const cid = decodeURIComponent(url.pathname.slice(spansPrefix.length + 1))
      const span = SESSION_SPANS_FIXTURE.find((s) => s.cid === cid)
      return span ? route.fulfill({ json: span }) : route.fulfill({ status: 404, body: "{}" })
    }
    return route.fulfill({ status: 404, contentType: "application/json", body: "{}" })
  })
  return page
}

async function signIn(page, user, password) {
  await page.getByLabel("Username").fill(user)
  await page.getByLabel("Password", { exact: true }).fill(password)
  await page.getByRole("button", { name: "Sign in" }).click()
}

async function expectInspectorOpen(page) {
  const dialog = page.getByRole("dialog", { name: "Span detail" })
  await dialog.waitFor({ state: "visible", timeout: 15_000 })
  assert.match(await dialog.innerText(), new RegExp(`span\\s+${TARGET_CID}`))
}

test("logged-out deep link survives sign-in and opens the span inspector", async () => {
  const page = await newPage()
  await page.goto(`${baseURL}/sessions/${SID}?view=all#span=${TARGET_CID}`)
  await page.waitForURL((u) => u.pathname === "/login")
  await signIn(page, "operator", "correct")
  await page.waitForURL((u) => u.pathname === `/sessions/${SID}`)
  const u = new URL(page.url())
  assert.equal(u.search, "?view=all")
  assert.equal(u.hash, `#span=${TARGET_CID}`)
  await expectInspectorOpen(page)
  // E2E_SCREENSHOT_DIR opts into a PNG of the opened inspector for visual review.
  if (process.env.E2E_SCREENSHOT_DIR) {
    await page.screenshot({ path: resolve(process.env.E2E_SCREENSHOT_DIR, "login-deep-link.png") })
  }
  await page.context().close()
})

test("the /lifecycle alias keeps the #span= hash through sign-in", async () => {
  const page = await newPage()
  await page.goto(`${baseURL}/sessions/${SID}/lifecycle#span=${TARGET_CID}`)
  await page.waitForURL((u) => u.pathname === "/login")
  await signIn(page, "operator", "correct")
  await page.waitForURL((u) => u.pathname === `/sessions/${SID}` && u.hash === `#span=${TARGET_CID}`)
  await expectInspectorOpen(page)
  await page.context().close()
})

test("a rejected password bounces back to login and still returns to the deep link", async () => {
  const page = await newPage()
  await page.goto(`${baseURL}/sessions/${SID}#span=${TARGET_CID}`)
  await page.waitForURL((u) => u.pathname === "/login")
  // The session page mounts, its API call 401s, and useLoginRedirect bounces
  // back to /login carrying the same deep link. Arm the wait before clicking:
  // the stop on the session page can be brief.
  const visited = page.waitForURL((u) => u.pathname === `/sessions/${SID}`)
  await signIn(page, "operator", "wrong")
  await visited
  await page.waitForURL((u) => u.pathname === "/login")
  await signIn(page, "operator", "correct")
  await page.waitForURL((u) => u.pathname === `/sessions/${SID}` && u.hash === `#span=${TARGET_CID}`)
  await expectInspectorOpen(page)
  await page.context().close()
})

test("no recorded target falls back to the dashboard", async () => {
  const page = await newPage()
  await page.goto(`${baseURL}/login`)
  await signIn(page, "operator", "correct")
  await page.waitForURL((u) => u.pathname === "/")
  await page.context().close()
})
