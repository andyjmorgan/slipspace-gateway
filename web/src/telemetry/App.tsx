import { useState } from "react"
import { NavLink, Navigate, Outlet, Route, Routes, useNavigate } from "react-router"
import { Activity, LayoutDashboard, ListTree, LogOut, Menu, Moon, Sun } from "lucide-react"
import { auth } from "@/lib/auth"
import { useTheme } from "@/lib/theme"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { DashboardPage } from "./pages/dashboard"
import { MessagesPage } from "./pages/messages"

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
        </Route>
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}

// Guard redirects to /login when no Basic credentials are cached. The pages
// themselves still handle a live 401 (rejected creds) by routing back here.
function Guard() {
  if (!auth.isLoggedIn()) {
    return <Navigate to="/login" replace />
  }
  return <Outlet />
}

const NAV = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard, end: true },
  { to: "/messages", label: "Messages", icon: ListTree, end: false },
]

function TelemetryLayout() {
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
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
        <Activity size={18} className="text-[color:var(--accent)]" />
        <span className="font-semibold tracking-[-0.01em]">Sluice Telemetry</span>
      </div>
      <nav className="flex-1 px-2 py-3 flex flex-col gap-0.5">
        {NAV.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.end}
            onClick={onClose}
            className={({ isActive }) =>
              cn(
                "flex items-center gap-2.5 rounded-[var(--radius)] px-3 py-2 text-[13px] transition-colors",
                isActive
                  ? "bg-[color:var(--hover)] text-[color:var(--text)]"
                  : "text-[color:var(--text-3)] hover:bg-[color:var(--hover)] hover:text-[color:var(--text)]",
              )
            }
          >
            <item.icon size={16} />
            {item.label}
          </NavLink>
        ))}
      </nav>
      <div className="px-4 py-3 border-t border-[color:var(--border)] text-[11px] text-[color:var(--text-4)]">
        Read-only observability
      </div>
    </aside>
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

function LoginPage() {
  const nav = useNavigate()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    // No /auth/me on the telemetry service — store the credentials and let the
    // first API call surface a 401 if they're wrong (the pages route back here
    // on UnauthorizedError).
    auth.store(username, password)
    nav("/", { replace: true })
  }

  return (
    <div className="min-h-screen grid place-items-center bg-[color:var(--bg)] px-4">
      <form
        onSubmit={submit}
        className="w-full max-w-sm rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-6 flex flex-col gap-4"
      >
        <div className="flex items-center gap-2">
          <Activity size={18} className="text-[color:var(--accent)]" />
          <h1 className="font-semibold tracking-[-0.01em]">Sluice Telemetry</h1>
        </div>
        <label className="flex flex-col gap-1 text-[13px]">
          <span className="text-[color:var(--text-3)]">Username</span>
          <input
            className="rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-2)] px-3 py-2 text-[13px] outline-none focus:border-[color:var(--border-strong)]"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            autoFocus
          />
        </label>
        <label className="flex flex-col gap-1 text-[13px]">
          <span className="text-[color:var(--text-3)]">Password</span>
          <input
            type="password"
            className="rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-2)] px-3 py-2 text-[13px] outline-none focus:border-[color:var(--border-strong)]"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
          />
        </label>
        <Button type="submit" className="mt-1">Sign in</Button>
      </form>
    </div>
  )
}
