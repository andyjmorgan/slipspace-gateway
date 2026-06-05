import { useState } from "react"
import { NavLink, Navigate, Outlet, Route, Routes, useNavigate } from "react-router"
import { Activity, Eye, EyeOff, LayoutDashboard, ListTree, LogOut, Menu, Moon, Sun } from "lucide-react"
import { auth } from "@/lib/auth"
import { useTheme } from "@/lib/theme"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
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
          <Activity size={20} className="text-[color:var(--accent)]" />
          <span className="text-[16px] font-semibold tracking-tight">Sluice Telemetry</span>
        </div>

        <h1 className="text-[20px] font-semibold tracking-[-0.02em] mb-1">Sign in</h1>
        <p className="text-[13px] text-[color:var(--text-3)] mb-5">Read-only observability console</p>

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
