import { useEffect, useState, type ReactNode } from "react"
import { NavLink, useLocation } from "react-router"
import { cn } from "@/lib/utils"
import { fetchVersion as gatewayFetchVersion } from "@/lib/api"
import { NAV, type NavMeta } from "@/lib/nav-meta"

// Sidebar is the primary nav rail, shared by both consoles. On desktop (md+)
// it renders as a static column docked left; on mobile a fixed overlay drawer
// that slides in when isOpen and dims the rest via the AppLayout backdrop.
//
// The rail is parameterised by its nav list, a version fetcher, and a footer
// node so the gateway admin SPA and the control-plane console render the
// identical component with their own contents. Defaults are the gateway's, so
// existing gateway callsites are unchanged.
export function Sidebar({
  isOpen,
  onClose,
  nav = NAV,
  versionFetch = gatewayFetchVersion,
  footer = <GatewayFooter />,
}: {
  isOpen: boolean
  onClose: () => void
  nav?: NavMeta[]
  versionFetch?: () => Promise<string>
  footer?: ReactNode
}) {
  const version = useVersion(versionFetch)
  const loc = useLocation()

  // Precompute, per nav item, the section header to show above it (only when
  // the section changes from the last section-bearing item). Done in a plain
  // loop rather than mutating during the JSX map.
  const sectionHeaders: (string | undefined)[] = []
  let carried: string | undefined
  for (const item of nav) {
    sectionHeaders.push(item.section && item.section !== carried ? item.section : undefined)
    if (item.section) carried = item.section
  }

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
          <span className="mono text-[10.5px] text-[color:var(--text-4)] truncate" title={version ?? ""}>
            {version ?? "…"}
          </span>
        </div>
      </div>

      <nav className="flex-1 py-2 overflow-y-auto">
        {nav.map((item, i) => {
          const header = sectionHeaders[i]
          return (
            <div key={item.to}>
              {header && (
                <div className="px-3.5 pt-3 pb-1 text-[10px] font-semibold uppercase tracking-[0.08em] text-[color:var(--text-4)]">
                  {header}
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
                <item.icon size={14} style={{ color: item.color }} className="shrink-0" />
                <span>{item.label}</span>
              </NavLink>
            </div>
          )
        })}
      </nav>

      {footer}
    </aside>
  )
}

function GatewayFooter() {
  return (
    <div className="flex items-center gap-2 px-3.5 py-3 border-t border-[color:var(--border)] text-[11.5px] text-[color:var(--text-3)]">
      <span className="inline-block size-1.5 rounded-full" style={{ background: "var(--ok)" }} />
      <span>gateway healthy</span>
      <span className="mono ml-auto text-[color:var(--text-4)]">:8081</span>
    </div>
  )
}

// SidebarFooter is the shared footer shape (status dot + label + meta), exported
// so the control-plane console can render a matching footer with its own text.
export function SidebarFooter({ label, meta }: { label: string; meta: string }) {
  return (
    <div className="flex items-center gap-2 px-3.5 py-3 border-t border-[color:var(--border)] text-[11.5px] text-[color:var(--text-3)]">
      <span className="inline-block size-1.5 rounded-full" style={{ background: "var(--ok)" }} />
      <span>{label}</span>
      <span className="mono ml-auto text-[color:var(--text-4)]">{meta}</span>
    </div>
  )
}

function useVersion(fetcher: () => Promise<string>): string | null {
  const [version, setVersion] = useState<string | null>(null)
  useEffect(() => {
    let cancelled = false
    fetcher()
      .then((v) => {
        if (!cancelled) setVersion(v)
      })
      .catch(() => {
        // best-effort cosmetic; swallow.
      })
    return () => {
      cancelled = true
    }
  }, [fetcher])
  return version
}
