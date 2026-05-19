import { Navigate, useLocation } from "react-router"
import { auth } from "@/lib/auth"

export function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const loc = useLocation()
  if (!auth.isLoggedIn()) {
    return <Navigate to="/login" state={{ from: loc.pathname }} replace />
  }
  return <>{children}</>
}
