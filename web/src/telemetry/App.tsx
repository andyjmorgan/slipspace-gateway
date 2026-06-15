import { useEffect, useState } from "react"
import { NavLink, Navigate, Outlet, Route, Routes, useLocation, useNavigate, useParams } from "react-router"
import { Eye, EyeOff, LayoutDashboard, ListTree, LogOut, Menu, Moon, MessagesSquare, Settings2, ShieldAlert, Sun } from "lucide-react"
import { auth } from "@/lib/auth"
import { useTheme } from "@/lib/theme"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { DashboardPage } from "./pages/dashboard"
import { MessagesPage } from "./pages/messages"
import { SessionsPage } from "./pages/sessions"
import { SessionLifecyclePage } from "./pages/session-lifecycle"
import { SecurityPage } from "./pages/security"
import { SecurityAuditPage } from "./pages/security-audit"
import { SettingsPage } from "./pages/settings"

// App is the telemetry console root: a Basic-auth login gate wrapping the
// shared app shell (sidebar + topbar) the gateway admin console uses, themed
// from the same design tokens. The console is read-only — it only ever
// observes the telemetry store; there are no write surfaces.
export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route element={<Guard />}>
        <Route element={<TelemetryLayout />}>
          <Route index element={<DashboardPage />} />
          <Route path="messages" element={<MessagesPage />} />
          {/* Security is a section: Findings (flagged traffic) + Audit (scan
              failures). /security redirects to Findings for old links. */}
          <Route path="security" element={<Navigate to="/security/findings" replace />} />
          <Route path="security/findings" element={<SecurityPage />} />
          <Route path="security/audit" element={<SecurityAuditPage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="sessions" element={<SessionsPage />} />
          <Route path="sessions/:id" element={<SessionLifecyclePage />} />
          <Route path="sessions/:id/lifecycle" element={<LifecycleAlias />} />
        </Route>
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}

// LifecycleAlias keeps pre-existing /sessions/:id/lifecycle links alive now
// that the lifecycle dashboard IS the session view at /sessions/:id. The
// redirect preserves the #span= deep-link hash.
function LifecycleAlias() {
  const { id } = useParams<{ id?: string }>()
  const { hash } = useLocation()
  if (!id) return <Navigate to="/sessions" replace />
  return <Navigate to={`/sessions/${encodeURIComponent(id)}${hash}`} replace />
}

// Guard redirects to /login when no Basic credentials are cached. The pages
// themselves still handle a live 401 (rejected creds) by routing back here.
function Guard() {
  if (!auth.isLoggedIn()) {
    return <Navigate to="/login" replace />
  }
  return <Outlet />
}

// NavItem is one sidebar link; NavSection is a labeled group with indented child
// links (Security → Findings + Audit); NavDivider is a subtle rule between
// groups. The nav is a flat ordered list so group boundaries are explicit in the
// data, not implied by render-time slicing.
type NavLeaf = { to: string; label: string; end: boolean }
type NavItem = NavLeaf & { icon: typeof LayoutDashboard }
type NavSection = { label: string; icon: typeof LayoutDashboard; items: NavLeaf[] }
type NavDivider = { divider: true }
type NavEntry = NavItem | NavSection | NavDivider

const isDivider = (e: NavEntry): e is NavDivider => "divider" in e
const isSection = (e: NavEntry): e is NavSection => "items" in e

const NAV: NavEntry[] = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard, end: true },
  { divider: true },
  { to: "/sessions", label: "Sessions", icon: MessagesSquare, end: false },
  {
    label: "Security",
    icon: ShieldAlert,
    items: [
      { to: "/security/findings", label: "Findings", end: false },
      { to: "/security/audit", label: "Audit", end: false },
    ],
  },
  { to: "/messages", label: "Messages", icon: ListTree, end: false },
  { divider: true },
  { to: "/settings", label: "Settings", icon: Settings2, end: false },
]

function TelemetryLayout() {
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  // Close the mobile nav drawer on Escape — the drawer behaves as a modal
  // overlay (scrim + above content), so it needs a keyboard dismiss too.
  useEffect(() => {
    if (!mobileNavOpen) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setMobileNavOpen(false)
    }
    document.addEventListener("keydown", onKey)
    return () => document.removeEventListener("keydown", onKey)
  }, [mobileNavOpen])
  return (
    <div className="flex min-h-screen bg-[color:var(--bg)]">
      <TelemetrySidebar isOpen={mobileNavOpen} onClose={() => setMobileNavOpen(false)} />
      {mobileNavOpen && (
        <button
          type="button"
          aria-label="Close navigation"
          className="fixed inset-0 z-30 bg-black/40 md:hidden"
          onClick={() => setMobileNavOpen(false)}
        />
      )}
      <div className="flex flex-col flex-1 min-w-0">
        <TelemetryTopbar onMenuToggle={() => setMobileNavOpen((v) => !v)} />
        <div className="flex-1 px-4 sm:px-7 pt-6 pb-18 overflow-auto">
          <Outlet />
        </div>
      </div>
    </div>
  )
}

function TelemetrySidebar({ isOpen, onClose }: { isOpen: boolean; onClose: () => void }) {
  return (
    <aside
      className={cn(
        "fixed inset-y-0 left-0 z-40 flex w-[var(--sidebar-w)] flex-col border-r border-[color:var(--border)] bg-[color:var(--bg-1)] transition-transform md:static md:translate-x-0",
        isOpen ? "translate-x-0" : "-translate-x-full",
      )}
    >
      <div className="flex items-center gap-2 px-4 h-[var(--header-h)] border-b border-[color:var(--border)]">
        <img src="/arbiter.svg" alt="" width={18} height={18} className="rounded-[4px]" />
        <span className="font-semibold tracking-[-0.01em]">Arbiter</span>
      </div>
      <nav className="flex-1 px-2 py-3 flex flex-col gap-0.5">
        {NAV.map((item, i) => {
          if (isDivider(item)) {
            return <hr key={`div-${i}`} className="my-2 border-t border-[color:var(--border)]" aria-hidden="true" />
          }
          if (isSection(item)) {
            return (
              <div key={item.label} className="flex flex-col gap-0.5">
                <div className="flex items-center gap-2.5 px-3 pt-2 pb-1 text-[11px] font-medium uppercase tracking-[0.07em] text-[color:var(--text-4)]">
                  <item.icon size={16} />
                  {item.label}
                </div>
                {item.items.map((sub) => (
                  <SidebarLink key={sub.to} to={sub.to} end={sub.end} onClick={onClose} nested>
                    {sub.label}
                  </SidebarLink>
                ))}
              </div>
            )
          }
          return (
            <SidebarLink key={item.to} to={item.to} end={item.end} onClick={onClose} icon={item.icon}>
              {item.label}
            </SidebarLink>
          )
        })}
      </nav>
    </aside>
  )
}

// SidebarLink is one nav link, shared by top-level items (with a leading icon)
// and section children (nested: no icon, indented to align under the section
// label). Active state is the same filled treatment in both cases.
function SidebarLink({
  to,
  end,
  onClick,
  icon: Icon,
  nested,
  children,
}: {
  to: string
  end: boolean
  onClick: () => void
  icon?: typeof LayoutDashboard
  nested?: boolean
  children: React.ReactNode
}) {
  return (
    <NavLink
      to={to}
      end={end}
      onClick={onClick}
      className={({ isActive }) =>
        cn(
          "flex items-center gap-2.5 rounded-[var(--radius)] py-2 text-[13px] transition-colors",
          nested ? "pl-9 pr-3" : "px-3",
          isActive
            ? "bg-[color:var(--hover)] text-[color:var(--text)]"
            : "text-[color:var(--text-3)] hover:bg-[color:var(--hover)] hover:text-[color:var(--text)]",
        )
      }
    >
      {Icon && <Icon size={16} />}
      {children}
    </NavLink>
  )
}

function TelemetryTopbar({ onMenuToggle }: { onMenuToggle: () => void }) {
  const [theme, , toggle] = useTheme()
  const nav = useNavigate()
  const logout = () => {
    auth.clear()
    nav("/login", { replace: true })
  }
  return (
    <header className="flex items-center gap-2 px-4 sm:px-7 h-[var(--header-h)] border-b border-[color:var(--border)] bg-[color:var(--bg)]">
      <Button variant="ghost" size="icon-sm" className="md:hidden" onClick={onMenuToggle} aria-label="Toggle navigation">
        <Menu />
      </Button>
      <div className="flex-1" />
      <Button variant="ghost" size="icon-sm" onClick={toggle} aria-label="Toggle theme">
        {theme === "dark" ? <Sun /> : <Moon />}
      </Button>
      <Button variant="ghost" size="sm" onClick={logout} aria-label="Log out">
        <LogOut /> <span className="hidden sm:inline">Log out</span>
      </Button>
    </header>
  )
}

// LoginPage is the telemetry console's Basic-auth gate. It mirrors the gateway
// admin console's login (web/src/pages/login.tsx) — same gradient backdrop,
// branded card, theme toggle, shared Input/Label controls, and password
// reveal — so the two consoles feel like one product. The auth model stays
// telemetry-specific: there is no /auth/me on the telemetry service, so we
// store the credentials and let the first gated API call surface a 401 (the
// pages route back here on UnauthorizedError). The form only guards against an
// empty submit locally.
function LoginPage() {
  const nav = useNavigate()
  const [theme, , toggleTheme] = useTheme()

  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [showPw, setShowPw] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)
    if (!username || !password) {
      setError("Enter your operator credentials.")
      return
    }
    auth.store(username, password)
    nav("/", { replace: true })
  }

  return (
    <div
      className="min-h-screen grid place-items-center px-6 py-8 relative"
      style={{
        backgroundImage:
          "radial-gradient(ellipse 80% 60% at 50% 0%, color-mix(in oklab, var(--accent) 8%, transparent), transparent 70%), radial-gradient(ellipse 60% 50% at 50% 100%, color-mix(in oklab, var(--accent) 5%, transparent), transparent 70%)",
        background: "var(--bg)",
      }}
    >
      <Button
        variant="ghost"
        size="icon"
        onClick={toggleTheme}
        aria-label="Toggle theme"
        className="absolute top-4 right-4"
      >
        {theme === "dark" ? <Sun /> : <Moon />}
      </Button>

      <div
        className="w-full max-w-[380px] rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-8"
        style={{ boxShadow: "var(--shadow-md)" }}
      >
        <div className="flex items-center gap-2.5 pb-5 mb-5 border-b border-[color:var(--border)]">
          <img src="/arbiter.svg" alt="" width={20} height={20} className="rounded-[5px]" />
          <span className="text-[16px] font-semibold tracking-tight">Arbiter</span>
        </div>

        <h1 className="text-[20px] font-semibold tracking-[-0.02em] mb-5">Sign in</h1>

        <form onSubmit={submit} className="flex flex-col gap-3.5">
          <div className="flex flex-col gap-1.5">
            <Label
              htmlFor="login-user"
              className="text-[11px] font-medium uppercase tracking-[0.07em] text-[color:var(--text-3)]"
            >
              Username
            </Label>
            <Input
              id="login-user"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="operator"
              autoComplete="username"
              autoFocus
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label
              htmlFor="login-pw"
              className="text-[11px] font-medium uppercase tracking-[0.07em] text-[color:var(--text-3)]"
            >
              Password
            </Label>
            <div className="relative">
              <Input
                id="login-pw"
                type={showPw ? "text" : "password"}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
                autoComplete="current-password"
                className="pr-10"
              />
              <button
                type="button"
                onClick={() => setShowPw((s) => !s)}
                aria-label={showPw ? "Hide password" : "Show password"}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-[color:var(--text-3)] hover:text-[color:var(--text)] transition-colors"
              >
                {showPw ? <EyeOff size={14} /> : <Eye size={14} />}
              </button>
            </div>
          </div>

          {error && (
            <div
              className="text-[12.5px] rounded-[var(--radius)] px-2.5 py-2 border"
              style={{
                color: "var(--err)",
                background: "var(--err-bg)",
                borderColor: "color-mix(in oklab, var(--err) 30%, var(--border))",
              }}
            >
              {error}
            </div>
          )}

          <Button type="submit" className="w-full h-9 mt-1">
            Sign in
          </Button>
        </form>
      </div>
    </div>
  )
}
