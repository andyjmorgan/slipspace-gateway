import {
  Server as ServerIcon,
  SlidersHorizontal,
  ListTree,
  Server,
  Shield,
  Database,
  KeyRound,
  Waypoints,
  Activity,
  History,
} from "lucide-react"
import type { NavMeta } from "@/lib/nav-meta"

// CP_NAV is the control-plane console's sidebar. It mirrors the gateway's
// configuration items (same icons/labels/colours) minus Dashboard + Settings,
// and adds the CP-only sections: Fleet at the top, Observability + Versions
// below. Shared with the gateway via the parameterised Sidebar component.
export const CP_NAV: NavMeta[] = [
  { to: "/fleet", label: "Fleet", icon: ServerIcon, color: "#60a5fa", section: "Fleet" },

  { to: "/configurations", label: "Configurations", icon: SlidersHorizontal, color: "#a78bfa", section: "Configuration" },
  { to: "/rules", label: "Rules", icon: ListTree, color: "#f97316" },
  { to: "/backends", label: "Backends", icon: Server, color: "#38bdf8" },
  { to: "/groups", label: "Groups", icon: Shield, color: "#fb7185" },
  { to: "/connectors", label: "Connectors", icon: Database, color: "#2dd4bf" },
  { to: "/api-keys", label: "API keys", icon: KeyRound, color: "#facc15" },
  { to: "/bindings", label: "Bindings", icon: Waypoints, color: "#22d3ee" },

  { to: "/observability", label: "Observability", icon: Activity, color: "#4ade80", section: "Operations" },
  { to: "/versions", label: "Versions", icon: History, color: "#94a3b8" },
]
