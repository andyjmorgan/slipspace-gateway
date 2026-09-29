import { Navigate, useLocation } from "react-router"
import { auth } from "@/lib/auth"
import { LOGIN_PATH, loginRedirectState } from "@/lib/login-redirect"

export function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const loc = useLocation()
  if (!auth.isLoggedIn()) {
    return <Navigate to={LOGIN_PATH} state={loginRedirectState(loc)} replace />
  }
  return <>{children}</>
}
