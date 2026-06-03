import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { BrowserRouter } from "react-router"
import "@/index.css"
import App from "./App.tsx"
import { bootstrapTheme } from "@/lib/theme"

bootstrapTheme()

// The control-plane console is served at the listener root (no /admin
// prefix), so the router basename is "/".
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter basename="/">
      <App />
    </BrowserRouter>
  </StrictMode>,
)
