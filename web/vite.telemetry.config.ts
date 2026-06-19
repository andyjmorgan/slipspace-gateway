import path from "node:path"
import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"

// The telemetry console is a second build target over the same src/ tree.
// Output lands in the telemetry binary's embed.FS dir
// (internal/arbiter/webdist). Unlike the gateway admin SPA (served under
// /admin), the telemetry service serves its console + API at the listener
// root, so base is "/". `npm run build:telemetry` invokes this config.
const telemetryURL = process.env.SLUICE_TELEMETRY_URL ?? "http://localhost:8686"

// In dev, vite serves the default index.html (the gateway-admin SPA, basename
// "/admin") at "/", so the telemetry console only loads via /index.telemetry.html.
// This plugin rewrites top-level HTML navigations to the telemetry entry so plain
// localhost:5181 (and a refresh on any client route) serves the right SPA. Dev
// only — the built telemetry binary serves index.telemetry.html at root itself.
function serveTelemetryEntryInDev() {
  return {
    name: "serve-telemetry-entry-dev",
    configureServer(server: { middlewares: { use: (fn: (req: any, res: any, next: () => void) => void) => void } }) {
      server.middlewares.use((req, _res, next) => {
        const url = req.url ?? "/"
        const accept = String(req.headers?.accept ?? "")
        if (
          req.method === "GET" &&
          accept.includes("text/html") &&
          !url.startsWith("/index.telemetry.html") &&
          !url.startsWith("/api") &&
          !url.startsWith("/src") &&
          !url.startsWith("/@") &&
          !url.startsWith("/node_modules")
        ) {
          req.url = "/index.telemetry.html"
        }
        next()
      })
    },
  }
}

export default defineConfig({
  plugins: [serveTelemetryEntryInDev(), react(), tailwindcss()],
  base: "/",
  build: {
    outDir: "../internal/arbiter/server/webdist",
    emptyOutDir: false,
    rollupOptions: {
      input: path.resolve(__dirname, "index.telemetry.html"),
    },
  },
  server: {
    port: 5181,
    strictPort: true,
    proxy: {
      "/api": {
        target: telemetryURL,
        changeOrigin: true,
      },
    },
  },
  preview: {
    port: 5181,
    strictPort: true,
  },
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
})
