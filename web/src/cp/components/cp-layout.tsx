import { useEffect, useState } from "react"
import { NavLink, Outlet, useNavigate } from "react-router"
import { LogOut, Moon, Server, Sun } from "lucide-react"
import { cn } from "@/lib/utils"
import { auth } from "@/lib/auth"
import { useTheme } from "@/lib/theme"
import { Button } from "@/components/ui/button"
import { fetchVersion } from "../lib/api"

const NAV = [{ to: "/fleet", label: "Fleet", icon: Server }]

export function CpLayout() {
  const [theme, , toggleTheme] = useTheme()
  const nav = useNavigate()
  const version = useVersion()

  const signOut = () => {
    auth.clear()
    nav("/login", { replace: true })
  }

  return (
    <div className="flex min-h-screen bg-[color:var(--bg)]">
      <aside
        className="flex flex-col bg-[color:var(--bg-1)] border-r border-[color:var(--border)]"
        style={{ width: "var(--sidebar-w)" }}
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
            <span className="mono text-[10.5px] text-[color:var(--text-4)] truncate">control plane</span>
          </div>
        </div>

        <nav className="flex-1 py-2 overflow-y-auto">
          {NAV.map((item) => (
            <NavLink
              key={item.to}
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
              <item.icon size={14} className="shrink-0" />
              <span>{item.label}</span>
            </NavLink>
          ))}
        </nav>

        <div className="flex items-center gap-2 px-3.5 py-3 border-t border-[color:var(--border)] text-[11.5px] text-[color:var(--text-3)]">
          <span className="inline-block size-1.5 rounded-full" style={{ background: "var(--ok)" }} />
          <span>control plane</span>
          <span className="mono ml-auto text-[color:var(--text-4)]" title={version ?? ""}>
            {version ?? "…"}
          </span>
        </div>
      </aside>

      <div className="flex flex-col flex-1 min-w-0">
        <header className="flex items-center gap-2 h-12 px-4 sm:px-7 border-b border-[color:var(--border)] bg-[color:var(--bg-1)]">
          <div className="ml-auto flex items-center gap-1">
            <Button variant="ghost" size="icon" onClick={toggleTheme} aria-label="Toggle theme">
              {theme === "dark" ? <Sun /> : <Moon />}
            </Button>
            <Button variant="ghost" size="icon" onClick={signOut} aria-label="Sign out">
              <LogOut />
            </Button>
          </div>
        </header>
        <div className="flex-1 px-4 sm:px-7 pt-6 pb-18 overflow-auto">
          <Outlet />
        </div>
      </div>
    </div>
  )
}

function useVersion(): string | null {
  const [version, setVersion] = useState<string | null>(null)
  useEffect(() => {
    let cancelled = false
    fetchVersion()
      .then((v) => {
        if (!cancelled) setVersion(v)
      })
      .catch(() => {
        /* cosmetic */
      })
    return () => {
      cancelled = true
    }
  }, [])
  return version
}
