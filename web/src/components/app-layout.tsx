import { useState, type ReactNode } from "react"
import { Outlet } from "react-router"
import { Sidebar } from "@/components/sidebar"
import { Topbar, type TopbarTitles } from "@/components/topbar"
import type { NavMeta } from "@/lib/nav-meta"

// AppLayout is the shared console shell — the nav rail, the topbar, and the
// scrolling content pane with the routed Outlet. It is parameterised by the
// nav list, version fetcher, sidebar footer, and topbar titles so the gateway
// admin SPA and the control-plane console render the identical shell with
// their own contents. Every prop defaults to the gateway's, so the gateway's
// callsite is unchanged.
export function AppLayout({
  nav,
  versionFetch,
  footer,
  titles,
}: {
  nav?: NavMeta[]
  versionFetch?: () => Promise<string>
  footer?: ReactNode
  titles?: TopbarTitles
} = {}) {
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  return (
    <div className="flex min-h-screen bg-[color:var(--bg)]">
      <Sidebar
        isOpen={mobileNavOpen}
        onClose={() => setMobileNavOpen(false)}
        nav={nav}
        versionFetch={versionFetch}
        footer={footer}
      />
      {mobileNavOpen && (
        <button
          type="button"
          aria-label="Close navigation"
          className="fixed inset-0 z-30 bg-black/40 md:hidden"
          onClick={() => setMobileNavOpen(false)}
        />
      )}
      <div className="flex flex-col flex-1 min-w-0">
        <Topbar onMenuToggle={() => setMobileNavOpen((v) => !v)} titles={titles} />
        <div className="flex-1 px-4 sm:px-7 pt-6 pb-18 overflow-auto">
          <Outlet />
        </div>
      </div>
    </div>
  )
}
