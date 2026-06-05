import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { BrowserRouter } from "react-router"
import "@/index.css"
import { bootstrapTheme } from "@/lib/theme"
import { setApiBase } from "@/lib/api"
import App from "./App.tsx"

// The telemetry console serves its API at the listener root (no /admin
// prefix), so the shared data hooks must target "" instead of the gateway's
// "/admin". Set before any fetch fires.
setApiBase("")
bootstrapTheme()

// The telemetry console is served at the listener root (no /admin prefix).
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter basename="/">
      <App />
    </BrowserRouter>
  </StrictMode>,
)
