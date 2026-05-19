import { Outlet } from "react-router"
import { Sidebar } from "@/components/sidebar"
import { Topbar } from "@/components/topbar"

export function AppLayout() {
  return (
    <div className="flex min-h-screen bg-[color:var(--bg)]">
      <Sidebar />
      <div className="flex flex-col flex-1 min-w-0">
        <Topbar />
        <div className="flex-1 px-7 pt-6 pb-18 overflow-auto">
          <Outlet />
        </div>
      </div>
    </div>
  )
}
