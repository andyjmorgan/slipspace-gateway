import {
  LayoutDashboard,
  Settings as SettingsIcon,
  ListTree,
  Server,
  SlidersHorizontal,
  Waypoints,
  Activity,
  Shield,
  KeyRound,
  Database,
} from "lucide-react"

// NavMeta is the single source of truth for a top-level section's nav entry:
// its route, label, icon, and accent colour. Both the sidebar rail and the
// page header read from it, so a section's icon + colour match by construction
// — change it here and both surfaces update together.
export type NavMeta = {
  to: string
  label: string
  icon: React.ComponentType<{ size?: number; className?: string; style?: React.CSSProperties }>
  // color is the icon accent — a distinct hue per section, chosen to read on
  // the dark theme.
  color: string
  section?: string
  // match lists the path prefixes this section owns, so detail/editor routes
  // (e.g. /backends/foo/edit) resolve to the parent section's icon. Defaults
  // to [to] when omitted.
  match?: string[]
}

export const NAV: NavMeta[] = [
  { to: "/dashboard", label: "Dashboard", icon: LayoutDashboard, color: "#60a5fa" },
  { to: "/messages", label: "Live messages", icon: Activity, color: "#4ade80" },
  { to: "/configurations", label: "Configurations", icon: SlidersHorizontal, color: "#a78bfa", section: "Configuration" },
  { to: "/rules", label: "Rules", icon: ListTree, color: "#f97316" },
  { to: "/backends", label: "Backends", icon: Server, color: "#38bdf8" },
  { to: "/policies", label: "Groups", icon: Shield, color: "#fb7185", match: ["/policies", "/groups"] },
  { to: "/connectors", label: "Connectors", icon: Database, color: "#2dd4bf" },
  { to: "/api-keys", label: "API keys", icon: KeyRound, color: "#facc15" },
  { to: "/bindings", label: "Bindings", icon: Waypoints, color: "#22d3ee" },
  { to: "/settings", label: "Settings", icon: SettingsIcon, color: "#94a3b8", section: "System" },
]

// navMetaForPath resolves a pathname to its owning section by longest matching
// prefix (so /backends/foo → Backends). Returns undefined for paths no section
// claims (e.g. /login).
export function navMetaForPath(pathname: string): NavMeta | undefined {
  let best: NavMeta | undefined
  let bestLen = -1
  for (const item of NAV) {
    for (const prefix of item.match ?? [item.to]) {
      if ((pathname === prefix || pathname.startsWith(prefix + "/")) && prefix.length > bestLen) {
        best = item
        bestLen = prefix.length
      }
    }
  }
  return best
}
