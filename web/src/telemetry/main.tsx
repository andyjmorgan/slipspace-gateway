import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { BrowserRouter } from "react-router"
import "@/index.css"
import { bootstrapTheme } from "@/lib/theme"
import App from "./App.tsx"

bootstrapTheme()

// The telemetry console is served at the listener root (no /admin prefix).
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter basename="/">
      <App />
    </BrowserRouter>
  </StrictMode>,
)
