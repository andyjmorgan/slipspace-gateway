import { useLocation, useNavigate } from "react-router"
import { LogOut, Menu, Moon, Sun } from "lucide-react"
import { Button } from "@/components/ui/button"
import { auth } from "@/lib/auth"
import { useTheme } from "@/lib/theme"

export type TopbarTitles = Record<string, { t: string; s: string }>

const GATEWAY_TITLES: TopbarTitles = {
  "/dashboard": { t: "Dashboard", s: "metrics over the last 24h" },
  "/configurations": { t: "Configurations", s: "policy bundles · rule chains" },
  "/apikeys": { t: "API Keys", s: "internal services holding sluice secrets" },
  "/rules": { t: "Rules", s: "shared library — referenced by configurations" },
  "/backends": { t: "Backends", s: "backends.yaml — read-only" },
  "/bindings": { t: "Bindings", s: "model → backend mapping — read-only" },
  "/settings": { t: "Settings", s: "gateway config & telemetry destinations" },
}

// Topbar is shared by both consoles; titles defaults to the gateway map so its
// callsites stay unchanged, and the control-plane console passes its own.
export function Topbar({
  onMenuToggle,
  titles = GATEWAY_TITLES,
}: {
  onMenuToggle: () => void
  titles?: TopbarTitles
}) {
  const loc = useLocation()
  const nav = useNavigate()
  const [theme, , toggle] = useTheme()
  const cur = titles[loc.pathname] ?? { t: "", s: "" }
  const onLogout = () => {
    auth.clear()
    nav("/login", { replace: true })
  }
  return (
    <div
      className="flex items-center gap-3 border-b border-[color:var(--border)] bg-[color:var(--bg-1)] px-4"
      style={{ height: "var(--header-h)" }}
    >
      <Button
        variant="ghost"
        size="icon"
        className="md:hidden"
        onClick={onMenuToggle}
        aria-label="Open navigation"
        title="Open navigation"
      >
        <Menu />
      </Button>
      <div className="min-w-0">
        <div className="text-[14px] font-semibold tracking-tight">{cur.t}</div>
        <div className="mono text-[11.5px] text-[color:var(--text-3)]">{cur.s}</div>
      </div>
      <div className="flex-1" />
      <Button
        variant="ghost"
        size="icon"
        onClick={toggle}
        aria-label="Toggle theme"
        title="Toggle theme"
      >
        {theme === "dark" ? (
          <Sun style={{ color: "#f59e0b" }} />
        ) : (
          <Moon style={{ color: "#6366f1" }} />
        )}
      </Button>
      <Button variant="ghost" size="icon" onClick={onLogout} aria-label="Log out" title="Log out">
        <LogOut style={{ color: "#f87171" }} />
      </Button>
    </div>
  )
}
