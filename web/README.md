# sluice-gateway / web

Vite + React + shadcn-ui SPA for the management console. Embedded into the gateway binary at build time via `//go:embed` — see `internal/admin/static.go`.

## Stack

- Vite 8 · React 19 · TypeScript 6
- Tailwind 4 (via `@tailwindcss/vite`)
- shadcn-ui (new-york style; components in `src/components/ui/`)
- React Router 7
- lucide-react

## Dev loop

The dev server runs on `:5180` (pinned, `strictPort: true`) under `/admin` (matching the SPA's Vite `base`), and proxies `/admin/api/v1/*` to the gateway's admin listener. The gateway must be running with the admin console enabled.

```sh
# Terminal 1: gateway with admin enabled
SLUICE_ADMIN_PASSWORD=dev SLUICE_CONFIG_DIR=./config-dev \
  go run ./cmd/gateway
# (admin.yaml in config-dev/ already has enabled: true bind_addr: 127.0.0.1:8081)

# Terminal 2: Vite dev server
make web-dev
# or: cd web && npm run dev
```

Open <http://localhost:5180/admin>. Sign in with username `admin` and the password you exported above.

Override the proxy target with `SLUICE_ADMIN_URL=http://other-host:8081 npm run dev` if the gateway runs elsewhere.

## Production build

```sh
make web
```

emits to `../internal/admin/webdist/` (Vite `build.outDir`). `internal/admin/static.go` embeds this directory at compile time. `make build` runs this step before `go build` automatically.

The committed `placeholder.html` stands in for `index.html` when the SPA hasn't been built (fresh checkout, CI before `make web`), so `go build` and `go:embed` always succeed.

## Files

```
src/
  pages/         # LoginPage, DashboardPage, PlaceholderPage
  components/    # AppLayout, Sidebar, Topbar, ProtectedRoute, BrandMark
    atoms/       # ProviderChip, StatusPill, KPI, Tag, Sparkline, LineChart, Segmented, PanelCard
    ui/          # shadcn-ui generated components (button, card, input, etc.)
  lib/           # auth, api (fetch wrapper), dashboard (typed hook), fmt, theme, utils
  mock/          # data.ts — timeseries fixtures still used for charts pending /api/v1/dashboard/timeseries
  App.tsx        # router
  main.tsx       # bootstrap
  index.css      # design tokens (oklch palette, provider colors, light/dark)
```
