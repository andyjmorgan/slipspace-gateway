import { useEffect, useState } from "react"
import { useNavigate, useLocation } from "react-router"
import { Eye, EyeOff, Moon, Sun } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { auth } from "@/lib/auth"
import { fetchVersion, validateSession } from "@/lib/api"
import { useTheme } from "@/lib/theme"

export function LoginPage() {
  const nav = useNavigate()
  const loc = useLocation()
  const [theme, , toggleTheme] = useTheme()

  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [showPw, setShowPw] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [version, setVersion] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    fetchVersion()
      .then((v) => {
        if (!cancelled) setVersion(v)
      })
      .catch(() => {
        /* cosmetic — swallow */
      })
    return () => {
      cancelled = true
    }
  }, [])

  const from = (loc.state as { from?: string } | null)?.from ?? "/dashboard"

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)
    if (!username || !password) {
      setError("Enter your operator credentials.")
      return
    }
    setBusy(true)
    auth.store(username, password)
    try {
      const okSession = await validateSession()
      if (!okSession) {
        setError("Invalid credentials.")
        setBusy(false)
        return
      }
      nav(from, { replace: true })
    } catch {
      auth.clear()
      setError("Could not reach the gateway. Check that the admin listener is up.")
      setBusy(false)
    }
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
          <img
            src={`${import.meta.env.BASE_URL}sluice.png`}
            alt=""
            width={36}
            height={36}
            className="size-9 rounded-md"
          />
          <span className="text-[16px] font-semibold tracking-tight">sluice</span>
          <span className="mono ml-auto text-[11px] text-[color:var(--text-4)]">{version ?? "…"}</span>
        </div>

        <h1 className="text-[20px] font-semibold tracking-[-0.02em] mb-1">Sign in</h1>
        <p className="text-[13px] text-[color:var(--text-3)] mb-5">
          Management console — gateway <span className="mono">:8081</span>
        </p>

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

          <Button type="submit" className="w-full h-9 mt-1" disabled={busy}>
            {busy ? "Signing in…" : "Sign in"}
          </Button>
        </form>

        <div className="flex items-center gap-1.5 flex-wrap mt-6 pt-4 border-t border-[color:var(--border)] text-[12px] text-[color:var(--text-3)]">
          <span className="mono">cluster-internal access</span>
          <span className="text-[color:var(--text-4)]">·</span>
          <span>contact your platform admin for credentials</span>
        </div>
      </div>
    </div>
  )
}
