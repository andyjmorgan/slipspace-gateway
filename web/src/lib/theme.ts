import { useEffect, useState } from "react"

export type Theme = "dark" | "light"
const KEY = "sluice.theme"

function read(): Theme {
  const stored = localStorage.getItem(KEY)
  if (stored === "dark" || stored === "light") return stored
  return "dark"
}

function apply(theme: Theme) {
  const root = document.documentElement
  root.classList.toggle("dark", theme === "dark")
  root.classList.toggle("light", theme === "light")
}

export function useTheme(): [Theme, (t: Theme) => void, () => void] {
  const [theme, setTheme] = useState<Theme>(() => read())
  useEffect(() => {
    apply(theme)
    localStorage.setItem(KEY, theme)
  }, [theme])
  const toggle = () => setTheme((t) => (t === "dark" ? "light" : "dark"))
  return [theme, setTheme, toggle]
}

export function bootstrapTheme() {
  apply(read())
}
