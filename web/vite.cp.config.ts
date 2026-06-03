import path from "node:path"
import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"

// Second Vite delivery: the control-plane console. It reuses the gateway
// SPA's component library + design system (the @ alias resolves to the
// shared web/src) but has its own entry (index.cp.html → src/cp), its own
// router, and its own build output embedded into the cmd/api binary
// (internal/controlplane/webdist). Unlike the gateway admin SPA it is
// served at the listener root, so base is "/".
//
// `make web-cp` invokes `vite build --config vite.cp.config.ts`; the dev
// server proxies /api/v1/* to the control-plane HTTP listener (:8484).
const cpURL = process.env.SLUICE_CP_URL ?? "http://localhost:8484"

export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: "/",
  build: {
    outDir: "../internal/controlplane/webdist",
    emptyOutDir: false,
    rollupOptions: {
      input: path.resolve(__dirname, "index.cp.html"),
    },
  },
  server: {
    port: 5181,
    strictPort: true,
    proxy: {
      "/api": {
        target: cpURL,
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
