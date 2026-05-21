import { useState } from "react"
import { Outlet } from "react-router"
import { Sidebar } from "@/components/sidebar"
import { Topbar } from "@/components/topbar"

export function AppLayout() {
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  return (
    <div className="flex min-h-screen bg-[color:var(--bg)]">
      <Sidebar isOpen={mobileNavOpen} onClose={() => setMobileNavOpen(false)} />
      {mobileNavOpen && (
        <button
          type="button"
          aria-label="Close navigation"
          className="fixed inset-0 z-30 bg-black/40 md:hidden"
          onClick={() => setMobileNavOpen(false)}
        />
      )}
      <div className="flex flex-col flex-1 min-w-0">
        <Topbar onMenuToggle={() => setMobileNavOpen((v) => !v)} />
        <div className="flex-1 px-7 pt-6 pb-18 overflow-auto">
          <Outlet />
        </div>
      </div>
    </div>
  )
}
