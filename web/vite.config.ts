import path from "node:path"
import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"

// Build output is dropped into the gateway's embed.FS target so a
// fresh `go build` picks up the latest SPA. The Makefile's `web`
// target invokes `npm run build` in this directory; the resulting
// internal/admin/webdist/ is what //go:embed reads.
//
// Dev mode proxies /api/v1 to the gateway's admin listener (default
// 0.0.0.0:8081 — see contracts/admin/admin.go). Override with
// SLUICE_ADMIN_URL when the gateway runs elsewhere.
const adminURL = process.env.SLUICE_ADMIN_URL ?? "http://localhost:8081"

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "../internal/admin/webdist",
    // emptyOutDir would wipe the committed placeholder.html and
    // .gitignore on every build. The Makefile's `web` target removes
    // only the previously-generated artefacts before invoking vite.
    emptyOutDir: false,
  },
  server: {
    port: 5180,
    strictPort: true,
    proxy: {
      "/api": {
        target: adminURL,
        changeOrigin: true,
      },
    },
  },
  preview: {
    port: 5180,
    strictPort: true,
  },
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
})
