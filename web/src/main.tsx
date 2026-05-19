import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { BrowserRouter } from "react-router"
import "./index.css"
import App from "./App.tsx"
import { bootstrapTheme } from "@/lib/theme"

bootstrapTheme()

// Both the SPA bundle and the API live under /admin (see
// internal/admin.Prefix on the Go side); BrowserRouter basename keeps
// react-router URLs aligned with what's actually served.
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter basename="/admin">
      <App />
    </BrowserRouter>
  </StrictMode>,
)
