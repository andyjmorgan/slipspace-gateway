import { useCallback, useEffect, useRef } from "react"
import { useLocation, useNavigate } from "react-router"
import { LOGIN_PATH, locationPath } from "@/lib/login-redirect"

/**
 * useLoginRedirect returns a callback that bounces to /login while recording
 * the current location, so a live 401 (expired or mistyped credentials)
 * returns the operator to the same deep link after they sign in again.
 *
 * The location is read through a ref rather than closed over so the callback
 * stays stable across in-page navigations (e.g. `#span=` hash changes);
 * effects that list it as a dependency would otherwise refetch on every one.
 */
export function useLoginRedirect(): () => void {
  const nav = useNavigate()
  const loc = useLocation()
  const fromRef = useRef(locationPath(loc))
  useEffect(() => {
    fromRef.current = locationPath(loc)
  }, [loc])
  return useCallback(() => {
    nav(LOGIN_PATH, { replace: true, state: { from: fromRef.current } })
  }, [nav])
}
