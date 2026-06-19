import path from "node:path"
import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"

// The telemetry console is a second build target over the same src/ tree.
// Output lands in the telemetry binary's embed.FS dir
// (internal/arbiter/webdist). Unlike the gateway admin SPA (served under
// /admin), the telemetry service serves its console + API at the listener
// root, so base is "/". `npm run build:telemetry` invokes this config.
const telemetryURL = process.env.SLIPSPACE_TELEMETRY_URL ?? "http://localhost:8686"

export default defineConfig({
  plugins: [react(), tailwindcss()],
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
