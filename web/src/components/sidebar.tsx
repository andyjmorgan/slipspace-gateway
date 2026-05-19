import { NavLink } from "react-router"
import {
  LayoutDashboard,
  Settings as SettingsIcon,
  KeyRound,
  ListTree,
  Server,
  SlidersHorizontal,
} from "lucide-react"
import { cn } from "@/lib/utils"

type NavItem = {
  to: string
  label: string
  icon: React.ComponentType<{ size?: number; className?: string }>
  section?: string
}

const NAV: NavItem[] = [
  { to: "/dashboard", label: "Dashboard", icon: LayoutDashboard },
  { to: "/configurations", label: "Configurations", icon: SlidersHorizontal, section: "Manage" },
  { to: "/apikeys", label: "API Keys", icon: KeyRound },
  { to: "/rules", label: "Rules", icon: ListTree },
  { to: "/providers", label: "Providers", icon: Server, section: "Read-only" },
  { to: "/settings", label: "Settings", icon: SettingsIcon, section: "System" },
]

export function Sidebar() {
  let lastSection: string | undefined
  return (
    <aside
      className="hidden md:flex flex-col bg-[color:var(--bg-1)] border-r border-[color:var(--border)]"
      style={{ width: "var(--sidebar-w)" }}
    >
      <div className="flex items-center gap-2.5 px-3.5 py-3 border-b border-[color:var(--border)]">
        <img
          src={`${import.meta.env.BASE_URL}sluice.png`}
          alt=""
          width={28}
          height={28}
          className="size-7 rounded-md"
        />
        <span className="font-semibold tracking-tight text-[15px]">sluice</span>
        <span className="mono ml-auto text-[10.5px] text-[color:var(--text-4)]">v1.1.0-rc2</span>
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
