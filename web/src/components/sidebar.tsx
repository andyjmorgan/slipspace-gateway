import { useEffect, useState } from "react"
import { NavLink, useLocation } from "react-router"
import {
  LayoutDashboard,
  Settings as SettingsIcon,
  ListTree,
  Server,
  SlidersHorizontal,
  Waypoints,
  Activity,
  Shield,
} from "lucide-react"
import { cn } from "@/lib/utils"
import { fetchVersion } from "@/lib/api"

type NavItem = {
  to: string
  label: string
  icon: React.ComponentType<{ size?: number; className?: string }>
  section?: string
}

const NAV: NavItem[] = [
  { to: "/dashboard", label: "Dashboard", icon: LayoutDashboard },
  { to: "/messages", label: "Live messages", icon: Activity },
  { to: "/configurations", label: "Configurations", icon: SlidersHorizontal, section: "Read-only inspect" },
  { to: "/rules", label: "Rules", icon: ListTree },
  { to: "/backends", label: "Backends", icon: Server },
  { to: "/bindings", label: "Bindings", icon: Waypoints },
  { to: "/policies", label: "Groups", icon: Shield },
  { to: "/settings", label: "Settings", icon: SettingsIcon, section: "System" },
]

// Sidebar is the primary nav rail. On desktop (md+) it renders as a
// static column always docked to the left. On mobile it renders as a
// fixed overlay drawer that slides in when isOpen is true and dims the
// rest of the screen via the backdrop in AppLayout. Tapping a nav link
// closes the drawer so a touch user lands on the new route in the main
// pane instead of stranded behind the rail.
export function Sidebar({
  isOpen,
  onClose,
}: {
  isOpen: boolean
  onClose: () => void
}) {
  const version = useGatewayVersion()
  const loc = useLocation()
  let lastSection: string | undefined

  // Close the drawer whenever the route changes (mobile interaction).
  useEffect(() => {
    onClose()
    // Only react to path changes, not callback identity churn.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loc.pathname])

  return (
    <aside
      className={cn(
        "flex flex-col bg-[color:var(--bg-1)] border-r border-[color:var(--border)]",
        // Mobile: fixed overlay that slides in. Desktop: static column.
        "fixed inset-y-0 left-0 z-40 transform transition-transform duration-200",
        "md:static md:translate-x-0 md:transition-none",
        isOpen ? "translate-x-0" : "-translate-x-full md:translate-x-0",
      )}
      style={{ width: "var(--sidebar-w)" }}
      aria-hidden={!isOpen}
    >
      <div className="flex items-center gap-2.5 px-3.5 py-3 border-b border-[color:var(--border)]">
        <img
          src={`${import.meta.env.BASE_URL}sluice.svg`}
          alt=""
          width={28}
          height={28}
          className="size-7 rounded-md"
        />
        <div className="flex flex-col min-w-0">
          <span className="font-semibold tracking-tight text-[15px] leading-tight">sluice</span>
          <span
            className="mono text-[10.5px] text-[color:var(--text-4)] truncate"
            title={version ?? ""}
          >
            {version ?? "…"}
          </span>
        </div>
      </div>

      <nav className="flex-1 py-2 overflow-y-auto">
        {NAV.map((item) => {
          const isNew = item.section && item.section !== lastSection
          lastSection = item.section ?? lastSection
          return (
            <div key={item.to}>
              {isNew && (
                <div className="px-3.5 pt-3 pb-1 text-[10px] font-semibold uppercase tracking-[0.08em] text-[color:var(--text-4)]">
                  {item.section}
                </div>
              )}
              <NavLink
                to={item.to}
                className={({ isActive }) =>
                  cn(
                    "flex items-center gap-2.5 mx-2 my-0.5 px-2.5 py-1.5 rounded-[var(--radius)] text-[13px]",
                    isActive
                      ? "bg-[color:var(--bg-3)] text-[color:var(--text)]"
                      : "text-[color:var(--text-2)] hover:bg-[color:var(--hover)] hover:text-[color:var(--text)]",
                  )
                }
              >
                <item.icon size={14} className="opacity-80" />
                <span>{item.label}</span>
              </NavLink>
            </div>
          )
        })}
      </nav>

      <div className="flex items-center gap-2 px-3.5 py-3 border-t border-[color:var(--border)] text-[11.5px] text-[color:var(--text-3)]">
        <span
          className="inline-block size-1.5 rounded-full"
          style={{ background: "var(--ok)" }}
        />
        <span>gateway healthy</span>
        <span className="mono ml-auto text-[color:var(--text-4)]">:8081</span>
      </div>
    </aside>
  )
}

// useGatewayVersion fetches the binary's build-time version from the
// unauthenticated /api/v1/version endpoint once on mount. Returns null
// while in flight or on error — the sidebar renders "…" in either
// case rather than blocking the layout on it.
function useGatewayVersion(): string | null {
  const [version, setVersion] = useState<string | null>(null)
  useEffect(() => {
    let cancelled = false
    fetchVersion()
      .then((v) => {
        if (!cancelled) setVersion(v)
      })
      .catch(() => {
        // /version is best-effort cosmetic; swallow.
      })
    return () => {
      cancelled = true
    }
  }, [])
  return version
}
