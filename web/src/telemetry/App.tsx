import { useState } from "react"
import { Navigate, NavLink, Route, Routes, useNavigate } from "react-router"
import { auth } from "@/lib/auth"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Card } from "@/components/ui/card"
import { DashboardPage } from "./pages/dashboard"
import { MessagesPage } from "./pages/messages"

// Guard redirects to /login unless Basic credentials are cached. The telemetry
// service has no separate session endpoint; a cached credential is treated as
// logged-in and the first API 401 routes back here (apiFetch clears the cache).
function Guard({ children }: { children: React.ReactNode }) {
  if (!auth.isLoggedIn()) {
    return <Navigate to="/login" replace />
  }
  return <>{children}</>
}

function LoginPage() {
  const nav = useNavigate()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    auth.store(username, password)
    nav("/", { replace: true })
  }

  return (
    <div className="flex min-h-screen items-center justify-center p-4">
      <Card className="w-full max-w-sm space-y-4 p-6">
        <div>
          <h1 className="text-lg font-semibold">Sluice Telemetry</h1>
          <p className="text-muted-foreground text-sm">Sign in to the central console</p>
        </div>
        <form className="space-y-3" onSubmit={submit}>
          <Input placeholder="username" value={username} onChange={(e) => setUsername(e.target.value)} autoFocus />
          <Input
            type="password"
            placeholder="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <Button type="submit" className="w-full">
            Sign in
          </Button>
        </form>
      </Card>
    </div>
  )
}

function Shell({ children }: { children: React.ReactNode }) {
  const nav = useNavigate()
  const logout = () => {
    auth.clear()
    nav("/login", { replace: true })
  }
  const linkCls = ({ isActive }: { isActive: boolean }) =>
    `rounded px-3 py-1.5 text-sm ${isActive ? "bg-muted font-medium" : "text-muted-foreground hover:text-foreground"}`
  return (
    <div className="min-h-screen">
      <header className="flex items-center gap-2 border-b px-4 py-2">
        <span className="font-semibold">Sluice Telemetry</span>
        <nav className="ml-4 flex gap-1">
          <NavLink to="/" end className={linkCls}>
            Dashboard
          </NavLink>
          <NavLink to="/messages" className={linkCls}>
            Messages
          </NavLink>
        </nav>
        <Button variant="ghost" size="sm" className="ml-auto" onClick={logout}>
          Sign out
        </Button>
      </header>
      <main className="p-4">{children}</main>
    </div>
  )
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route
        path="/"
        element={
          <Guard>
            <Shell>
              <DashboardPage />
            </Shell>
          </Guard>
        }
      />
      <Route
        path="/messages"
        element={
          <Guard>
            <Shell>
              <MessagesPage />
            </Shell>
          </Guard>
        }
      />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
